package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"math"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
)

// AddGasCode 在 import 之后添加 var GAS、SetGas/GetGas 包装函数
func AddGasCode(file *ast.File) error {
	gasInitCode := `var GAS = &registry.Gas{}`
	gasInitDecl, err := code2astDecl(gasInitCode)
	if err != nil {
		return fmt.Errorf("无法解析 gas 初始化: %v", err)
	}

	setGasCode := `func SetGas(amount uint64) { registry.SetGas(amount) }`
	setGasDecl, err := code2astDecl(setGasCode)
	if err != nil {
		return fmt.Errorf("无法解析 SetGas 包装: %v", err)
	}

	getGasCode := `func GetGas() uint64 { return registry.GetGas() }`
	getGasDecl, err := code2astDecl(getGasCode)
	if err != nil {
		return fmt.Errorf("无法解析 GetGas 包装: %v", err)
	}

	// 找到最后一个 import 的位置
	lastImportIndex := -1
	for i, decl := range file.Decls {
		if genDecl, ok := decl.(*ast.GenDecl); ok && genDecl.Tok == token.IMPORT {
			lastImportIndex = i
		}
	}

	// 在最后一个 import 之后插入声明
	insertIndex := lastImportIndex + 1
	if lastImportIndex < 0 {
		insertIndex = 0
	}

	newDecls := make([]ast.Decl, 0, len(file.Decls)+3)
	newDecls = append(newDecls, file.Decls[:insertIndex]...)
	newDecls = append(newDecls, gasInitDecl)
	newDecls = append(newDecls, setGasDecl)
	newDecls = append(newDecls, getGasDecl)
	newDecls = append(newDecls, file.Decls[insertIndex:]...)

	file.Decls = newDecls

	return nil
}

// AddRegisterGasToMain 在 main 函数开头插入 registry.Register("gas", GAS)
func AddRegisterGasToMain(fn *ast.FuncDecl) error {
	if fn.Body == nil {
		return nil
	}
	registerCode := `registry.Register("gas", GAS)`
	registerStmt, err := code2astStmt(registerCode)
	if err != nil {
		return fmt.Errorf("无法解析 Register 语句: %v", err)
	}
	fn.Body.List = append([]ast.Stmt{registerStmt}, fn.Body.List...)
	return nil
}

// AddSetGasToMain 在 main 函数开头插入 registry.SetGas(amount) 语句
func AddSetGasToMain(fn *ast.FuncDecl, amount uint64) error {
	if fn.Body == nil {
		return nil
	}

	// 使用 code2ast 创建 SetGas 调用语句
	setGasCode := fmt.Sprintf("registry.SetGas(%d)", amount)
	setGasStmt, err := code2astStmt(setGasCode)
	if err != nil {
		return fmt.Errorf("无法解析 SetGas 语句: %v", err)
	}

	// 在 main 函数开头插入
	fn.Body.List = append([]ast.Stmt{setGasStmt}, fn.Body.List...)
	return nil
}

// AddGasOutputToMain 在 main 函数末尾添加剩余 gas 输出
func AddGasOutputToMain(fn *ast.FuncDecl) error {
	if fn.Body == nil {
		return nil
	}

	// 使用 code2ast 创建输出语句: fmt.Println("Remaining gas:", registry.GetGas())
	outputCode := `fmt.Println("Remaining gas:", registry.GetGas())`
	outputStmt, err := code2astStmt(outputCode)
	if err != nil {
		return fmt.Errorf("无法解析 gas 输出语句: %v", err)
	}

	// 将新语句追加到函数体的末尾
	fn.Body.List = append(fn.Body.List, outputStmt)
	return nil
}

// AddConsumeGasToBlock 在 BlockStmt 开头插入 ConsumeGas(amount)
func AddConsumeGasToBlock(block *ast.BlockStmt, amount int) error {
	if block == nil {
		return nil
	}

	// 使用 code2ast 创建 ConsumeGas 调用语句
	consumeCode := fmt.Sprintf("registry.ConsumeGas(%d)", amount)
	consumeStmt, err := code2astStmt(consumeCode)
	if err != nil {
		return fmt.Errorf("无法解析 ConsumeGas 语句: %v", err)
	}

	// 在 BlockStmt 开头插入
	block.List = append([]ast.Stmt{consumeStmt}, block.List...)
	return nil
}

// AddConsumeGasToCommClause 在 CommClause (select 的 case 子句) 的 body 开头插入 ConsumeGas(amount)
func AddConsumeGasToCommClause(clause *ast.CommClause, amount int) error {
	if clause == nil {
		return nil
	}

	// 使用 code2ast 创建 ConsumeGas 调用语句
	consumeCode := fmt.Sprintf("registry.ConsumeGas(%d)", amount)
	consumeStmt, err := code2astStmt(consumeCode)
	if err != nil {
		return fmt.Errorf("无法解析 ConsumeGas 语句: %v", err)
	}

	// 在 CommClause 的 body 开头插入
	clause.Body = append([]ast.Stmt{consumeStmt}, clause.Body...)
	return nil
}

// AddConsumeGasToCaseClause 在 CaseClause (switch 的 case 子句) 的 body 开头插入 ConsumeGas(amount)
func AddConsumeGasToCaseClause(clause *ast.CaseClause, amount int) error {
	if clause == nil {
		return nil
	}

	// 使用 code2ast 创建 ConsumeGas 调用语句
	consumeCode := fmt.Sprintf("registry.ConsumeGas(%d)", amount)
	consumeStmt, err := code2astStmt(consumeCode)
	if err != nil {
		return fmt.Errorf("无法解析 ConsumeGas 语句: %v", err)
	}

	// 在 CaseClause 的 body 开头插入
	clause.Body = append([]ast.Stmt{consumeStmt}, clause.Body...)
	return nil
}

// ApplyInstrumentation 使用一次 astutil.Apply 遍历统一处理所有插桩操作
// 计算 gas 时用 AST 节点的 Position.Filename 与指令池匹配（与 SSA 的 Position 同源，避免路径不一致）
// consumeGasOnly 为 true 时仅插桩 registry.ConsumeGas，不添加 GAS/Register，不修改 main 函数体
// 返回 (是否插入了 registry.ConsumeGas 等调用, error)
func ApplyInstrumentation(node *ast.File, pool *InstructionPool, fset *token.FileSet, consumeGasOnly bool) (bool, error) {
	inserted := false

	astutil.Apply(node, nil, func(c *astutil.Cursor) bool {
		curNode := c.Node()

		// 在文件级别添加 Gas 代码（在 import 之后）；consumeGasOnly 时跳过
		if !consumeGasOnly {
			if file, ok := curNode.(*ast.File); ok {
				if err := AddGasCode(file); err != nil {
					fmt.Printf("警告: 添加 Gas 代码失败: %v\n", err)
				} else {
					inserted = true
				}
			}
		}

		// 在每个 BlockStmt 开头插入 ConsumeGas
		if block, ok := curNode.(*ast.BlockStmt); ok {

			// 检查父节点是否是 select 的 case 子句或 switch 的 case 子句
			parent := c.Parent()
			if parent != nil {
				if _, ok := parent.(*ast.SwitchStmt); ok {
					return true // 如果在 switch 中，跳过插桩
				}
				if _, ok := parent.(*ast.TypeSwitchStmt); ok {
					return true // 如果在 type switch 中，跳过插桩
				}
				if _, ok := parent.(*ast.SelectStmt); ok {
					return true // 如果在 select 中，跳过插桩
				}
			}

			// 用该 block 的 Position.Filename 与指令池匹配（与 SSA 同源，避免传入路径与 SSA 路径不一致）
			targetFile := fset.Position(block.Lbrace).Filename
			gasAmount := CalculateGasForBlockStmt(block, pool, fset, targetFile)

			// 插入 ConsumeGas 调用
			if err := AddConsumeGasToBlock(block, gasAmount); err != nil {
				fmt.Printf("警告: 无法在 BlockStmt 中插入 ConsumeGas: %v\n", err)
			} else {
				inserted = true
			}
		}

		// 在 select 的 case 子句 (CommClause) 的 body 开头插入 ConsumeGas
		if commClause, ok := curNode.(*ast.CommClause); ok {

			// 计算 body 中所有语句的 gas 总量
			if len(commClause.Body) > 0 {
				// 获取第一个和最后一个语句的位置
				firstStmt := commClause.Body[0]
				lastStmt := commClause.Body[len(commClause.Body)-1]

				startPos := fset.Position(firstStmt.Pos())
				endPos := fset.Position(lastStmt.End())

				gasAmount := CalculateGasForStmtList(commClause.Body, startPos, endPos, pool, fset, startPos.Filename)

				// 插入 ConsumeGas 调用
				if err := AddConsumeGasToCommClause(commClause, gasAmount); err != nil {
					fmt.Printf("警告: 无法在 CommClause 中插入 ConsumeGas: %v\n", err)
				} else {
					inserted = true
				}
			}
		}

		// 在 switch 的 case 子句 (CaseClause) 的 body 开头插入 ConsumeGas
		if caseClause, ok := curNode.(*ast.CaseClause); ok {

			// 计算 body 中所有语句的 gas 总量
			if len(caseClause.Body) > 0 {
				// 获取第一个和最后一个语句的位置
				firstStmt := caseClause.Body[0]
				lastStmt := caseClause.Body[len(caseClause.Body)-1]

				startPos := fset.Position(firstStmt.Pos())
				endPos := fset.Position(lastStmt.End())

				gasAmount := CalculateGasForStmtList(caseClause.Body, startPos, endPos, pool, fset, startPos.Filename)

				// 插入 ConsumeGas 调用
				if err := AddConsumeGasToCaseClause(caseClause, gasAmount); err != nil {
					fmt.Printf("警告: 无法在 CaseClause 中插入 ConsumeGas: %v\n", err)
				} else {
					inserted = true
				}
			}
		}

		// // 在 main 函数开头插入 SetGas(k)，末尾添加输出语句
		// if fn, ok := curNode.(*ast.FuncDecl); ok && fn.Name.Name == "main" {
		// 	if err := AddSetGasToMain(fn, k); err != nil {
		// 		fmt.Printf("警告: 无法在 main 函数开头添加 SetGas: %v\n", err)
		// 	}
		// 	if err := AddGasOutputToMain(fn); err != nil {
		// 		fmt.Printf("警告: 无法在 main 函数中添加 gas 输出: %v\n", err)
		// 	}
		// }

		return true
	})

// 非 consumeGasOnly 时：在 main 函数最前插入 gas 初始化语句：
// 1) registry.Register("gas", GAS)
// 2) registry.SetGas(math.MaxInt64)
	if !consumeGasOnly {
		for _, decl := range node.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "main" {
				if err := AddSetGasToMain(fn, uint64(math.MaxInt64)); err != nil {
					fmt.Printf("警告: 无法在 main 函数开头添加 SetGas: %v\n", err)
				} else {
					inserted = true
				}
				if err := AddRegisterGasToMain(fn); err != nil {
					fmt.Printf("警告: 无法在 main 函数开头添加 registry.Register: %v\n", err)
				} else {
					inserted = true
				}
				break
			}
		}
	}

	return inserted, nil
}

// AddCommentToFunction 向匹配特定字符串的函数声明前添加注释
// file: AST 文件节点
// matchString: 用于匹配函数声明的字符串（函数声明开头包含此字符串即匹配）
// commentText: 要添加的注释文本（例如 "//go:wasmexport GetGas"）
func AddCommentToFunction(file *ast.File, matchString string, commentText string) error {
	fset := token.NewFileSet()
	matchString = strings.TrimSpace(matchString)

	// 遍历文件中的所有声明
	for _, decl := range file.Decls {
		// 检查是否为函数声明
		fnDecl, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		// 优先按函数名精确匹配（避免依赖 format 后的字符串形态）
		matched := false
		if strings.HasPrefix(matchString, "func ") && fnDecl.Recv == nil {
			rest := strings.TrimSpace(strings.TrimPrefix(matchString, "func "))
			name := rest
			if idx := strings.IndexAny(rest, "( \t"); idx >= 0 {
				name = rest[:idx]
			}
			if name != "" && fnDecl.Name != nil && fnDecl.Name.Name == name {
				matched = true
			}
		}
		// 回退到原有前缀匹配（兼容方法等场景）
		if !matched {
			var buf bytes.Buffer
			err := format.Node(&buf, fset, fnDecl)
			if err != nil {
				continue
			}
			funcStr := strings.TrimSpace(buf.String())
			matched = strings.HasPrefix(funcStr, matchString)
		}
		if matched {
			// 确保注释文本格式正确（以 // 开头，以 \n 结尾）
			commentLine := strings.TrimSpace(commentText)
			if !strings.HasPrefix(commentLine, "//") {
				commentLine = "//" + commentLine
			}
			// ast.Comment 的 Text 不需要携带换行符

			// 创建注释
			comment := &ast.Comment{
				Text: commentLine,
			}

			// 将注释添加到函数声明的 Doc 字段
			// 如果已有注释，则在最前面插入新注释（保持注释顺序）
			if fnDecl.Doc != nil {
				// 已存在相同注释则跳过，避免重复添加
				for _, c := range fnDecl.Doc.List {
					if strings.TrimSpace(c.Text) == commentLine {
						return nil
					}
				}
				// 将新注释插入到现有注释列表的最前面
				fnDecl.Doc.List = append([]*ast.Comment{comment}, fnDecl.Doc.List...)
			} else {
				// 如果没有现有注释，创建新的注释组
				fnDecl.Doc = &ast.CommentGroup{
					List: []*ast.Comment{comment},
				}
			}
		}
	}

	return nil
}

// RemoveAllComments 移除文件中的所有注释
func RemoveAllComments(node *ast.File) {
	// 移除文件级别的注释
	node.Comments = nil

	// 使用 ast.Inspect 遍历所有节点，移除所有注释
	ast.Inspect(node, func(n ast.Node) bool {
		if n == nil {
			return false
		}

		// 移除函数声明的注释
		if fn, ok := n.(*ast.FuncDecl); ok {
			fn.Doc = nil
		}

		// 移除通用声明的注释（如 type, var, const）
		if genDecl, ok := n.(*ast.GenDecl); ok {
			genDecl.Doc = nil
		}

		// 移除类型声明的注释
		if typeSpec, ok := n.(*ast.TypeSpec); ok {
			typeSpec.Doc = nil
		}

		// 移除字段的注释
		if field, ok := n.(*ast.Field); ok {
			field.Doc = nil
			field.Comment = nil
		}

		// 移除值的注释
		if valueSpec, ok := n.(*ast.ValueSpec); ok {
			valueSpec.Doc = nil
			valueSpec.Comment = nil
		}

		return true
	})
}
