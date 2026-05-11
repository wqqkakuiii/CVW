package main

import (
	"fmt"
	"log"
	"os"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

func main() {
	// 1) 加载 example 目录下的包，带类型信息
	cfg := &packages.Config{Mode: packages.LoadSyntax}
	initial, err := packages.Load(cfg, "../example")
	if err != nil {
		log.Fatal(err)
	}

	// 2) 若有错误则退出
	if packages.PrintErrors(initial) > 0 {
		log.Fatal("type errors")
	}

	// 3) 创建 SSA 程序和包，使用 LogSource 模式显示源代码位置
	prog, pkgs := ssautil.Packages(initial, ssa.LogSource)
	_ = prog
	// 4) 为每个包构建 SSA
	for _, pkg := range pkgs {
		if pkg == nil {
			continue
		}
		pkg.Build()

		// 5) 遍历每个函数打印其 SSA（带源代码位置）
		fmt.Printf("\n=== 包: %s ===\n\n", pkg.Pkg.Name())
		for _, mem := range pkg.Members {
			if fn, ok := mem.(*ssa.Function); ok {
				fmt.Printf("--- 函数: %s ---\n", fn.Name())
				fn.WriteTo(os.Stdout)
				fmt.Println()
			}
		}
	}
}
