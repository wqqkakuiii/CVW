package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// 要跳过的目录名（路径某级目录名完全匹配则跳过）
var skipDirs = []string{}

// 要跳过的关键字（路径中包含该关键字则跳过，如 test 会跳过 testdata、mytest 等）
var skipKeywords = []string{
	"test",
}

func main() {
	dir := "."
	if len(os.Args) > 1 {
		dir = os.Args[1]
		if dir == "std" || dir == "stdlib" {
			cmd := exec.Command("go", "env", "GOROOT")
			cmd.Stderr = nil
			if out, err := cmd.Output(); err == nil {
				dir = filepath.Join(strings.TrimSpace(string(out)), "src")
			}
		}
	}
	cwd, _ := os.Getwd()

	absDir, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "解析目录失败: %v\n", err)
		os.Exit(1)
	}
	info, err := os.Stat(absDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "目录不存在或无法访问: %v\n", err)
		os.Exit(1)
	}
	if !info.IsDir() {
		fmt.Fprintf(os.Stderr, "不是目录: %s\n", absDir)
		os.Exit(1)
	}

	// 日志统一写到当前工作目录，避免对只读目录（如 GOROOT/src）写入失败
	logPath := filepath.Join(cwd, "countfunc_pkgs.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建日志文件失败: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()

	// 失败包单独日志
	failedLogPath := filepath.Join(cwd, "countfunc_failed.log")
	failedLogFile, err := os.Create(failedLogPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建失败日志文件失败: %v\n", err)
		os.Exit(1)
	}
	defer failedLogFile.Close()

	pkgDirs := findPackageDirs(absDir)
	totalFuncs := 0
	skippedByDir := 0
	skippedBySSA := 0

	for _, pkg := range pkgDirs {
		abs, _ := filepath.Abs(pkg.dir)
		if underSkipDir(abs) {
			skippedByDir++
			fmt.Fprintf(logFile, "%s\t%d\t%s\n", abs, 0, "skip_dir")
			//fmt.Fprintf(failedLogFile, "%s\t%d\t%s\n", abs, 0, "skip_dir")
			continue
		}
		if !canBuildSSA(abs) {
			skippedBySSA++
			fmt.Fprintf(logFile, "%s\t%d\t%s\n", abs, 0, "ssa_failed")
			fmt.Fprintf(failedLogFile, "%s\t%d\t%s\n", abs, 0, "ssa_failed")
			continue
		}
		n, _ := countPkgFuncs(pkg.dir)
		totalFuncs += n
		fmt.Fprintf(logFile, "%s\t%d\t%s\n", abs, n, "ok")
	}

	countedPkgs := len(pkgDirs) - skippedByDir - skippedBySSA
	fmt.Printf("目录: %s\n", absDir)
	fmt.Printf("包总数: %d（计入 %d，按目录跳过 %d，SSA 失败跳过 %d）\n",
		len(pkgDirs), countedPkgs, skippedByDir, skippedBySSA)
	fmt.Printf("函数总数: %d\n", totalFuncs)
	fmt.Printf("包详细统计已写入: %s\n", logPath)
	fmt.Printf("失败包日志已写入: %s\n", failedLogPath)
}

// findPackageDirs 返回所有包含 .go 文件的包目录
func findPackageDirs(root string) []struct{ dir string } {
	var list []struct{ dir string }
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		entries, _ := os.ReadDir(path)
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
				list = append(list, struct{ dir string }{dir: path})
				break
			}
		}
		return nil
	})
	return list
}

func findModuleRoot(start string) string {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// canBuildSSA 使用与插桩流程相同的标准来判断包是否能构建 SSA：
// - 直接对包目录（绝对路径）调用 packages.Load(LoadSyntax)
// - 若 Load 失败或存在类型错误则视为失败
// - 使用 x/tools/ssa（LogSource 模式）构建 SSA 包
func canBuildSSA(pkgDir string) bool {
	absPkg, err := filepath.Abs(pkgDir)
	if err != nil {
		return false
	}

	cfg := &packages.Config{
		Mode: packages.LoadSyntax,
	}
	pkgs, err := packages.Load(cfg, absPkg)
	if err != nil || len(pkgs) == 0 {
		return false
	}

	// 与 BuildSSAForExample 一致：若存在类型错误则认为构建失败
	if n := packages.PrintErrors(pkgs); n > 0 {
		return false
	}

	prog, ssaPkgs := ssautil.Packages(pkgs, ssa.LogSource)
	if len(ssaPkgs) == 0 || ssaPkgs[0] == nil {
		return false
	}
	ssaPkgs[0].Build()
	_ = prog
	return true
}

func underSkipDir(absPath string) bool {
	pathSlash := filepath.ToSlash(absPath)
	parts := strings.Split(pathSlash, "/")
	for _, part := range parts {
		for _, skip := range skipDirs {
			if part == skip {
				return true
			}
		}
	}
	for _, kw := range skipKeywords {
		if strings.Contains(pathSlash, kw) {
			return true
		}
	}
	return false
}

func countPkgFuncs(pkgDir string) (int, error) {
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		n, err := countFuncsInFile(filepath.Join(pkgDir, e.Name()))
		if err != nil {
			return 0, err // 任一文件解析失败则包视为失败，不累加
		}
		total += n
	}
	return total, nil
}

func countFuncsInFile(path string) (int, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return 0, err
	}
	count := 0
	ast.Inspect(node, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncDecl); ok {
			count++
		}
		return true
	})
	return count, nil
}
