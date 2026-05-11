package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
)

// code2ast 将代码字符串解析为 AST 节点
// 输入：codeStr - 代码字符串（可以是语句、表达式、声明等）
// 输出：ast.Node - 对应的 AST 节点，如果解析失败返回 nil 和错误
func code2ast(codeStr string) (ast.Node, error) {
	// 创建一个临时的文件集
	fset := token.NewFileSet()

	// 尝试作为表达式解析
	expr, err := parser.ParseExpr(codeStr)
	if err == nil {
		return expr, nil
	}

	// 如果表达式解析失败，尝试作为语句解析
	// 需要包装在函数体中
	stmtCode := fmt.Sprintf("package temp\nfunc _() {\n\t%s\n}", codeStr)
	node, err := parser.ParseFile(fset, "", stmtCode, parser.ParseComments)
	if err == nil && node != nil && len(node.Decls) > 0 {
		if fn, ok := node.Decls[0].(*ast.FuncDecl); ok && fn.Body != nil && len(fn.Body.List) > 0 {
			return fn.Body.List[0], nil
		}
	}

	// 如果语句解析失败，尝试作为声明解析
	declCode := fmt.Sprintf("package temp\n%s", codeStr)
	node, err = parser.ParseFile(fset, "", declCode, parser.ParseComments)
	if err == nil && node != nil && len(node.Decls) > 0 {
		return node.Decls[0], nil
	}

	// 如果都失败，返回错误
	return nil, fmt.Errorf("无法解析代码: %v", err)
}

// code2astExpr 将代码字符串解析为表达式 AST 节点
func code2astExpr(codeStr string) (ast.Expr, error) {
	expr, err := parser.ParseExpr(codeStr)
	if err != nil {
		return nil, fmt.Errorf("无法解析为表达式: %v", err)
	}
	return expr, nil
}

// code2astStmt 将代码字符串解析为语句 AST 节点
func code2astStmt(codeStr string) (ast.Stmt, error) {
	fset := token.NewFileSet()
	stmtCode := fmt.Sprintf("package temp\nfunc _() {\n\t%s\n}", codeStr)
	node, err := parser.ParseFile(fset, "", stmtCode, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("无法解析为语句: %v", err)
	}

	if node == nil || len(node.Decls) == 0 {
		return nil, fmt.Errorf("解析结果为空")
	}

	fn, ok := node.Decls[0].(*ast.FuncDecl)
	if !ok || fn.Body == nil || len(fn.Body.List) == 0 {
		return nil, fmt.Errorf("无法提取语句")
	}

	return fn.Body.List[0], nil
}

// code2astDecl 将代码字符串解析为声明 AST 节点
func code2astDecl(codeStr string) (ast.Decl, error) {
	fset := token.NewFileSet()
	declCode := fmt.Sprintf("package temp\n%s", codeStr)
	node, err := parser.ParseFile(fset, "", declCode, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("无法解析为声明: %v", err)
	}

	if node == nil || len(node.Decls) == 0 {
		return nil, fmt.Errorf("解析结果为空")
	}

	return node.Decls[0], nil
}

// Gas 结构体用于管理 Gas 量
type Gas struct {
	Remain int // 剩余的 Gas 量
}

// ConsumeGas 消耗定量 Gas，若 gas 量不足则通过 os.Exit 退出程序
func (g *Gas) ConsumeGas(amount int) {
	if g.Remain < amount {
		fmt.Fprintf(os.Stderr, "Gas 不足: 需要 %d，但只有 %d\n", amount, g.Remain)
		os.Exit(1)
	}
	g.Remain -= amount
}

// SetGas 设定 Gas 量
func (g *Gas) SetGas(amount int) {
	g.Remain = amount
}

// GetGas 读取 gas 量
func (g *Gas) GetGas() int {
	return g.Remain
}
