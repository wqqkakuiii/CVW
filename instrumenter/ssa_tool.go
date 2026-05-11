package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// InstructionGasTable 指令类型到 gas 值的映射表。key 为 SSA 指令类型短名（如 "Call"、"Alloc"），
// 未在表中的指令使用 DefaultInstructionGas 作为默认 gas。
type InstructionGasTable map[string]int

// DefaultInstructionGas 表中未列出的指令的默认 gas 值
const DefaultInstructionGas = 1

// DefaultInstructionGasTable 预定义的指令-gas 表，覆盖全部 SSA Instruction 类型（含仅 Instruction 的 DebugRef/Defer/Go/If/Jump/MapUpdate/Panic/Return/RunDefers/Send/Store）。
// 可按需替换或作为 pool.GasTable 使用。key 对应 *ssa.Xxx 的短名。
var DefaultInstructionGasTable = InstructionGasTable{
	// Value+Instruction
	"Alloc":               2,
	"BinOp":               2,
	"Call":                2,
	"ChangeInterface":     2,
	"ChangeType":          2,
	"Convert":             2,
	"Extract":             2,
	"Field":               2,
	"FieldAddr":           2,
	"Index":               2,
	"IndexAddr":           2,
	"Lookup":              2,
	"MakeChan":            2,
	"MakeClosure":         2,
	"MakeInterface":       2,
	"MakeMap":             2,
	"MakeSlice":           2,
	"MultiConvert":        2,
	"Next":                2,
	"Phi":                 2,
	"Range":               2,
	"Select":              2,
	"Slice":               2,
	"SliceToArrayPointer": 2,
	"TypeAssert":          2,
	"UnOp":                2,
	// Instruction only (无 Value)
	"DebugRef":  1,
	"Defer":     1,
	"Go":        1,
	"If":        1,
	"Jump":      1,
	"MapUpdate": 1,
	"Panic":     1,
	"Return":    1,
	"RunDefers": 1,
	"Send":      1,
	"Store":     1,
}

// InstructionInfo 存储每条 SSA 指令的完整信息
type InstructionInfo struct {
	// 指令本身
	Instruction ssa.Instruction

	// 指令所在的基本块
	Block *ssa.BasicBlock

	// 指令在基本块中的索引
	Index int

	// 指令所在的函数
	Function *ssa.Function

	// 指令的源代码位置
	Position token.Position

	// 指令的字符串表示
	StringRepr string
}

// InstructionPool 指令池，以位置为索引存储指令，支持 O(1) 按位置查询
type InstructionPool struct {
	// instructionsByPosition 以 "normalizedPath:line" 为 key，便于 O(1) 按位置取出
	instructionsByPosition map[string][]*InstructionInfo
	Program                *ssa.Program
	FileSet                *token.FileSet
	// GasTable 指令类型到 gas 的映射；为 nil 时每条指令按 DefaultInstructionGas 计
	GasTable InstructionGasTable
}

// positionKey 生成 map 索引 key
func positionKey(normalizedFilename string, line int) string {
	return fmt.Sprintf("%s:%d", normalizedFilename, line)
}

// BuildSSAForExample 为指定路径构建 SSA 并填充指令池
// 当 srcRoot、importPath 均非空时：用 GOPATH 模式加载（GOPATH=Dir(srcRoot)，GO111MODULE=off），Load(importPath）
func BuildSSAForExample(path string, fillEmptyPositions bool, srcRoot, importPath string) (*InstructionPool, error) {
	cfg := &packages.Config{Mode: packages.LoadSyntax}
	loadQuery := path
	env := os.Environ()
	if srcRoot != "" && importPath != "" {
		gopath := filepath.Dir(filepath.Clean(srcRoot))
		env = setEnv(env, "GOPATH", gopath)
		env = setEnv(env, "GO111MODULE", "off")
		loadQuery = importPath
	}
	// github.com/TKOTKCh/contract-sdk-go-wasm 使用 //go:wasmimport 无函数体声明；
	// 仅在 wasip1/wasm 目标下类型检查合法（与合约 build.sh 中 GOOS=wasip1 GOARCH=wasm 一致）。
	env = setEnv(env, "GOOS", "wasip1")
	env = setEnv(env, "GOARCH", "wasm")
	env = setEnv(env, "CGO_ENABLED", "0")
	cfg.Env = env
	initial, err := packages.Load(cfg, loadQuery)
	if err != nil {
		return nil, fmt.Errorf("加载包失败: %v", err)
	}

	// 2) 检查是否有错误，若有则收集具体错误信息后返回
	if n := packages.PrintErrors(initial); n > 0 {
		var msgs []string
		for _, pkg := range initial {
			for _, e := range pkg.Errors {
				msgs = append(msgs, e.Msg)
			}
		}
		detail := strings.Join(msgs, "; ")
		if detail == "" {
			detail = "详见 stderr 输出"
		}
		return nil, fmt.Errorf("类型检查错误: %s", detail)
	}

	// 3) 创建 SSA 程序和包，使用 LogSource 模式显示源代码位置
	prog, pkgs := ssautil.Packages(initial, ssa.LogSource)

	// 4) 创建指令池（以位置为索引的 map，便于 O(1)  lookup）
	pool := &InstructionPool{
		instructionsByPosition: make(map[string][]*InstructionInfo),
		Program:                prog,
		FileSet:                prog.Fset,
	}

	// 5) 为每个包构建 SSA 并收集指令
	for _, pkg := range pkgs {
		if pkg == nil {
			continue
		}
		pkg.Build()

		// 6) 遍历每个函数收集所有指令
		for _, mem := range pkg.Members {
			if fn, ok := mem.(*ssa.Function); ok {
				collectInstructions(fn, pool, fillEmptyPositions)
			}
		}
	}

	return pool, nil
}

func setEnv(env []string, name, value string) []string {
	p := name + "="
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if !strings.HasPrefix(e, p) {
			out = append(out, e)
		}
	}
	out = append(out, p+value)
	return out
}

// collectInstructions 收集函数中的所有指令到指令池
// fillEmptyPositions: 如果为 true，则对位置为空的指令进行补全（使用前一条指令的位置，或同块内下一条带位置的指令，或 rangeindex/rangeiter 对应 body 块位置）
func collectInstructions(fn *ssa.Function, pool *InstructionPool, fillEmptyPositions bool) {
	// 预计算：每个 range*.loop 块对应的下一个 range*.body 块（按 fn.Blocks 顺序）
	// rangeindex: for i, v := range slice；rangeiter: for k, v := range map 等
	nextBodyBlockByLoopBlock := make(map[*ssa.BasicBlock]*ssa.BasicBlock)
	if fillEmptyPositions {
		for bi, b := range fn.Blocks {
			var bodyComment string
			switch b.Comment {
			case "rangeindex.loop":
				bodyComment = "rangeindex.body"
			case "rangeiter.loop":
				bodyComment = "rangeiter.body"
			default:
				continue
			}
			for j := bi + 1; j < len(fn.Blocks); j++ {
				if fn.Blocks[j].Comment == bodyComment {
					nextBodyBlockByLoopBlock[b] = fn.Blocks[j]
					break
				}
			}
		}
	}

	for _, block := range fn.Blocks {
		// 在基本块级别维护最后已知的有效位置
		var lastValidPos token.Pos
		var lastValidPosition token.Position

		for i, instr := range block.Instrs {
			// 获取指令位置
			pos := instr.Pos()
			var position token.Position

			if pos.IsValid() {
				// 当前指令有位置信息，使用它并更新最后已知位置
				position = pool.FileSet.Position(pos)
				lastValidPos = pos
				lastValidPosition = position
			} else {
				// 当前指令没有位置信息
				if fillEmptyPositions {
					// 特殊：若基本块含标签 rangeindex.loop 或 rangeiter.loop 且为块内第一条指令，用对应 range*.body 块的首条带位置指令补全
					if i == 0 && (block.Comment == "rangeindex.loop" || block.Comment == "rangeiter.loop") {
						if bodyBlock := nextBodyBlockByLoopBlock[block]; bodyBlock != nil {
							for _, in := range bodyBlock.Instrs {
								if p := in.Pos(); p.IsValid() {
									position = pool.FileSet.Position(p)
									lastValidPos = p
									lastValidPosition = position
									break
								}
							}
						}
					}
					// 若仍未补全：用本块内已出现的“最后已知有效位置”（即前一条或更早的带位置指令）
					if !position.IsValid() && lastValidPos.IsValid() {
						position = lastValidPosition
					}
					// 若仍未补全：用同一基本块中下一个带位置信息的指令的位置
					if !position.IsValid() {
						for j := i + 1; j < len(block.Instrs); j++ {
							nextPos := block.Instrs[j].Pos()
							if nextPos.IsValid() {
								position = pool.FileSet.Position(nextPos)
								lastValidPos = nextPos
								lastValidPosition = position
								break
							}
						}
					}
				}
			}

			// 创建指令信息
			info := &InstructionInfo{
				Instruction: instr,
				Block:       block,
				Index:       i,
				Function:    fn,
				Position:    position,
				StringRepr:  fmt.Sprintf("%s", instr),
			}

			// 添加到指令池（按位置索引，便于 O(1) 查询）
			if position.IsValid() {
				key := positionKey(normalizePath(position.Filename), position.Line)
				pool.instructionsByPosition[key] = append(pool.instructionsByPosition[key], info)
			}
		}
	}
}

// InstructionCount 返回指令池中的总指令数
func (pool *InstructionPool) InstructionCount() int {
	if pool == nil || pool.instructionsByPosition == nil {
		return 0
	}
	n := 0
	for _, list := range pool.instructionsByPosition {
		n += len(list)
	}
	return n
}

// AllInstructions 返回所有指令（用于遍历），顺序不保证
func (pool *InstructionPool) AllInstructions() []*InstructionInfo {
	if pool == nil || pool.instructionsByPosition == nil {
		return nil
	}
	var out []*InstructionInfo
	for _, list := range pool.instructionsByPosition {
		out = append(out, list...)
	}
	return out
}

// Clone 深拷贝指令池（复制 map 及每个 key 下的切片），用于分析时独立修改
func (pool *InstructionPool) Clone() *InstructionPool {
	if pool == nil {
		return nil
	}
	cpy := &InstructionPool{
		instructionsByPosition: make(map[string][]*InstructionInfo),
		Program:                pool.Program,
		FileSet:                pool.FileSet,
		GasTable:               pool.GasTable,
	}
	for k, list := range pool.instructionsByPosition {
		cpy.instructionsByPosition[k] = append([]*InstructionInfo{}, list...)
	}
	return cpy
}

// PrintPoolInfo 打印指令池的统计信息
func (pool *InstructionPool) PrintPoolInfo() {
	fmt.Printf("=== 指令池信息 ===\n")
	fmt.Printf("总指令数: %d\n", pool.InstructionCount())
	fmt.Printf("程序: %v\n", pool.Program)
	fmt.Printf("\n")
}

// PrintAllInstructions 打印所有指令的详细信息
func (pool *InstructionPool) PrintAllInstructions() {
	fmt.Printf("=== 所有指令详情 ===\n\n")
	all := pool.AllInstructions()
	for i, info := range all {
		fmt.Printf("指令 [%d]:\n", i)
		fmt.Printf("  函数: %s\n", info.Function.Name())
		fmt.Printf("  基本块: %s\n", info.Block.Comment)
		fmt.Printf("  索引: %d\n", info.Index)
		if info.Position.IsValid() {
			fmt.Printf("  位置: %s:%d:%d\n", info.Position.Filename, info.Position.Line, info.Position.Column)
		} else {
			fmt.Printf("  位置: <无位置信息>\n")
		}
		fmt.Printf("  指令: %s\n", info.StringRepr)
		fmt.Printf("\n")
	}
}

// normalizePath 规范化文件路径，用于比较（统一为绝对路径 + 正斜杠，避免 Windows 下 \ 与 / 不一致导致匹配失败）
func normalizePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(abs)
}

// GetInstructionsByPosition 获取指定位置的指令，O(1) 字典查找
// removeFromPool: 如果为 true，则从指令池中移除匹配的指令
func (pool *InstructionPool) GetInstructionsByPosition(filename string, line int, removeFromPool bool) []*InstructionInfo {
	if pool == nil || pool.instructionsByPosition == nil {
		return nil
	}
	key := positionKey(normalizePath(filename), line)
	list := pool.instructionsByPosition[key]
	if list == nil {
		return nil
	}
	if removeFromPool {
		delete(pool.instructionsByPosition, key)
		return list
	}
	// 不移除时返回副本，避免调用方修改影响池
	result := make([]*InstructionInfo, len(list))
	copy(result, list)
	return result
}

// instructionTypeKey 返回 SSA 指令的类型短名，用于查 gas 表（如 "*ssa.Call" -> "Call"）
func instructionTypeKey(instr ssa.Instruction) string {
	s := fmt.Sprintf("%T", instr)
	if strings.HasPrefix(s, "*ssa.") {
		return s[5:]
	}
	return s
}

// gasForInstruction 根据 GasTable 返回单条指令的 gas；表为空或未命中时返回 DefaultInstructionGas
func gasForInstruction(info *InstructionInfo, table InstructionGasTable) int {
	if table == nil {
		return DefaultInstructionGas
	}
	if g, ok := table[instructionTypeKey(info.Instruction)]; ok {
		return g
	}
	return DefaultInstructionGas
}

// CalculateGasForBlockStmt 计算 BlockStmt 节点范围内所有指令的 gas 总和
// 只统计属于 targetFilename 的 SSA 指令（SSA 按包生成，池中含同包多文件）
// 从 InstructionPool 中获取该范围内的所有指令（会从池中移除），每条指令的 gas 由 pool.GasTable 决定
func CalculateGasForBlockStmt(block *ast.BlockStmt, pool *InstructionPool, fset *token.FileSet, targetFilename string) int {
	if block == nil || pool == nil || fset == nil || targetFilename == "" {
		return 0
	}

	// 获取 BlockStmt 的起始位置
	if !block.Lbrace.IsValid() {
		return 0
	}

	startPos := fset.Position(block.Lbrace)
	if !startPos.IsValid() {
		return 0
	}

	// 获取 BlockStmt 的结束位置（用于确定范围）
	var endLine int
	if block.Rbrace.IsValid() {
		endPos := fset.Position(block.Rbrace)
		if endPos.IsValid() {
			endLine = endPos.Line
		} else {
			endLine = startPos.Line
		}
	} else {
		endLine = startPos.Line
	}

	// 收集范围内、且属于 targetFilename 的指令
	allInstructions := make([]*InstructionInfo, 0)
	for line := startPos.Line; line <= endLine; line++ {
		instructions := pool.GetInstructionsByPosition(targetFilename, line, true)
		allInstructions = append(allInstructions, instructions...)
	}

	// 按 GasTable 累加每条指令的 gas
	gasSum := 0
	for _, info := range allInstructions {
		gasSum += gasForInstruction(info, pool.GasTable)
	}

	return gasSum
}

// CalculateGasForStmtList 计算语句列表中所有指令的 gas 总和
// 只统计属于 targetFilename 的 SSA 指令（SSA 按包生成，池中含同包多文件）
func CalculateGasForStmtList(stmts []ast.Stmt, startPos token.Position, endPos token.Position, pool *InstructionPool, fset *token.FileSet, targetFilename string) int {
	if len(stmts) == 0 || pool == nil || fset == nil || targetFilename == "" {
		return 0
	}

	if !startPos.IsValid() || !endPos.IsValid() {
		return 0
	}

	allInstructions := make([]*InstructionInfo, 0)
	for line := startPos.Line; line <= endPos.Line; line++ {
		instructions := pool.GetInstructionsByPosition(targetFilename, line, true)
		allInstructions = append(allInstructions, instructions...)
	}

	// 按 GasTable 累加每条指令的 gas
	gasSum := 0
	for _, info := range allInstructions {
		gasSum += gasForInstruction(info, pool.GasTable)
	}

	return gasSum
}

// AddExportCommentsToAllFunctions 使用 AddCommentToFunction 向所有函数（main除外）添加导出标记
func AddExportCommentsToAllFunctions(node *ast.File, fset *token.FileSet) error {
	for _, decl := range node.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			// 跳过 main 函数，不导出
			if fn.Name.Name == "main" {
				continue
			}
			// 构建匹配字符串：对于普通函数使用 "func FunctionName"，对于方法需要包含接收者
			var matchString string
			if fn.Recv == nil {
				// 普通函数
				matchString = fmt.Sprintf("func %s", fn.Name.Name)
			} else {
				// 方法：需要格式化接收者
				var recvBuf bytes.Buffer
				err := format.Node(&recvBuf, fset, fn.Recv.List[0].Type)
				if err == nil {
					matchString = fmt.Sprintf("func (%s) %s", recvBuf.String(), fn.Name.Name)
				} else {
					// 如果格式化失败，使用函数名作为后备
					matchString = fmt.Sprintf("func %s", fn.Name.Name)
				}
			}
			// 为每个函数添加导出标记
			exportComment := fmt.Sprintf("//go:wasmexport %s", fn.Name.Name)
			if err := AddCommentToFunction(node, matchString, exportComment); err != nil {
				return fmt.Errorf("为函数 %s 添加导出标记失败: %v", fn.Name.Name, err)
			}
		}
	}
	return nil
}
