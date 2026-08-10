package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func extractOOMSampleLines(output []byte) []string {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	var lines []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.Contains(line, "[oom-sample]") || strings.Contains(line, "[oom-summary]") || strings.Contains(line, "[oom-warn]") {
			lines = append(lines, line)
		}
	}
	return lines
}

func runPoolOOMLoadTest(targetDir, contractName, contractType, wasmPath string, contractCount int, duration, sampleInterval time.Duration, memWarnMB uint64, useManager bool) error {
	args := []string{
		"test", "-run", "^TestPoolOOMLoadOnly$", "-v", ".", "-count=1", "-timeout=0",
		"-args",
		"-oom_contract_name", contractName,
		"-oom_contract_type", contractType,
		"-oom_contract_count", fmt.Sprintf("%d", contractCount),
		"-oom_sample_interval", sampleInterval.String(),
		"-oom_mem_warn_mb", fmt.Sprintf("%d", memWarnMB),
		"-oom_use_manager", fmt.Sprintf("%t", useManager),
	}
	if wasmPath != "" {
		args = append(args, "-oom_wasm", wasmPath)
	}
	if duration > 0 {
		args = append(args, "-oom_duration", duration.String())
	}

	cmd := exec.Command("go", args...)
	cmd.Dir = targetDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func main() {
	contractName := flag.String("name", "oom-load-test", "contract name prefix for loaded pools")
	contractType := flag.String("type", "go", "contract type; used when -wasm is empty (testdata/name-type.wasm)")
	wasmPath := flag.String("wasm", "", "wasm file path; default testdata/<name>-<type>.wasm under vm-wasmer module")
	contractCount := flag.Int("contracts", 1, "number of contract pools to load")
	duration := flag.Duration("duration", 0, "max wait; 0 runs until OOM/kill (requires go test -timeout 0)")
	sampleInterval := flag.Duration("sample", 5*time.Second, "memory/pool stats log interval")
	memWarnMB := flag.Uint64("mem-warn-mb", 2048, "warn when Go MemStats.Sys exceeds this MB; 0 disables")
	useManager := flag.Bool("use-manager", true, "load via InstancesManager.getVmPool")
	reportPath := flag.String("report", "poolOomLoadReport.txt", "output file for oom sample lines")
	flag.Parse()

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

	resolvedWasm := *wasmPath
	if resolvedWasm == "" {
		resolvedWasm = filepath.Join(targetDir, "testdata", fmt.Sprintf("%s-%s.wasm", *contractName, *contractType))
	} else if !filepath.IsAbs(resolvedWasm) {
		// 相对路径优先相对于仓库根目录 wd
		candidate := filepath.Join(repoRoot, resolvedWasm)
		if _, err := os.Stat(candidate); err == nil {
			resolvedWasm = candidate
		}
	}
	if _, err := os.Stat(resolvedWasm); err != nil {
		fmt.Fprintf(os.Stderr, "wasm not found: %s (%v)\n", resolvedWasm, err)
		os.Exit(1)
	}

	fmt.Printf("=== pool OOM load-only test ===\n")
	fmt.Printf("target: %s\n", targetDir)
	fmt.Printf("contracts=%d wasm=%s sample=%v duration=%v mem-warn=%dMB use-manager=%v\n",
		*contractCount, resolvedWasm, *sampleInterval, *duration, *memWarnMB, *useManager)
	fmt.Printf("note: only loads wasm/pool; no Invoke. pool grows via serial manual grow in TestPoolOOMLoadOnly\n\n")

	runErr := runPoolOOMLoadTest(targetDir, *contractName, *contractType, resolvedWasm,
		*contractCount, *duration, *sampleInterval, *memWarnMB, *useManager)

	// 由于已改为实时输出，这里不再生成详细的 oom-sample 报告
	// 如需事后分析，可直接查看子进程的终端输出或调整 pool_oom_test.go 增加文件日志
	reportFile, err := os.Create(filepath.Join(repoRoot, *reportPath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create report failed: %v\n", err)
		os.Exit(1)
	}
	defer reportFile.Close()

	_, _ = fmt.Fprintf(reportFile, "# pool OOM load-only report\n")
	_, _ = fmt.Fprintf(reportFile, "# time=%s\n", time.Now().Format(time.RFC3339))
	_, _ = fmt.Fprintf(reportFile, "# contracts=%d wasm=%s\n\n", *contractCount, resolvedWasm)
	_, _ = fmt.Fprintln(reportFile, "## note: realtime output enabled, see terminal for [oom-sample] logs")

	fmt.Printf("\nreport written: %s (realtime mode, no parsed samples)\n", *reportPath)

	if runErr != nil {
		if strings.Contains(runErr.Error(), "signal: killed") || strings.Contains(runErr.Error(), "Killed") {
			fmt.Fprintf(os.Stderr, "process likely OOM-killed: %v\n", runErr)
			os.Exit(2)
		}
		// CombinedOutput 不再有 output，这里简单判断
		if *duration > 0 {
			fmt.Println("Test finished within -duration (no OOM).")
			return
		}
		fmt.Fprintf(os.Stderr, "TestPoolOOMLoadOnly failed: %v\n", runErr)
		os.Exit(1)
	}

	if *duration > 0 {
		fmt.Println("Test finished within -duration (no OOM).")
	} else {
		fmt.Println("Test exited without error (unexpected if waiting for OOM); check logs.")
	}
}
