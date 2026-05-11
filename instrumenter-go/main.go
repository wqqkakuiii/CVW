package main

import (
	"bufio"
	"flag"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// copyDirRecursive 递归将 srcDir 完整复制到 dstDir
func copyDirRecursive(srcDir, dstDir string) error {
	return filepath.WalkDir(srcDir, func(srcPath string, d os.DirEntry, errWalk error) error {
		if errWalk != nil {
			return errWalk
		}
		rel, err := filepath.Rel(srcDir, srcPath)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dstDir, rel)
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			return os.MkdirAll(dstPath, 0755)
		}
		_ = os.MkdirAll(filepath.Dir(dstPath), 0755)
		src, err := os.Open(srcPath)
		if err != nil {
			return err
		}
		dst, err := os.Create(dstPath)
		if err != nil {
			src.Close()
			return err
		}
		_, err = io.Copy(dst, src)
		src.Close()
		dst.Close()
		return err
	})
}

func main() {
	inputDir := flag.String("input", "", "要插桩的根目录（递归所有子目录中的包），必填")
	outDir := flag.String("out-dir", "", "插桩副本输出目录，必填")
	consumeGasOnly := flag.Bool("consume-gas-only", false, "仅插桩 registry.ConsumeGas")
	flag.Parse()

	if strings.TrimSpace(*inputDir) == "" {
		log.Fatal("必须指定 -input（要插桩的目录）")
	}
	if strings.TrimSpace(*outDir) == "" {
		log.Fatal("必须指定 -out-dir")
	}

	outRoot := filepath.Clean(*outDir)
	_ = os.MkdirAll(outRoot, 0755)
	logPath := filepath.Join(outRoot, "instrumenter-go.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Fatalf("无法创建日志文件 %s: %v", logPath, err)
	}
	defer logFile.Close()
	log.SetOutput(io.MultiWriter(os.Stderr, logFile))
	log.Printf("日志写入: %s", logPath)

	root := filepath.Clean(*inputDir)
	info, err := os.Stat(root)
	if err != nil {
		log.Fatalf("input 目录不存在: %v", err)
	}
	if !info.IsDir() {
		log.Fatal("-input 必须是目录")
	}

	cvwRoot := findCVWRoot()

	// 1) 生成 os 及其递归依赖的黑名单
	// blacklist, err := buildOSDepsBlacklist(cvwRoot)
	// if err != nil {
	// 	log.Fatalf("生成 os 依赖黑名单失败: %v", err)
	// }
	// log.Printf("黑名单共 %d 个包（os 及其递归依赖）", len(blacklist))

	// 2) 先将输入目录完整复制到输出目录
	if err := copyDirRecursive(root, outRoot); err != nil {
		log.Fatalf("复制目录失败: %v", err)
	}
	log.Printf("已复制 %s -> %s", root, outRoot)

	// 3) 递归收集所有“包目录”并插桩，插桩结果替换输出目录中的对应文件
	packages := findPackageDirs(root)
	if len(packages) == 0 {
		log.Fatal("目录下（含子目录）没有 .go 文件")
	}

	// 按目录名前缀精确跳过的列表
	skipDirList := []string{
		// "cmd",
		// "crypto",
		// "internal",
		// "net",
		// "go",
		// "os",
		// "vendor",
	}

	// 按关键字模糊跳过的列表，只要 importPath 中包含任意关键字就跳过
	// 例如：testdata、testsrc、xxx_test 都会因为包含 "test" 被跳过
	skipKeywordList := []string{
		"test",
		"runtime",
		"internal",
	}

	skipped, done, failed := 0, 0, 0
	for _, p := range packages {
		importPath := filepath.ToSlash(p.relPath)
		if importPath == "." {
			importPath = filepath.Base(root)
		}
		// if blacklist[importPath] {
		// 	skipped++
		// 	log.Printf("跳过 [黑名单] %s", importPath)
		// 	continue
		// }
		// 先按关键字跳过
		skippedByKeyword := false
		for _, kw := range skipKeywordList {
			if strings.Contains(importPath, kw) {
				skipped++
				log.Printf("跳过 [关键字: %s] %s", kw, importPath)
				skippedByKeyword = true
				break
			}
		}
		if skippedByKeyword {
			continue
		}

		// 再按目录前缀精确匹配跳过
		skippedByDir := false
		for _, skipDir := range skipDirList {
			if importPath == skipDir || strings.HasPrefix(importPath, skipDir+"/") {
				skipped++
				log.Printf("跳过 [排除目录] %s", importPath)
				skippedByDir = true
				break
			}
		}
		if skippedByDir {
			continue
		}
		outPkgDir := filepath.Join(outRoot, p.relPath)
		args := []string{"run", "./instrumenter", "-input", p.dir, "-output", outPkgDir}
		if *consumeGasOnly {
			args = append(args, "-consume-gas-only")
		}
		cmd := exec.Command("go", args...)
		cmd.Dir = cvwRoot
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			failed++
			log.Printf("失败 [%s]: %v，恢复原文件", importPath, err)
			if err := copyDirRecursive(p.dir, outPkgDir); err != nil {
				log.Printf("恢复失败包文件失败 [%s]: %v", importPath, err)
			}
			continue
		}
		done++
	}
	log.Printf("--- 结束：已插桩 %d 个包，跳过 %d 个包，失败 %d 个包 ---", done, skipped, failed)
}

// buildOSDepsBlacklist 运行 go list -deps os 得到 os 及其递归依赖的包列表，作为黑名单
func buildOSDepsBlacklist(workDir string) (map[string]bool, error) {
	cmd := exec.Command("go", "list", "-deps", "os")
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool)
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		p := strings.TrimSpace(sc.Text())
		if p != "" {
			set[p] = true
		}
	}
	return set, sc.Err()
}

type pkgDir struct {
	dir     string
	relPath string
}

// findPackageDirs 递归遍历 root，返回所有“包目录”（该目录下直接含有 .go 文件的目录）及相对 root 的路径
func findPackageDirs(root string) []pkgDir {
	var list []pkgDir
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, errWalk error) error {
		if errWalk != nil || !d.IsDir() {
			return errWalk
		}
		entries, errRead := os.ReadDir(path)
		if errRead != nil {
			return errRead
		}
		var hasGo bool
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if strings.HasSuffix(strings.ToLower(e.Name()), ".go") {
				hasGo = true
				break
			}
		}
		if !hasGo {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		list = append(list, pkgDir{dir: path, relPath: rel})
		return nil
	})
	return list
}

func findCVWRoot() string {
	dir, _ := os.Getwd()
	for {
		if b, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil && strings.Contains(string(b), "module CVW") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}
