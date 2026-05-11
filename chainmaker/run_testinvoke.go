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
	return totalExecLine, gasSummaryLine, perRunLines, instanceCompareLines, instanceNotReusedLines, callDeallocateSiteCounts, err
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

	targetDir := filepath.Join(wd, "chainmaker-vm-wasmer", "vm-wasmer", "v2@v2.4.0")
	if _, err = os.Stat(targetDir); err != nil {
		fmt.Fprintf(os.Stderr, "target test directory not found: %s, err: %v\n", targetDir, err)
		os.Exit(1)
	}

	// 兼容原有单用例运行模式：同时传 -name 和 -method 时仅执行该用例。
	reportPath := filepath.Join(wd, "totalExecutionTime.txt")
	detailPath := filepath.Join(wd, "testInvokePerRun.csv")
	gasStatsPath := filepath.Join(wd, "methodGasDistribution.txt")
	instanceNotReusedPath := filepath.Join(wd, "instanceNotReused.txt")
	callDeallocateStatsPath := filepath.Join(wd, "callDeallocateStats.txt")
	gasStats := make(map[gasStatsKey]map[uint64]int)
	callDeallocateStats := make(map[deallocateSiteStatsKey]int)
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
	_, _ = fmt.Fprintf(reportFile, "contract,method,totalExecutionTime,gas_max,gas_min,gas_minus_spread,gas_spread_ratio\n")
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
		totalLine, gasLine, perRunLines, instanceCompareLines, instanceNotReusedLines, callDeallocateSiteCounts, runErr := runInvoke(targetDir, *contractMethod, *contractName, *contractType, *testTime)
		appendPerRunRows(detailWriter, *contractName, *contractMethod, perRunLines, instanceCompareLines)
		collectGasDistribution(gasStats, *contractName, *contractMethod, perRunLines)
		appendInstanceNotReusedLines(instanceNotReusedFile, *contractName, *contractMethod, instanceNotReusedLines)
		collectDeallocateSiteStats(callDeallocateStats, *contractName, *contractMethod, callDeallocateSiteCounts)
		if totalLine != "" {
			gmax, gmin, gminus, gratio, gok := parseGasSummary(gasLine)
			if gok {
				fmt.Printf("[gas] max=%d min=%d spread=%d spread/minkey=%.15f\n", gmax, gmin, gminus, gratio)
				_, _ = fmt.Fprintf(reportFile, "%s,%s,%s,%d,%d,%d,%.15f\n",
					*contractName, *contractMethod, strings.TrimPrefix(totalLine, "totalExecutionTime "),
					gmax, gmin, gminus, gratio)
			} else {
				if gasLine != "" {
					fmt.Fprintf(os.Stderr, "[gas] could not parse summary from log line: %s\n", gasLine)
				}
				_, _ = fmt.Fprintf(reportFile, "%s,%s,%s,,,,\n", *contractName, *contractMethod, strings.TrimPrefix(totalLine, "totalExecutionTime "))
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
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "TestInvoke failed: %v\n", runErr)
			os.Exit(1)
		}
		fmt.Printf("totalExecutionTime 已写入: %s\n", reportPath)
		fmt.Printf("逐次执行结果已写入: %s\n", detailPath)
		fmt.Printf("gas 统计已写入: %s\n", gasStatsPath)
		fmt.Printf("instance not reused 日志已写入: %s\n", instanceNotReusedPath)
		fmt.Printf("CallDeallocate 调用点统计已写入: %s\n", callDeallocateStatsPath)
		fmt.Println("TestInvoke passed.")
		return
	}

	contractsDir := filepath.Join(wd, "testdata")
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
		totalLine, gasLine, perRunLines, instanceCompareLines, instanceNotReusedLines, callDeallocateSiteCounts, runErr := runInvoke(targetDir, c.method, c.contract, *contractType, *testTime)
		appendPerRunRows(detailWriter, c.contract, c.method, perRunLines, instanceCompareLines)
		collectGasDistribution(gasStats, c.contract, c.method, perRunLines)
		appendInstanceNotReusedLines(instanceNotReusedFile, c.contract, c.method, instanceNotReusedLines)
		collectDeallocateSiteStats(callDeallocateStats, c.contract, c.method, callDeallocateSiteCounts)
		if totalLine != "" {
			gmax, gmin, gminus, gratio, gok := parseGasSummary(gasLine)
			if gok {
				fmt.Printf("[gas] %s.%s max=%d min=%d spread=%d spread/minkey=%.15f\n", c.contract, c.method, gmax, gmin, gminus, gratio)
				_, _ = fmt.Fprintf(reportFile, "%s,%s,%s,%d,%d,%d,%.15f\n",
					c.contract, c.method, strings.TrimPrefix(totalLine, "totalExecutionTime "),
					gmax, gmin, gminus, gratio)
			} else {
				if gasLine != "" {
					fmt.Fprintf(os.Stderr, "[gas] %s.%s could not parse: %s\n", c.contract, c.method, gasLine)
				}
				_, _ = fmt.Fprintf(reportFile, "%s,%s,%s,,,,\n", c.contract, c.method, strings.TrimPrefix(totalLine, "totalExecutionTime "))
			}
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
}
