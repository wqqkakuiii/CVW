/*
Copyright (C) BABEC. All rights reserved.
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wasmer

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	logger2 "chainmaker.org/chainmaker/logger/v2"
	wasmergo "chainmaker.org/chainmaker/vm-wasmer/v2/wasmer-go"
)

var (
	compilerThreadsProbeWasms = flag.String("compiler_threads_probe_wasms",
		strings.Join(defaultCompilerThreadsWasms(), ","),
		"comma-separated wasm paths for compiler threads probe")
	compilerThreadsProbeThreads = flag.String("compiler_threads_probe_threads", "12,4,2,1",
		"comma-separated compiler_num_threads values (wasm_config_sys_set_compiler_num_threads)")
	compilerThreadsProbeSettle = flag.Duration("compiler_threads_probe_settle", 300*time.Millisecond,
		"sleep before each RSS sample")
	compilerThreadsProbeLog = flag.String("compiler_threads_probe_log", "",
		"result log; empty => ./resource_probe_compiler_threads_<timestamp>.log")
	compilerThreadsProbeResultFile = flag.String("compiler_threads_probe_result_file", "",
		"single-case worker writes one CSV row here (TestCompilerThreadsSingle)")
)

func defaultCompilerThreadsWasms() []string {
	return []string{
		"./testdata/fact-go.wasm",
		"./testdata/exchange-go.wasm",
		"./testdata/erc721-go.wasm",
		"./testdata/compute-go.wasm",
		"./testdata/identity-go.wasm",
		"./testdata/raffle-go.wasm",
		"./testdata/itinerary-go.wasm",
		"./testdata/bigInput-go.wasm",
	}
}

type compilerThreadsRow struct {
	Contract     string
	Threads      uint32
	WasmMB       float64
	Elapsed      time.Duration
	DeltaRSSMB   float64
	DeltaAnonMB  float64
	DeltaThreads int
	ProcThreads  int
}

func (r compilerThreadsRow) csvLine() string {
	return fmt.Sprintf("%s,%d,%.4f,%.6f,%+.4f,%+.4f,%d,%d",
		r.Contract, r.Threads, r.WasmMB, r.Elapsed.Seconds(),
		r.DeltaRSSMB, r.DeltaAnonMB, r.DeltaThreads, r.ProcThreads)
}

func parseCompilerThreadsCSVLine(line string) (compilerThreadsRow, error) {
	parts := strings.Split(line, ",")
	if len(parts) != 8 {
		return compilerThreadsRow{}, fmt.Errorf("expected 8 fields, got %d: %q", len(parts), line)
	}
	wasmMB, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return compilerThreadsRow{}, err
	}
	compileS, err := strconv.ParseFloat(parts[3], 64)
	if err != nil {
		return compilerThreadsRow{}, err
	}
	deltaRSS, err := strconv.ParseFloat(parts[4], 64)
	if err != nil {
		return compilerThreadsRow{}, err
	}
	deltaAnon, err := strconv.ParseFloat(parts[5], 64)
	if err != nil {
		return compilerThreadsRow{}, err
	}
	th, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return compilerThreadsRow{}, err
	}
	dt, err := strconv.Atoi(parts[6])
	if err != nil {
		return compilerThreadsRow{}, err
	}
	pt, err := strconv.Atoi(parts[7])
	if err != nil {
		return compilerThreadsRow{}, err
	}
	return compilerThreadsRow{
		Contract:     parts[0],
		Threads:      uint32(th),
		WasmMB:       wasmMB,
		Elapsed:      time.Duration(compileS * float64(time.Second)),
		DeltaRSSMB:   deltaRSS,
		DeltaAnonMB:  deltaAnon,
		DeltaThreads: dt,
		ProcThreads:  pt,
	}, nil
}

func newCompileProbeConfig(compilerThreads uint32) *wasmergo.Config {
	config := wasmergo.NewConfig()
	config.MaxPagesLimit(512)
	config.PushMeteringMiddleware(1e19, map[wasmergo.Opcode]uint32{wasmergo.Opcode(0): 0}, map[string]uint32{}, "")
	config.CompilerNumThreads(compilerThreads)
	return config
}

func parseUint32List(s string) ([]uint32, error) {
	parts := splitCSV(s)
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty thread list")
	}
	out := make([]uint32, 0, len(parts))
	for _, p := range parts {
		v, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid thread count %q: %w", p, err)
		}
		out = append(out, uint32(v))
	}
	return out, nil
}

func runOneCompilerThreadsProbe(wasmPath string, th uint32, settle time.Duration, detail io.Writer) (compilerThreadsRow, error) {
	byteCode, err := os.ReadFile(wasmPath)
	if err != nil {
		return compilerThreadsRow{}, err
	}
	contract := strings.TrimSuffix(filepath.Base(wasmPath), filepath.Ext(wasmPath))
	wasmMB := float64(len(byteCode)) / 1024 / 1024
	logger := logger2.GetLogger("compiler_threads_probe")

	settleFn := func() {
		if settle > 0 {
			time.Sleep(settle)
		}
	}
	sampleNoGC := func(label string) ResourceSnapshot {
		settleFn()
		return SampleResourceNoGC(label)
	}
	logf := func(format string, args ...interface{}) {
		if detail == nil {
			return
		}
		line := fmt.Sprintf(format, args...)
		if !strings.HasSuffix(line, "\n") {
			line += "\n"
		}
		_, _ = io.WriteString(detail, line)
	}

	processBase := sampleNoGC("process_baseline")
	logf("%s", processBase.String())

	config := newCompileProbeConfig(th)
	engine := wasmergo.NewEngineWithConfig(config)
	store := wasmergo.NewStore(engine)
	afterStore := sampleNoGC("after_store")
	logf("%s  %s", afterStore.String(), afterStore.Sub(processBase).String())

	if err := wasmergo.ValidateModule(store, byteCode); err != nil {
		store.Close()
		return compilerThreadsRow{}, fmt.Errorf("ValidateModule: %w", err)
	}
	afterValidate := sampleNoGC("after_validate")
	logf("%s  %s", afterValidate.String(), afterValidate.Sub(afterStore).String())

	compileStart := time.Now()
	module, err := wasmergo.NewModule(store, byteCode, logger)
	compileElapsed := time.Since(compileStart)
	if err != nil {
		store.Close()
		return compilerThreadsRow{}, fmt.Errorf("NewModule: %w", err)
	}
	afterCompile := sampleNoGC("after_NewModule")
	compileΔ := afterCompile.Sub(afterValidate)
	logf("%s  %s", afterCompile.String(), compileΔ.String())

	module.Close()
	store.Close()
	runtime.GC()
	afterTeardown := sampleNoGC("after_teardown")
	logf("%s  %s", afterTeardown.String(), afterTeardown.Sub(afterCompile).String())
	logf("ratio ΔVmRSS/wasm=%.2fx ΔAnon/wasm=%.2fx",
		float64(compileΔ.VmRSSKB)/1024/wasmMB, float64(compileΔ.AnonRSSKB)/1024/wasmMB)

	return compilerThreadsRow{
		Contract:     contract,
		Threads:      th,
		WasmMB:       wasmMB,
		Elapsed:      compileElapsed,
		DeltaRSSMB:   float64(compileΔ.VmRSSKB) / 1024,
		DeltaAnonMB:  float64(compileΔ.AnonRSSKB) / 1024,
		DeltaThreads: compileΔ.Threads,
		ProcThreads:  afterCompile.Threads,
	}, nil
}

// TestCompilerThreadsSingle runs one wasm + one thread in a fresh process (worker for matrix).
func TestCompilerThreadsSingle(t *testing.T) {
	wasms := splitCSV(*compilerThreadsProbeWasms)
	if len(wasms) != 1 {
		t.Fatalf("TestCompilerThreadsSingle requires exactly one wasm in -compiler_threads_probe_wasms, got %d", len(wasms))
	}
	threadList, err := parseUint32List(*compilerThreadsProbeThreads)
	if err != nil {
		t.Fatal(err)
	}
	if len(threadList) != 1 {
		t.Fatalf("TestCompilerThreadsSingle requires exactly one thread in -compiler_threads_probe_threads, got %d", len(threadList))
	}
	row, err := runOneCompilerThreadsProbe(wasms[0], threadList[0], *compilerThreadsProbeSettle, os.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	resultPath := *compilerThreadsProbeResultFile
	if resultPath == "" {
		t.Fatal("compiler_threads_probe_result_file is required for TestCompilerThreadsSingle")
	}
	line := row.csvLine() + "\n"
	if err := os.WriteFile(resultPath, []byte(line), 0o644); err != nil {
		t.Fatalf("write result: %v", err)
	}
	t.Logf("RESULT %s", strings.TrimSpace(line))
}

// TestCompilerThreadsMatrixIsolated runs each contract×threads in a new go test process
// so ΔVmRSS is measured without cross-case RSS accumulation.
//
//	go test -run TestCompilerThreadsMatrixIsolated -v -count=1 -timeout 90m
func TestCompilerThreadsMatrixIsolated(t *testing.T) {
	logPath := *compilerThreadsProbeLog
	if logPath == "" {
		logPath = fmt.Sprintf("./resource_probe_compiler_threads_isolated_%s.log", time.Now().Format("20060102_150405"))
	}
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	defer f.Close()
	w := io.MultiWriter(f, probeTestWriter{t})
	logf := func(format string, args ...interface{}) {
		line := fmt.Sprintf(format, args...)
		if !strings.HasSuffix(line, "\n") {
			line += "\n"
		}
		_, _ = io.WriteString(w, line)
	}

	wasms := splitCSV(*compilerThreadsProbeWasms)
	if len(wasms) == 0 {
		t.Fatal("no wasm paths")
	}
	threadList, err := parseUint32List(*compilerThreadsProbeThreads)
	if err != nil {
		t.Fatal(err)
	}

	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir, err := os.MkdirTemp("", "compiler_threads_probe_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	logf("=== TestCompilerThreadsMatrixIsolated (fresh process per case) ===")
	logf("time=%s", time.Now().Format(time.RFC3339))
	logf("log_file=%s", logPath)
	logf("host_nproc=%d", runtime.NumCPU())
	logf("pkg_dir=%s", pkgDir)
	logf("threads=%v", threadList)
	logf("contracts=%v", wasms)
	logf("settle=%v", *compilerThreadsProbeSettle)
	logf("")

	rows := make([]compilerThreadsRow, 0, len(wasms)*len(threadList))
	for wi, wasmPath := range wasms {
		contract := strings.TrimSuffix(filepath.Base(wasmPath), filepath.Ext(wasmPath))
		for _, th := range threadList {
			resultFile := filepath.Join(tmpDir, fmt.Sprintf("%s_t%d.csv", contract, th))
			detailFile := filepath.Join(tmpDir, fmt.Sprintf("%s_t%d.detail.log", contract, th))
			t0 := time.Now()
			cmd := exec.Command("go", "test",
				"-run", "^TestCompilerThreadsSingle$",
				"-count=1",
				"-timeout", "10m",
				"-args",
				"-compiler_threads_probe_wasms="+wasmPath,
				fmt.Sprintf("-compiler_threads_probe_threads=%d", th),
				fmt.Sprintf("-compiler_threads_probe_settle=%s", compilerThreadsProbeSettle.String()),
				"-compiler_threads_probe_result_file="+resultFile,
			)
			cmd.Dir = pkgDir
			detailOut, err := os.Create(detailFile)
			if err != nil {
				t.Fatalf("create detail log: %v", err)
			}
			cmd.Stdout = detailOut
			cmd.Stderr = detailOut
			runErr := cmd.Run()
			detailOut.Close()
			if runErr != nil {
				detailBytes, _ := os.ReadFile(detailFile)
				t.Fatalf("subprocess %s t=%d failed: %v\n%s", contract, th, runErr, string(detailBytes))
			}
			raw, err := os.ReadFile(resultFile)
			if err != nil {
				t.Fatalf("read result %s: %v", resultFile, err)
			}
			row, err := parseCompilerThreadsCSVLine(strings.TrimSpace(string(raw)))
			if err != nil {
				t.Fatalf("parse result %s: %v", resultFile, err)
			}
			rows = append(rows, row)
			logf("--- [%d/%d] %s threads=%d subprocess_elapsed=%v ---",
				len(rows), len(wasms)*len(threadList), contract, th, time.Since(t0))
			logf("  %s", row.csvLine())
			detailBytes, _ := os.ReadFile(detailFile)
			for _, line := range strings.Split(string(detailBytes), "\n") {
				if strings.TrimSpace(line) != "" {
					logf("  %s", line)
				}
			}
			logf("")
		}
		logf("########## contract[%d/%d] %s done ##########", wi+1, len(wasms), contract)
	}

	writeCompilerThreadsSummary(logf, wasms, threadList, rows)
	logf("log_file=%s", logPath)
	t.Logf("isolated compiler threads matrix written to %s", logPath)
}

func writeCompilerThreadsSummary(logf func(string, ...interface{}), wasms []string, threadList []uint32, rows []compilerThreadsRow) {
	logf("=== SUMMARY (ΔVmRSS MB: validate → NewModule) ===")
	logf("contract | threads | wasm_MB | compile_s | ΔVmRSS_MB | ΔAnon_MB | Δthreads | proc_threads")
	for _, r := range rows {
		logf("%s | %d | %.2f | %.3f | %+.2f | %+.2f | %+d | %d",
			r.Contract, r.Threads, r.WasmMB, r.Elapsed.Seconds(),
			r.DeltaRSSMB, r.DeltaAnonMB, r.DeltaThreads, r.ProcThreads)
	}
	logf("")
	logf("=== PIVOT: ΔVmRSS_MB by contract × threads ===")
	header := "contract"
	for _, th := range threadList {
		header += fmt.Sprintf(" | t=%d", th)
	}
	logf(header)
	for _, wasmPath := range wasms {
		contract := strings.TrimSuffix(filepath.Base(wasmPath), filepath.Ext(wasmPath))
		line := contract
		for _, th := range threadList {
			val := "?"
			for _, r := range rows {
				if r.Contract == contract && r.Threads == th {
					val = fmt.Sprintf("%+.1f", r.DeltaRSSMB)
					break
				}
			}
			line += " | " + val
		}
		logf(line)
	}
	logf("")
	logf("=== PIVOT: compile_seconds by contract × threads ===")
	logf(header)
	for _, wasmPath := range wasms {
		contract := strings.TrimSuffix(filepath.Base(wasmPath), filepath.Ext(wasmPath))
		line := contract
		for _, th := range threadList {
			val := "?"
			for _, r := range rows {
				if r.Contract == contract && r.Threads == th {
					val = fmt.Sprintf("%.2f", r.Elapsed.Seconds())
					break
				}
			}
			line += " | " + val
		}
		logf(line)
	}
}
