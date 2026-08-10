package main

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	wasmPageBytes     = 64 * 1024
	maxPagesLimit     = 512
	maxLinearMemBytes = maxPagesLimit * wasmPageBytes
)

// defaultSweep 单参数 inputSize：覆盖 EasyCodec 1MB 边界
var defaultSweep = []int{
	1024,
	64 * 1024,
	1024 * 1024,
	1572864,
	4 * 1024 * 1024,
	8 * 1024 * 1024,
}

// defaultMultiSweep 分片 inputSizeMulti：总字节数（每片 ≤1MB），阶梯至 64MB 观测失败边界
var defaultMultiSweep = []int{
	1024,
	64 * 1024,
	2 * 1024 * 1024,
	4 * 1024 * 1024,
	8 * 1024 * 1024,
	10 * 1024 * 1024,
	12 * 1024 * 1024,
	16 * 1024 * 1024,
	20 * 1024 * 1024,
	24 * 1024 * 1024,
	32 * 1024 * 1024,
	40 * 1024 * 1024,
	48 * 1024 * 1024,
	56 * 1024 * 1024,
	64 * 1024 * 1024,
}

type bigInputResult struct {
	DataSize   int
	Shards     int
	Expected   string
	Actual     string
	Code       int
	Gas        uint64
	ExecTime   float64
	OK         bool
	MemBytes   int
	MemPages   int
	Message    string
	HasMemInfo bool
}

var (
	reBigInputResult = regexp.MustCompile(`\[biginput-result\] data_size=(\d+)(?: shards=(\d+))? expected=(\S*) actual=(\S*) code=(\d+) gas=(\d+) exec_time=([\d.]+) ok=(true|false)`)
	reExportMemory   = regexp.MustCompile(`exportMemory datasize (\d+)字节 (\d+)页`)
	reBigInputMsg    = regexp.MustCompile(`\[biginput-message\] (.+)`)
)

func parseOutput(output []byte) []bigInputResult {
	lines := bufio.NewScanner(bytes.NewReader(output))
	var results []bigInputResult
	var cur *bigInputResult
	var pendingMemBytes, pendingMemPages int
	var pendingMem bool

	attachPendingMem := func() {
		if cur != nil && pendingMem {
			cur.MemBytes = pendingMemBytes
			cur.MemPages = pendingMemPages
			cur.HasMemInfo = true
			pendingMem = false
		}
	}

	for lines.Scan() {
		line := lines.Text()

		if m := reExportMemory.FindStringSubmatch(line); m != nil {
			pendingMemBytes, _ = strconv.Atoi(m[1])
			pendingMemPages, _ = strconv.Atoi(m[2])
			pendingMem = true
			continue
		}

		if m := reBigInputResult.FindStringSubmatch(line); m != nil {
			if cur != nil {
				results = append(results, *cur)
			}
			cur = &bigInputResult{}
			attachPendingMem()
			cur.DataSize, _ = strconv.Atoi(m[1])
			if m[2] != "" {
				cur.Shards, _ = strconv.Atoi(m[2])
			}
			cur.Expected = m[3]
			cur.Actual = m[4]
			cur.Code, _ = strconv.Atoi(m[5])
			gas, _ := strconv.ParseUint(m[6], 10, 64)
			cur.Gas = gas
			cur.ExecTime, _ = strconv.ParseFloat(m[7], 64)
			cur.OK = m[8] == "true"
			continue
		}

		if cur == nil {
			continue
		}

		if m := reBigInputMsg.FindStringSubmatch(line); m != nil {
			cur.Message = m[1]
		}
	}
	if cur != nil {
		results = append(results, *cur)
	}
	return results
}

func buildWasm(projectRoot string, useInstrumented bool) (string, error) {
	if useInstrumented {
		out := filepath.Join(projectRoot, "chainmaker", "testdata-instrument", "bigInput", "bigInput-go.wasm")
		cmd := exec.Command("go", "build", "-o", out, "./chainmaker/testdata-instrument/bigInput")
		cmd.Dir = projectRoot
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
		if err := cmd.Run(); err != nil {
			return "", err
		}
		return out, nil
	}
	contractDir := filepath.Join(projectRoot, "chainmaker", "testdata", "bigInput")
	cmd := exec.Command("bash", "build.sh", "bigInput", "go")
	cmd.Dir = contractDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return filepath.Join(contractDir, "bigInput-go.wasm"), nil
}

func copyWasm(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, in, 0644)
}

func runTestBigInput(targetDir string, dataSize int, sweep string, contractName, contractType, method string) ([]byte, error) {
	args := []string{
		"test", "-run", "^TestBigInput$", "-v", ".", "-count=1", "-timeout=0",
		"-args",
		"-biginput_contract_name", contractName,
		"-biginput_contract_type", contractType,
		"-biginput_contract_method", method,
	}
	if sweep != "" {
		args = append(args, "-biginput_sweep", sweep)
	} else {
		args = append(args, "-biginput_data_size", fmt.Sprintf("%d", dataSize))
	}

	cmd := exec.Command("go", args...)
	cmd.Dir = targetDir
	return cmd.CombinedOutput()
}

func formatBytes(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.2fMB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	default:
		return fmt.Sprintf("%dB", n)
	}
}

func writeReport(path string, results []bigInputResult) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	_ = w.Write([]string{
		"data_size", "data_size_human", "shards", "expected", "actual", "ok", "code",
		"gas", "exec_time_sec", "mem_bytes", "mem_pages", "mem_human", "message",
	})
	for _, r := range results {
		memHuman := ""
		if r.HasMemInfo {
			memHuman = formatBytes(r.MemBytes)
		}
		shardsStr := ""
		if r.Shards > 0 {
			shardsStr = strconv.Itoa(r.Shards)
		}
		_ = w.Write([]string{
			strconv.Itoa(r.DataSize),
			formatBytes(r.DataSize),
			shardsStr,
			r.Expected,
			r.Actual,
			strconv.FormatBool(r.OK),
			strconv.Itoa(r.Code),
			strconv.FormatUint(r.Gas, 10),
			fmt.Sprintf("%.6f", r.ExecTime),
			strconv.Itoa(r.MemBytes),
			strconv.Itoa(r.MemPages),
			memHuman,
			r.Message,
		})
	}
	w.Flush()
	return w.Error()
}

func main() {
	dataSize := flag.Int("size", 0, "single test: data param size in bytes; 0 means use -sweep or default sweep")
	sweep := flag.String("sweep", "", "comma-separated data sizes in bytes, e.g. 1024,1048576,16777216")
	contractName := flag.String("name", "bigInput", "contract name (wasm: testdata/<name>-<type>.wasm)")
	contractType := flag.String("type", "go", "contract type suffix")
	method := flag.String("method", "", "contract method; default inputSize, or inputSizeMulti with -multi")
	multi := flag.Bool("multi", false, "use inputSizeMulti (sharded params, each dataN ≤1MB)")
	build := flag.Bool("build", false, "build bigInput wasm before test")
	instrumented := flag.Bool("instrumented", true, "use testdata-instrument wasm (required for vmwasmer SetGas/GetGas)")
	reportPath := flag.String("report", "", "CSV report path (relative to chainmaker/); default bigInputReport.csv or bigInputMultiReport.csv")
	flag.Parse()

	if *method == "" {
		if *multi {
			*method = "inputSizeMulti"
		} else {
			*method = "inputSize"
		}
	}
	if *multi && *method == "inputSize" {
		*method = "inputSizeMulti"
	}

	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get working directory failed: %v\n", err)
		os.Exit(1)
	}
	chainmakerRoot, err := filepath.Abs(filepath.Join(wd, "..", ".."))
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve chainmaker root failed: %v\n", err)
		os.Exit(1)
	}
	repoRoot, err := filepath.Abs(filepath.Join(chainmakerRoot, ".."))
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve repo root failed: %v\n", err)
		os.Exit(1)
	}

	targetDir := filepath.Join(chainmakerRoot, "chainmaker-vm-wasmer", "vm-wasmer", "v2@v2.4.0")
	if _, err = os.Stat(targetDir); err != nil {
		fmt.Fprintf(os.Stderr, "vm-wasmer test dir not found: %s (%v)\n", targetDir, err)
		os.Exit(1)
	}

	var wasmSrc string
	if *instrumented {
		wasmSrc = filepath.Join(chainmakerRoot, "testdata-instrument", "bigInput", "bigInput-go.wasm")
	} else {
		wasmSrc = filepath.Join(chainmakerRoot, "testdata", "bigInput", "bigInput-go.wasm")
	}
	wasmDst := filepath.Join(targetDir, "testdata", fmt.Sprintf("%s-%s.wasm", *contractName, *contractType))

	if *build {
		fmt.Printf("building bigInput wasm (instrumented=%v) ...\n", *instrumented)
		built, err := buildWasm(repoRoot, *instrumented)
		if err != nil {
			fmt.Fprintf(os.Stderr, "build wasm failed: %v\n", err)
			os.Exit(1)
		}
		wasmSrc = built
	}

	if _, err := os.Stat(wasmSrc); err != nil {
		hint := "testdata-instrument/bigInput"
		if !*instrumented {
			hint = "testdata/bigInput"
		}
		fmt.Fprintf(os.Stderr, "wasm not found: %s\nRun with -build or build from %s\n", wasmSrc, hint)
		os.Exit(1)
	}
	if err := copyWasm(wasmSrc, wasmDst); err != nil {
		fmt.Fprintf(os.Stderr, "copy wasm to testdata failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("wasm: %s -> %s\n", wasmSrc, wasmDst)

	sweepArg := strings.TrimSpace(*sweep)
	if sweepArg == "" && *dataSize <= 0 {
		base := defaultSweep
		if *method == "inputSizeMulti" {
			base = defaultMultiSweep
		}
		parts := make([]string, len(base))
		for i, s := range base {
			parts[i] = strconv.Itoa(s)
		}
		sweepArg = strings.Join(parts, ",")
		fmt.Printf("no -size/-sweep given, using default %s sweep: %s\n", *method, sweepArg)
	}

	fmt.Printf("\n=== bigInput large-param test ===\n")
	fmt.Printf("target: %s\n", targetDir)
	fmt.Printf("contract=%s method=%s type=%s\n", *contractName, *method, *contractType)
	if *method == "inputSizeMulti" {
		fmt.Printf("mode: multi-shard (part_count + data0..dataN, each shard ≤1MB)\n")
	}
	fmt.Printf("vm linear memory limit: %d pages = %s (MaxPagesLimit=%d)\n\n",
		maxPagesLimit, formatBytes(maxLinearMemBytes), maxPagesLimit)

	output, runErr := runTestBigInput(targetDir, *dataSize, sweepArg, *contractName, *contractType, *method)
	_, _ = os.Stdout.Write(output)

	results := parseOutput(output)
	if len(results) == 0 {
		fmt.Fprintln(os.Stderr, "no [biginput-result] lines parsed from test output")
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "TestBigInput failed: %v\n", runErr)
			os.Exit(1)
		}
		os.Exit(1)
	}

	fmt.Printf("\n--- summary ---\n")
	passed, failed := 0, 0
	for _, r := range results {
		status := "PASS"
		if !r.OK {
			status = "FAIL"
			failed++
		} else {
			passed++
		}
		memInfo := "n/a"
		if r.HasMemInfo {
			memInfo = fmt.Sprintf("%s (%d pages)", formatBytes(r.MemBytes), r.MemPages)
		}
		fmt.Printf("[%s] data=%s", status, formatBytes(r.DataSize))
		if r.Shards > 0 {
			fmt.Printf(" shards=%d", r.Shards)
		}
		fmt.Printf(" code=%d gas=%d exec=%.4fs mem=%s",
			r.Code, r.Gas, r.ExecTime, memInfo)
		if r.Message != "" {
			fmt.Printf(" msg=%s", r.Message)
		}
		fmt.Println()
	}
	fmt.Printf("passed=%d failed=%d total=%d\n", passed, failed, len(results))

	reportFile := *reportPath
	if reportFile == "" {
		if *method == "inputSizeMulti" {
			reportFile = "bigInputMultiReport.csv"
		} else {
			reportFile = "bigInputReport.csv"
		}
	}
	if !filepath.IsAbs(reportFile) {
		reportFile = filepath.Join(chainmakerRoot, reportFile)
	}
	if err := writeReport(reportFile, results); err != nil {
		fmt.Fprintf(os.Stderr, "write report failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\nreport written: %s (time=%s)\n", reportFile, time.Now().Format(time.RFC3339))

	if failed > 0 || runErr != nil {
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "TestBigInput exit error: %v\n", runErr)
		}
		os.Exit(1)
	}
}
