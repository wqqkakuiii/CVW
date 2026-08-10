package main

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	//  _ "net/http/pprof"
	// "net/http"
)

type invokeCase struct {
	contract string
	method   string
}

type gasStatsKey struct {
	contract string
	method   string
}

type deallocateSiteStatsKey struct {
	contract string
	method   string
	site     string
}

type vmPoolGrowStatsKey struct {
	contract string
	method   string
}

type vmPoolGrowCaseStats struct {
	sizes []int // 按时间顺序记录每次 grow 的 size
}

func extractTotalExecutionTimeLine(output []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "totalExecutionTime ") {
			return line
		}
	}
	return ""
}

// invokeTPSStats runtime_test.go 中 TPS(callMethod only) 相关统计。
type invokeTPSStats struct {
	successCnt          int
	totalCallMethodTime float64
	tps                 float64
	ok                  bool
}

// extractInvokeTPSStats 解析 TestInvoke 日志中的 successCnt / totalCallMethodTime / TPS(callMethod only)。
func extractInvokeTPSStats(output []byte) invokeTPSStats {
	const marker = "TPS(callMethod only)="
	scanner := bufio.NewScanner(bytes.NewReader(output))
	var last invokeTPSStats
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, marker) {
			continue
		}
		if st, ok := parseInvokeTPSLine(line); ok {
			last = st
		}
	}
	return last
}

func parseInvokeTPSLine(line string) (invokeTPSStats, bool) {
	const (
		successMarker = "successCnt="
		timeMarker    = "totalCallMethodTime="
		tpsMarker     = "TPS(callMethod only)="
	)
	var st invokeTPSStats

	if idx := strings.Index(line, successMarker); idx >= 0 {
		tail := line[idx+len(successMarker):]
		_, _ = fmt.Sscanf(tail, "%d", &st.successCnt)
	}
	if idx := strings.Index(line, timeMarker); idx >= 0 {
		tail := strings.TrimSpace(line[idx+len(timeMarker):])
		if sp := strings.IndexAny(tail, " \t"); sp >= 0 {
			tail = tail[:sp]
		}
		_, _ = fmt.Sscanf(tail, "%f", &st.totalCallMethodTime)
	}
	idx := strings.Index(line, tpsMarker)
	if idx < 0 {
		return st, false
	}
	tail := strings.TrimSpace(line[idx+len(tpsMarker):])
	n, err := fmt.Sscanf(tail, "%f", &st.tps)
	if err != nil || n != 1 {
		return st, false
	}
	st.ok = true
	return st, true
}

func formatTPSStatsField(st invokeTPSStats) string {
	if !st.ok {
		return ""
	}
	return fmt.Sprintf("%.15f", st.tps)
}

func appendInvokeReportRow(
	reportFile *os.File,
	contract, method, totalLine, gasLine string,
	tpsStats invokeTPSStats,
) {
	totalExec := ""
	if totalLine != "" {
		totalExec = strings.TrimPrefix(strings.TrimSpace(totalLine), "totalExecutionTime ")
	}
	if totalExec == "" && !tpsStats.ok {
		return
	}

	gmax, gmin, gminus, gratio, gok := parseGasSummary(gasLine)
	successCntStr := ""
	if tpsStats.ok {
		successCntStr = fmt.Sprintf("%d", tpsStats.successCnt)
		fmt.Printf("[tps] %s.%s successCnt=%d totalCallMethodTime=%.6f TPS(callMethod only)=%.6f\n",
			contract, method, tpsStats.successCnt, tpsStats.totalCallMethodTime, tpsStats.tps)
	}

	tpsStr := formatTPSStatsField(tpsStats)
	if gok {
		_, _ = fmt.Fprintf(reportFile, "%s,%s,%s,%s,%s,%d,%d,%d,%.15f\n",
			contract, method, totalExec, successCntStr, tpsStr,
			gmax, gmin, gminus, gratio)
		return
	}
	if gasLine != "" {
		fmt.Fprintf(os.Stderr, "[gas] %s.%s could not parse summary from log line: %s\n", contract, method, gasLine)
	}
	_, _ = fmt.Fprintf(reportFile, "%s,%s,%s,%s,%s,,,,\n",
		contract, method, totalExec, successCntStr, tpsStr)
}

// extractGasSummaryLine 解析 TestInvoke 日志里 runtime_test.go 打印的 gas 统计行：
// maxkey = %d, minkey = %d, minus = %d, minus/minkey = %.15f
func extractGasSummaryLine(output []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	var last string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "maxkey =") && strings.Contains(line, "minkey =") {
			last = strings.TrimSpace(line)
		}
	}
	return last
}

// parseGasSummary 从一行日志中解析出 max/min/spread/ratio；解析失败返回 ok=false。
func parseGasSummary(line string) (gasMax, gasMin, minus uint64, ratio float64, ok bool) {
	idx := strings.Index(line, "maxkey =")
	if idx < 0 {
		return 0, 0, 0, 0, false
	}
	tail := line[idx:]
	n, err := fmt.Sscanf(tail, "maxkey = %d, minkey = %d, minus = %d, minus/minkey = %f",
		&gasMax, &gasMin, &minus, &ratio)
	if err != nil || n != 4 {
		return 0, 0, 0, 0, false
	}
	return gasMax, gasMin, minus, ratio, true
}

// extractPerRunResultLines 提取每次循环执行的结果日志行（testid + contractResult）。
func extractPerRunResultLines(output []byte) []string {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	var lines []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.Contains(line, "testid =") && strings.Contains(line, "contractResult =") {
			lines = append(lines, line)
		}
	}
	return lines
}

// extractInstanceCompareLines 提取 runtime.go 中 instance_compare 日志行。
func extractInstanceCompareLines(output []byte) []string {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	var lines []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.Contains(line, "instance_compare") && strings.Contains(line, "same_all_fields:") {
			lines = append(lines, line)
		}
	}
	return lines
}

// extractVmPoolGrowSizes 提取 vm_pool.go grow() 中 "vm pool grow size = %d" 的每次增长大小。
func extractVmPoolGrowSizes(output []byte) []int {
	const marker = "vm pool grow size = "
	scanner := bufio.NewScanner(bytes.NewReader(output))
	var sizes []int
	for scanner.Scan() {
		line := scanner.Text()
		idx := strings.Index(line, marker)
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(line[idx+len(marker):])
		var size int
		n, err := fmt.Sscanf(rest, "%d", &size)
		if err != nil || n != 1 {
			continue
		}
		sizes = append(sizes, size)
	}
	return sizes
}

// extractInstanceNotReusedLines 提取 vm_pool.go 中 "instance not reused" 的日志行。
func extractInstanceNotReusedLines(output []byte) []string {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	var lines []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.Contains(line, "instance not reused, instance id:") {
			lines = append(lines, line)
		}
	}
	return lines
}

// extractRegistryConsumeGasBlocksByRun 按「第几次运行」切分 registry.ConsumeGas 的 println 块。
// runtime_test 在每次 InvokeTime 结束后打印含 testid 与 contractResult 的一行；该行之前
// 累积的 begin...end 块属于该 testid 对应的那次运行（从 0 起）。
// 若输出末尾仍有未归属的块，归入 run_index -1。
func extractRegistryConsumeGasBlocksByRun(output []byte) map[int][]string {
	byRun := make(map[int][]string)
	var pending []string

	scanner := bufio.NewScanner(bytes.NewReader(output))
	const maxScan = 64 * 1024 * 1024
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, maxScan)

	var cur []string
	inBlock := false
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if inBlock {
			cur = append(cur, line)
			if trimmed == "end" {
				pending = append(pending, strings.Join(cur, "\n"))
				cur = nil
				inBlock = false
			}
			continue
		}

		if trimmed == "begin" {
			inBlock = true
			cur = []string{line}
			continue
		}

		if strings.Contains(line, "testid =") && strings.Contains(line, "contractResult =") {
			if id, ok := parseTestID(line); ok {
				if len(pending) > 0 {
					byRun[id] = append(byRun[id], pending...)
					pending = nil
				}
			}
		}
	}
	if len(pending) > 0 {
		byRun[-1] = append(byRun[-1], pending...)
	}
	return byRun
}

func sanitizePathSegment(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '/', '\\', ':', 0:
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" || out == "." {
		return "_"
	}
	return out
}

// writeRegistryConsumeGasLogsByRun 在 baseDir 下创建子目录 <contract>__<method>，
// 每个 testid 单独一个文件 testid_<N>.txt；无法归属的块写入 testid_unbound.txt。
func writeRegistryConsumeGasLogsByRun(baseDir, contractName, contractMethod string, blocksByRun map[int][]string) error {
	total := 0
	for _, bs := range blocksByRun {
		total += len(bs)
	}
	if total == 0 {
		return nil
	}

	caseDir := filepath.Join(baseDir, sanitizePathSegment(contractName)+"__"+sanitizePathSegment(contractMethod))
	if err := os.MkdirAll(caseDir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", caseDir, err)
	}

	keys := make([]int, 0, len(blocksByRun))
	for k := range blocksByRun {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i] == -1 {
			return false
		}
		if keys[j] == -1 {
			return true
		}
		return keys[i] < keys[j]
	})

	for _, runIdx := range keys {
		blocks := blocksByRun[runIdx]
		if len(blocks) == 0 {
			continue
		}
		var fname string
		if runIdx == -1 {
			fname = "testid_unbound.txt"
		} else {
			fname = fmt.Sprintf("testid_%d.txt", runIdx)
		}
		path := filepath.Join(caseDir, fname)
		f, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("create %s: %w", path, err)
		}
		if runIdx == -1 {
			_, _ = fmt.Fprintf(f, "contract=%s method=%s testid=unbound block_count=%d\n\n", contractName, contractMethod, len(blocks))
		} else {
			_, _ = fmt.Fprintf(f, "contract=%s method=%s testid=%d block_count=%d\n\n", contractName, contractMethod, runIdx, len(blocks))
		}
		for i, blk := range blocks {
			_, _ = fmt.Fprintf(f, "--- block %d ---\n%s\n\n", i+1, blk)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("close %s: %w", path, err)
		}
	}
	return nil
}

// extractCallDeallocateSiteCounts 提取 vm_pool.go 中 CallDeallocate 各调用点计数。
func extractCallDeallocateSiteCounts(output []byte) map[string]int {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	counts := make(map[string]int)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		marker := "CallDeallocate site="
		idx := strings.Index(line, marker)
		if idx < 0 {
			continue
		}
		rest := line[idx+len(marker):]
		site := rest
		if sp := strings.IndexAny(rest, " \t"); sp >= 0 {
			site = rest[:sp]
		}
		site = strings.TrimSpace(site)
		if site == "" {
			continue
		}
		counts[site]++
	}
	return counts
}

func parseTestID(line string) (int, bool) {
	idx := strings.Index(line, "testid =")
	if idx < 0 {
		return 0, false
	}
	tail := line[idx:]
	var id int
	n, err := fmt.Sscanf(tail, "testid = %d", &id)
	if err != nil || n != 1 {
		return 0, false
	}
	return id, true
}

func parseSameAllFields(line string) (bool, bool) {
	idx := strings.Index(line, "same_all_fields:")
	if idx < 0 {
		return false, false
	}
	tail := line[idx:]
	var v bool
	n, err := fmt.Sscanf(tail, "same_all_fields:%t", &v)
	if err != nil || n != 1 {
		return false, false
	}
	return v, true
}

func appendPerRunRows(writer *csv.Writer, contractName, contractMethod string, perRunLines, instanceCompareLines []string) {
	for i, line := range perRunLines {
		runID := i
		if id, ok := parseTestID(line); ok {
			runID = id
		}
		compareLine := ""
		sameAllFields := ""
		if i < len(instanceCompareLines) {
			compareLine = instanceCompareLines[i]
			if v, ok := parseSameAllFields(compareLine); ok {
				sameAllFields = fmt.Sprintf("%t", v)
			}
		}
		_ = writer.Write([]string{
			contractName,
			contractMethod,
			fmt.Sprintf("%d", runID),
			sameAllFields,
			compareLine,
			line,
		})
	}
	writer.Flush()
}

func parseGasUsedFromPerRunLine(line string) (uint64, bool) {
	idx := strings.Index(line, "gas_used:")
	if idx < 0 {
		return 0, false
	}
	tail := line[idx+len("gas_used:"):]
	var gas uint64
	n, err := fmt.Sscanf(tail, "%d", &gas)
	if err != nil || n != 1 {
		return 0, false
	}
	return gas, true
}

func collectGasDistribution(stats map[gasStatsKey]map[uint64]int, contractName, contractMethod string, perRunLines []string) {
	key := gasStatsKey{contract: contractName, method: contractMethod}
	if _, ok := stats[key]; !ok {
		stats[key] = make(map[uint64]int)
	}
	for _, line := range perRunLines {
		if gas, ok := parseGasUsedFromPerRunLine(line); ok {
			stats[key][gas]++
		}
	}
}

func writeGasDistributionTxt(path string, stats map[gasStatsKey]map[uint64]int) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	_, _ = fmt.Fprintln(file, "contract,method,gas_used,count")

	keys := make([]gasStatsKey, 0, len(stats))
	for key := range stats {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].contract != keys[j].contract {
			return keys[i].contract < keys[j].contract
		}
		return keys[i].method < keys[j].method
	})

	for _, key := range keys {
		gasMap := stats[key]
		gases := make([]uint64, 0, len(gasMap))
		for gas := range gasMap {
			gases = append(gases, gas)
		}
		sort.Slice(gases, func(i, j int) bool { return gases[i] < gases[j] })

		for _, gas := range gases {
			_, _ = fmt.Fprintf(file, "%s,%s,%d,%d\n", key.contract, key.method, gas, gasMap[gas])
		}
	}
	return nil
}

func appendInstanceNotReusedLines(file *os.File, contractName, contractMethod string, lines []string) {
	for _, line := range lines {
		_, _ = fmt.Fprintf(file, "%s,%s,%s\n", contractName, contractMethod, line)
	}
}

func collectDeallocateSiteStats(
	stats map[deallocateSiteStatsKey]int,
	contractName, contractMethod string,
	siteCounts map[string]int,
) {
	for site, cnt := range siteCounts {
		key := deallocateSiteStatsKey{contract: contractName, method: contractMethod, site: site}
		stats[key] += cnt
	}
}

func collectVmPoolGrowStats(
	stats map[vmPoolGrowStatsKey]*vmPoolGrowCaseStats,
	contractName, contractMethod string,
	growSizes []int,
) {
	if len(growSizes) == 0 {
		return
	}
	key := vmPoolGrowStatsKey{contract: contractName, method: contractMethod}
	if stats[key] == nil {
		stats[key] = &vmPoolGrowCaseStats{}
	}
	stats[key].sizes = append(stats[key].sizes, growSizes...)
}

func formatVmPoolGrowSizes(sizes []int) string {
	if len(sizes) == 0 {
		return ""
	}
	parts := make([]string, len(sizes))
	for i, s := range sizes {
		parts[i] = fmt.Sprintf("%d", s)
	}
	return strings.Join(parts, ",")
}

func writeVmPoolGrowStatsTxt(path string, stats map[vmPoolGrowStatsKey]*vmPoolGrowCaseStats) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	keys := make([]vmPoolGrowStatsKey, 0, len(stats))
	for key := range stats {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].contract != keys[j].contract {
			return keys[i].contract < keys[j].contract
		}
		return keys[i].method < keys[j].method
	})

	_, _ = fmt.Fprintln(file, "# summary: contract,method,grow_event_count,grow_sizes")
	for _, key := range keys {
		caseStats := stats[key]
		if caseStats == nil || len(caseStats.sizes) == 0 {
			continue
		}
		_, _ = fmt.Fprintf(file, "%s,%s,%d,%s\n",
			key.contract, key.method, len(caseStats.sizes), formatVmPoolGrowSizes(caseStats.sizes))
	}

	_, _ = fmt.Fprintln(file)
	_, _ = fmt.Fprintln(file, "# detail: contract,method,grow_event_index,grow_size")
	for _, key := range keys {
		caseStats := stats[key]
		if caseStats == nil || len(caseStats.sizes) == 0 {
			continue
		}
		for i, size := range caseStats.sizes {
			_, _ = fmt.Fprintf(file, "%s,%s,%d,%d\n", key.contract, key.method, i+1, size)
		}
	}
	return nil
}

func writeCallDeallocateStatsTxt(path string, stats map[deallocateSiteStatsKey]int) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	_, _ = fmt.Fprintln(file, "contract,method,site,count")

	keys := make([]deallocateSiteStatsKey, 0, len(stats))
	for key := range stats {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].contract != keys[j].contract {
			return keys[i].contract < keys[j].contract
		}
		if keys[i].method != keys[j].method {
			return keys[i].method < keys[j].method
		}
		return keys[i].site < keys[j].site
	})

	for _, key := range keys {
		_, _ = fmt.Fprintf(file, "%s,%s,%s,%d\n", key.contract, key.method, key.site, stats[key])
	}
	return nil
}

func listContractCases(contractsDir string) ([]invokeCase, error) {
	entries, err := os.ReadDir(contractsDir)
	if err != nil {
		return nil, fmt.Errorf("read contracts dir failed: %w", err)
	}

	fset := token.NewFileSet()
	var cases []invokeCase

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		contractName := entry.Name()
		contractFile := filepath.Join(contractsDir, contractName, contractName+".go")
		if _, err = os.Stat(contractFile); err != nil {
			// 跳过没有标准命名 go 文件的目录
			continue
		}

		parsed, err := parser.ParseFile(fset, contractFile, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s failed: %w", contractFile, err)
		}

		var methods []string
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name == nil {
				continue
			}
			name := fn.Name.Name
			// 过滤掉不应被 invoke 的入口。
			if name == "main" {
				continue
			}
			if fn.Type.Params != nil && len(fn.Type.Params.List) > 0 {
				continue
			}
			if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 {
				continue
			}
			methods = append(methods, name)
		}

		sort.Strings(methods)
		for _, method := range methods {
			cases = append(cases, invokeCase{
				contract: contractName,
				method:   method,
			})
		}
	}

	sort.Slice(cases, func(i, j int) bool {
		if cases[i].contract != cases[j].contract {
			return cases[i].contract < cases[j].contract
		}
		return cases[i].method < cases[j].method
	})
	return cases, nil
}

func runInvoke(targetDir, contractMethod, contractName, contractType string, testTime int) (
	totalExecLine, gasSummaryLine string,
	perRunLines, instanceCompareLines, instanceNotReusedLines []string,
	callDeallocateSiteCounts map[string]int,
	vmPoolGrowSizes []int,
	tpsStats invokeTPSStats,
	registryConsumeGasByRun map[int][]string,
	err error,
) {
	// 通过 TestInvoke 间接触发 runtime_test.go 中的 runInvokeWithConfig。
	cmd := exec.Command(
		"go", "test", "-run", "^TestInvoke$", "-v", ".",
		"-args",
		"-invoke_contract_method", contractMethod,
		"-invoke_contract_name", contractName,
		"-invoke_contract_type", contractType,
		"-invoke_test_time", fmt.Sprintf("%d", testTime),
	)
	cmd.Dir = targetDir
	output, err := cmd.CombinedOutput()
	// 保持终端原有输出行为
	_, _ = os.Stdout.Write(output)
	totalExecLine = extractTotalExecutionTimeLine(output)
	gasSummaryLine = extractGasSummaryLine(output)
	perRunLines = extractPerRunResultLines(output)
	instanceCompareLines = extractInstanceCompareLines(output)
	instanceNotReusedLines = extractInstanceNotReusedLines(output)
	callDeallocateSiteCounts = extractCallDeallocateSiteCounts(output)
	vmPoolGrowSizes = extractVmPoolGrowSizes(output)
	tpsStats = extractInvokeTPSStats(output)
	registryConsumeGasByRun = extractRegistryConsumeGasBlocksByRun(output)
	return totalExecLine, gasSummaryLine, perRunLines, instanceCompareLines, instanceNotReusedLines, callDeallocateSiteCounts, vmPoolGrowSizes, tpsStats, registryConsumeGasByRun, err
}

func main() {
	contractMethod := flag.String("method", "", "contract method for TestInvoke; with -name to run single case")
	contractName := flag.String("name", "", "contract name for TestInvoke; with -method to run single case")
	contractType := flag.String("type", "go", "contract type for TestInvoke")
	testTime := flag.Int("times", 100, "loop count for TestInvoke")
	failFast := flag.Bool("fail-fast", false, "stop at first failed invoke case")
	flag.Parse()

	// go func() {
	//     // 只监听本机，避免误暴露
	//     _ = http.ListenAndServe("127.0.0.1:6060", nil)
	// }()

	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get working directory failed: %v\n", err)
		os.Exit(1)
	}
	repoRoot, err := filepath.Abs(filepath.Join(wd, "..", ".."))
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve repo root failed: %v\n", err)
		os.Exit(1)
	}

	targetDir := filepath.Join(repoRoot, "chainmaker-vm-wasmer", "vm-wasmer", "v2@v2.4.0")
	if _, err = os.Stat(targetDir); err != nil {
		fmt.Fprintf(os.Stderr, "target test directory not found: %s, err: %v\n", targetDir, err)
		os.Exit(1)
	}

	// 兼容原有单用例运行模式：同时传 -name 和 -method 时仅执行该用例。
	reportPath := filepath.Join(repoRoot, "totalExecutionTime.txt")
	detailPath := filepath.Join(repoRoot, "testInvokePerRun.csv")
	gasStatsPath := filepath.Join(repoRoot, "methodGasDistribution.txt")
	instanceNotReusedPath := filepath.Join(repoRoot, "instanceNotReused.txt")
	callDeallocateStatsPath := filepath.Join(repoRoot, "callDeallocateStats.txt")
	vmPoolGrowStatsPath := filepath.Join(repoRoot, "vmPoolGrowStats.txt")
	registryConsumeGasLogDir := filepath.Join(repoRoot, "registryConsumeGasLog")
	if err := os.MkdirAll(registryConsumeGasLogDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "create registry ConsumeGas log dir failed: %v\n", err)
		os.Exit(1)
	}
	gasStats := make(map[gasStatsKey]map[uint64]int)
	callDeallocateStats := make(map[deallocateSiteStatsKey]int)
	vmPoolGrowStats := make(map[vmPoolGrowStatsKey]*vmPoolGrowCaseStats)
	reportFile, err := os.Create(reportPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create report file failed: %v\n", err)
		os.Exit(1)
	}
	defer reportFile.Close()
	detailFile, err := os.Create(detailPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create per-run log file failed: %v\n", err)
		os.Exit(1)
	}
	defer detailFile.Close()
	detailWriter := csv.NewWriter(detailFile)
	_ = detailWriter.Write([]string{"contract", "method", "run_index", "instance_same_all_fields", "instance_compare_line", "raw_result_line"})
	detailWriter.Flush()
	_, _ = fmt.Fprintf(reportFile, "contract,method,totalExecutionTime,success_cnt,tps_call_method,gas_max,gas_min,gas_minus_spread,gas_spread_ratio\n")
	instanceNotReusedFile, err := os.Create(instanceNotReusedPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create instance-not-reused log file failed: %v\n", err)
		os.Exit(1)
	}
	defer instanceNotReusedFile.Close()
	_, _ = fmt.Fprintln(instanceNotReusedFile, "contract,method,raw_log_line")

	if strings.TrimSpace(*contractName) != "" && strings.TrimSpace(*contractMethod) != "" {
		fmt.Printf(
			"running single TestInvoke case in %s (method=%s, name=%s, type=%s, times=%d)\n",
			targetDir, *contractMethod, *contractName, *contractType, *testTime,
		)
		totalLine, gasLine, perRunLines, instanceCompareLines, instanceNotReusedLines, callDeallocateSiteCounts, vmPoolGrowSizes, tpsStats, registryByRun, runErr := runInvoke(targetDir, *contractMethod, *contractName, *contractType, *testTime)
		if err := writeRegistryConsumeGasLogsByRun(registryConsumeGasLogDir, *contractName, *contractMethod, registryByRun); err != nil {
			fmt.Fprintf(os.Stderr, "write registry ConsumeGas logs: %v\n", err)
			os.Exit(1)
		}
		appendPerRunRows(detailWriter, *contractName, *contractMethod, perRunLines, instanceCompareLines)
		collectGasDistribution(gasStats, *contractName, *contractMethod, perRunLines)
		appendInstanceNotReusedLines(instanceNotReusedFile, *contractName, *contractMethod, instanceNotReusedLines)
		collectDeallocateSiteStats(callDeallocateStats, *contractName, *contractMethod, callDeallocateSiteCounts)
		collectVmPoolGrowStats(vmPoolGrowStats, *contractName, *contractMethod, vmPoolGrowSizes)
		if len(vmPoolGrowSizes) > 0 {
			fmt.Printf("[vm pool grow] %s.%s events=%d sizes=[%s]\n",
				*contractName, *contractMethod, len(vmPoolGrowSizes), formatVmPoolGrowSizes(vmPoolGrowSizes))
		}
		if totalLine != "" || tpsStats.ok {
			gmax, gmin, gminus, gratio, gok := parseGasSummary(gasLine)
			if gok {
				fmt.Printf("[gas] max=%d min=%d spread=%d spread/minkey=%.15f\n", gmax, gmin, gminus, gratio)
			}
			appendInvokeReportRow(reportFile, *contractName, *contractMethod, totalLine, gasLine, tpsStats)
		}
		if err := writeGasDistributionTxt(gasStatsPath, gasStats); err != nil {
			fmt.Fprintf(os.Stderr, "write gas distribution file failed: %v\n", err)
			os.Exit(1)
		}
		if err := writeCallDeallocateStatsTxt(callDeallocateStatsPath, callDeallocateStats); err != nil {
			fmt.Fprintf(os.Stderr, "write CallDeallocate stats file failed: %v\n", err)
			os.Exit(1)
		}
		if err := writeVmPoolGrowStatsTxt(vmPoolGrowStatsPath, vmPoolGrowStats); err != nil {
			fmt.Fprintf(os.Stderr, "write vm pool grow stats file failed: %v\n", err)
			os.Exit(1)
		}
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "TestInvoke failed: %v\n", runErr)
			os.Exit(1)
		}
		fmt.Printf("totalExecutionTime 已写入: %s\n", reportPath)
		fmt.Printf("逐次执行结果已写入: %s\n", detailPath)
		fmt.Printf("gas 统计已写入: %s\n", gasStatsPath)
		fmt.Printf("instance not reused 日志已写入: %s\n", instanceNotReusedPath)
		fmt.Printf("CallDeallocate 调用点统计已写入: %s\n", callDeallocateStatsPath)
		fmt.Printf("vm pool grow 统计已写入: %s\n", vmPoolGrowStatsPath)
		fmt.Printf("registry ConsumeGas 按 testid 分文件目录: %s\n", registryConsumeGasLogDir)
		fmt.Println("TestInvoke passed.")
		return
	}

	contractsDir := filepath.Join(repoRoot, "testdata")
	cases, err := listContractCases(contractsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discover invoke cases failed: %v\n", err)
		os.Exit(1)
	}
	if len(cases) == 0 {
		fmt.Fprintf(os.Stderr, "no invoke cases found under %s\n", contractsDir)
		os.Exit(1)
	}

	fmt.Printf("discovered %d invoke cases, running in %s (type=%s, times=%d)\n", len(cases), targetDir, *contractType, *testTime)
	failed := 0
	for i, c := range cases {
		fmt.Printf("\n[%d/%d] running %s.%s\n", i+1, len(cases), c.contract, c.method)
		totalLine, gasLine, perRunLines, instanceCompareLines, instanceNotReusedLines, callDeallocateSiteCounts, vmPoolGrowSizes, tpsStats, registryByRun, runErr := runInvoke(targetDir, c.method, c.contract, *contractType, *testTime)
		if err := writeRegistryConsumeGasLogsByRun(registryConsumeGasLogDir, c.contract, c.method, registryByRun); err != nil {
			fmt.Fprintf(os.Stderr, "write registry ConsumeGas logs: %v\n", err)
			os.Exit(1)
		}
		appendPerRunRows(detailWriter, c.contract, c.method, perRunLines, instanceCompareLines)
		collectGasDistribution(gasStats, c.contract, c.method, perRunLines)
		appendInstanceNotReusedLines(instanceNotReusedFile, c.contract, c.method, instanceNotReusedLines)
		collectDeallocateSiteStats(callDeallocateStats, c.contract, c.method, callDeallocateSiteCounts)
		collectVmPoolGrowStats(vmPoolGrowStats, c.contract, c.method, vmPoolGrowSizes)
		if len(vmPoolGrowSizes) > 0 {
			fmt.Printf("[vm pool grow] %s.%s events=%d sizes=[%s]\n",
				c.contract, c.method, len(vmPoolGrowSizes), formatVmPoolGrowSizes(vmPoolGrowSizes))
		}
		if totalLine != "" || tpsStats.ok {
			gmax, gmin, gminus, gratio, gok := parseGasSummary(gasLine)
			if gok {
				fmt.Printf("[gas] %s.%s max=%d min=%d spread=%d spread/minkey=%.15f\n", c.contract, c.method, gmax, gmin, gminus, gratio)
			}
			appendInvokeReportRow(reportFile, c.contract, c.method, totalLine, gasLine, tpsStats)
		}
		if runErr != nil {
			failed++
			fmt.Fprintf(os.Stderr, "FAILED %s.%s: %v\n", c.contract, c.method, runErr)
			if *failFast {
				os.Exit(1)
			}
		}
	}

	if err := writeGasDistributionTxt(gasStatsPath, gasStats); err != nil {
		fmt.Fprintf(os.Stderr, "write gas distribution file failed: %v\n", err)
		os.Exit(1)
	}
	if err := writeCallDeallocateStatsTxt(callDeallocateStatsPath, callDeallocateStats); err != nil {
		fmt.Fprintf(os.Stderr, "write CallDeallocate stats file failed: %v\n", err)
		os.Exit(1)
	}
	if err := writeVmPoolGrowStatsTxt(vmPoolGrowStatsPath, vmPoolGrowStats); err != nil {
		fmt.Fprintf(os.Stderr, "write vm pool grow stats file failed: %v\n", err)
		os.Exit(1)
	}

	if failed > 0 {
		fmt.Fprintf(os.Stderr, "\nfinished with failures: %d/%d failed\n", failed, len(cases))
		os.Exit(1)
	}

	fmt.Printf("\nall invoke cases passed: %d/%d\n", len(cases)-failed, len(cases))
	fmt.Printf("totalExecutionTime 已写入: %s\n", reportPath)
	fmt.Printf("逐次执行结果已写入: %s\n", detailPath)
	fmt.Printf("gas 统计已写入: %s\n", gasStatsPath)
	fmt.Printf("instance not reused 日志已写入: %s\n", instanceNotReusedPath)
	fmt.Printf("CallDeallocate 调用点统计已写入: %s\n", callDeallocateStatsPath)
	fmt.Printf("vm pool grow 统计已写入: %s\n", vmPoolGrowStatsPath)
	fmt.Printf("registry ConsumeGas 按 testid 分文件目录: %s\n", registryConsumeGasLogDir)
}
