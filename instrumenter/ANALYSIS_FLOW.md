# SSA 指令分析流程说明

## 流程概述

分析工具 `AnalyzeIgnoredInstructions` 的流程与原始插桩流程 `ApplyInstrumentation` **基本一致**，主要区别是：
- **原始流程**：实际修改 AST，插入 `ConsumeGas` 调用
- **分析流程**：模拟插桩过程，只跟踪哪些指令被使用，不修改代码

## 详细流程对比

### 1. 初始化阶段

**分析流程**：
1. 构建 SSA 指令池（`BuildSSAForExample`）
2. 识别当前分析的包（排除外部导入的包）
3. 创建指令池副本用于跟踪
4. 解析 AST 文件

**原始流程**：
1. 构建 SSA 指令池（`BuildSSAForExample`）
2. 解析 AST 文件

### 2. AST 遍历阶段（核心逻辑一致）

两者都使用 `astutil.Apply` 遍历 AST，处理相同的节点类型：

#### 2.1 BlockStmt 处理
- **跳过条件**：如果父节点是 `SwitchStmt`、`TypeSwitchStmt` 或 `SelectStmt`，则跳过
- **原始流程**：调用 `CalculateGasForBlockStmt` 计算 gas，然后插入 `ConsumeGas`
- **分析流程**：调用 `GetInstructionsByPosition` 获取该范围内的指令，标记为"已使用"

#### 2.2 CommClause 处理（select 的 case 子句）
- **原始流程**：调用 `CalculateGasForStmtList` 计算 gas，然后插入 `ConsumeGas`
- **分析流程**：获取 body 范围内的指令，标记为"已使用"

#### 2.3 CaseClause 处理（switch 的 case 子句）
- **原始流程**：调用 `CalculateGasForStmtList` 计算 gas，然后插入 `ConsumeGas`
- **分析流程**：获取 body 范围内的指令，标记为"已使用"

### 3. 后处理阶段

**原始流程**：
- 清空 `main` 函数体
- 移除所有注释
- 添加导出标记

**分析流程**：
- 统计未使用的指令（被忽略的指令）
- 生成统计报告
- 输出 JSON 文件

## 一致性保证

分析流程通过以下方式确保与原始流程一致：

1. **相同的 AST 遍历逻辑**：使用相同的 `astutil.Apply` 回调函数结构
2. **相同的跳过条件**：对 `SwitchStmt`、`TypeSwitchStmt`、`SelectStmt` 的处理一致
3. **相同的指令获取方式**：都使用 `GetInstructionsByPosition` 按行号获取指令
4. **相同的范围计算**：BlockStmt 使用 `Lbrace` 到 `Rbrace`，Clause 使用第一个和最后一个语句的位置

## 每个函数的统计信息

现在每个函数包含以下统计信息：

- **TotalInstructions**：函数的总指令数（SSA 指令）
- **UsedInstructions**：在插桩过程中被使用的指令数
- **IgnoredInstructions**：被忽略的指令数（未使用的指令）
- **IgnoredPercentage**：忽略百分比 = (IgnoredInstructions / TotalInstructions) × 100%

这些统计信息可以帮助识别：
- 哪些函数的指令覆盖率较低
- 哪些语法结构导致指令被忽略
- 插桩策略的改进方向
