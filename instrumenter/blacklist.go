package main

import (
	"bufio"
	"fmt"
	"go/ast"
	"os"
	"strings"
)

// GasZeroBlacklist 按「package.函数名」匹配；命中后对该函数做 gas 保存/恢复，净消耗为 0。
// 条目格式示例：
//
//	main.init
//	main.GetGas
//	# 注释行
type GasZeroBlacklist struct {
	// key: "package.FuncName"（方法也只用函数名，与「package+函数名」约定一致）
	entries map[string]struct{}
}

// LoadGasZeroBlacklist 从文本文件加载黑名单；path 为空则返回空黑名单。
func LoadGasZeroBlacklist(path string) (*GasZeroBlacklist, error) {
	bl := &GasZeroBlacklist{entries: make(map[string]struct{})}
	if strings.TrimSpace(path) == "" {
		return bl, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开 gas-zero 黑名单失败: %w", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, err := normalizeBlacklistKey(line)
		if err != nil {
			return nil, fmt.Errorf("黑名单第 %d 行无效 %q: %w", lineNo, line, err)
		}
		bl.entries[key] = struct{}{}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return bl, nil
}

func normalizeBlacklistKey(s string) (string, error) {
	s = strings.TrimSpace(s)
	// 允许写成 package.Func 或 package/path.Func（取最后一段 package 名 + 函数名）
	if strings.Count(s, ".") < 1 {
		return "", fmt.Errorf("需要 package.FuncName 格式")
	}
	idx := strings.LastIndex(s, ".")
	pkgPart := strings.TrimSpace(s[:idx])
	fnPart := strings.TrimSpace(s[idx+1:])
	if pkgPart == "" || fnPart == "" {
		return "", fmt.Errorf("package 或函数名为空")
	}
	// import path 时取最后一段作为 AST package name（如 github.com/x/y -> y）
	if i := strings.LastIndex(pkgPart, "/"); i >= 0 {
		pkgPart = pkgPart[i+1:]
	}
	// 方法写成 package.(*T).M / package.T.M 时，package 取第一段
	if i := strings.Index(pkgPart, ".("); i >= 0 {
		pkgPart = pkgPart[:i]
	} else if i := strings.Index(pkgPart, "."); i >= 0 {
		// package.Type 形式：保留第一段为 package
		pkgPart = pkgPart[:i]
	}
	// 方法若写成 (*T).M，只保留 M
	if j := strings.LastIndex(fnPart, "."); j >= 0 {
		fnPart = fnPart[j+1:]
	}
	fnPart = strings.Trim(fnPart, "()")
	return pkgPart + "." + fnPart, nil
}

// MatchFunc 使用文件 package 名 + 函数名匹配。
func (b *GasZeroBlacklist) MatchFunc(pkgName string, fn *ast.FuncDecl) bool {
	if b == nil || len(b.entries) == 0 || fn == nil || fn.Name == nil {
		return false
	}
	key := pkgName + "." + fn.Name.Name
	_, ok := b.entries[key]
	return ok
}

// Len 返回条目数。
func (b *GasZeroBlacklist) Len() int {
	if b == nil {
		return 0
	}
	return len(b.entries)
}
