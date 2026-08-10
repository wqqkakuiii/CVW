package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"go/parser"
	"log"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
)

func ensureWasmExportComment(src, funcName string) string {
	directive := "//go:wasmexport " + funcName
	if strings.Contains(src, directive) {
		return src
	}
	signature := "func " + funcName + "("
	idx := strings.Index(src, signature)
	if idx < 0 {
		return src
	}
	return src[:idx] + directive + "\n" + src[idx:]
}

func main() {
	// 定义命令行参数（仅插桩模式）
	var (
		consumeGasOnly     = flag.Bool("consume-gas-only", false, "仅插桩 registry.ConsumeGas，不添加 GAS/Register，插桩结束后不添加注释")
		fillEmptyPositions = flag.Bool("fill-empty-positions", true, "对位置为空的指令进行补全（使用前一条指令的位置）")
		inputFile          = flag.String("input", "../example/test.go", "输入文件路径")
		outputFile         = flag.String("output", "../output/formatTest.go", "输出文件路径")
		gasZeroBlacklist   = flag.String("gas-zero-blacklist", "", "gas 净消耗为 0 的函数黑名单文件（每行 package.FuncName）")
	)

	// 解析命令行参数
	flag.Parse()

	blacklist, err := LoadGasZeroBlacklist(*gasZeroBlacklist)
	if err != nil {
		log.Fatalf("加载 gas-zero 黑名单失败: %v", err)
	}
	if blacklist.Len() > 0 {
		log.Printf("已加载 gas-zero 黑名单 %d 条", blacklist.Len())
	}

	inputPath := filepath.Clean(*inputFile)
	outputPath := filepath.Clean(*outputFile)
	info, err := os.Stat(inputPath)
	if err != nil {
		log.Fatalf("input 不存在: %v", err)
	}

	var pkgDir string
	var filesToProcess []string
	var outputIsDir bool

	if info.IsDir() {
		// 按包插桩：input 为包目录，output 为输出目录
		pkgDir = inputPath
		outInfo, err := os.Stat(outputPath)
		if err != nil {
			if !os.IsNotExist(err) {
				log.Fatalf("output 无效: %v", err)
			}
		} else if !outInfo.IsDir() {
			log.Fatal("-input 为目录时 -output 必须为目录")
		}
		_ = os.MkdirAll(outputPath, 0755)
		outputIsDir = true

		entries, err := os.ReadDir(pkgDir)
		if err != nil {
			log.Fatalf("读取包目录失败: %v", err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(strings.ToLower(name), ".go") {
				continue
			}
			filesToProcess = append(filesToProcess, filepath.Join(pkgDir, name))
		}
		if len(filesToProcess) == 0 {
			log.Fatal("包目录下没有 .go 文件")
		}
	} else {
		// 单文件模式
		pkgDir = filepath.Dir(inputPath)
		filesToProcess = []string{inputPath}
		outputIsDir = false
	}

	// 一次构建 SSA（整包）
	pool, err := BuildSSAForExample(pkgDir, *fillEmptyPositions, "", "")
	if err != nil {
		log.Fatalf("构建 SSA 指令池失败: %v", err)
	}
	pool.GasTable = DefaultInstructionGasTable
	log.Printf("SSA 指令池构建成功，共 %d 条指令，待插桩 %d 个文件", pool.InstructionCount(), len(filesToProcess))

	fset := pool.FileSet
	fmt.Println("--- 开始 AST 插桩 ---")

	for _, filename := range filesToProcess {
		node, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
		if err != nil {
			log.Printf("解析失败 %s: %v", filename, err)
			continue
		}

		inserted, err := ApplyInstrumentation(node, pool, fset, *consumeGasOnly, blacklist)
		if err != nil {
			log.Printf("插桩失败 %s: %v", filename, err)
			continue
		}

		if inserted {
			astutil.AddImport(fset, node, "CVW/registry")
		}
		var buf bytes.Buffer
		if err := format.Node(&buf, fset, node); err != nil {
			log.Printf("格式化失败 %s: %v", filename, err)
			continue
		}
		outputContent := buf.String()
		if !*consumeGasOnly && inserted {
			outputContent = ensureWasmExportComment(outputContent, "SetGas")
			outputContent = ensureWasmExportComment(outputContent, "GetGas")
		}

		var outFile string
		if outputIsDir {
			outFile = filepath.Join(outputPath, filepath.Base(filename))
		} else {
			outFile = outputPath
		}
		if err := os.WriteFile(outFile, []byte(outputContent), 0644); err != nil {
			log.Printf("写入失败 %s: %v", outFile, err)
			continue
		}
		fmt.Printf("已生成 %s\n", outFile)
	}

	fmt.Println("--- 插桩结束 ---")
}
