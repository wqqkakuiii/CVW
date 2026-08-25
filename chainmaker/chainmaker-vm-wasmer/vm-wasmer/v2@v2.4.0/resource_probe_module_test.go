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
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	logger2 "chainmaker.org/chainmaker/logger/v2"
	wasmergo "chainmaker.org/chainmaker/vm-wasmer/v2/wasmer-go"
)

var (
	moduleProbeWasm = flag.String("module_probe_wasm", "./testdata/test.wasm",
		"wasm path for TestModuleCompileRSS")
	moduleProbeN = flag.Int("module_probe_n", 1,
		"how many times to NewModule (same store); >1 checks accumulate vs reuse")
	moduleProbeSettle = flag.Duration("module_probe_settle", 300*time.Millisecond,
		"settle before each RSS sample")
	moduleProbeLog = flag.String("module_probe_log", "",
		"result log; empty => ./resource_probe_module_<timestamp>.log")
	moduleProbeCloseEach = flag.Bool("module_probe_close_each", true,
		"Module.Close after each compile when n>1 (isolate compile cost)")
)

// TestModuleCompileRSS measures Host RSS / Anon attributed to wasm compile (NewModule).
// Stages (no Instance / no wasiEnv):
//
//	0 baseline
//	1 after Engine+Store
//	2 after ReadFile (Go []byte only)
//	3 after ValidateModule
//	4 after NewModule  ← compile cost (primary)
//	5 after Module.Close (optional reclaim check)
//
//	go test -run TestModuleCompileRSS -v -count=1 -timeout 30m \
//	  -args -module_probe_wasm=./testdata/test.wasm -module_probe_n=1
func TestModuleCompileRSS(t *testing.T) {
	logPath := *moduleProbeLog
	if logPath == "" {
		logPath = fmt.Sprintf("./resource_probe_module_%s.log", time.Now().Format("20060102_150405"))
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

	n := *moduleProbeN
	if n < 1 {
		t.Fatal("module_probe_n must be >= 1")
	}
	wasmPath := *moduleProbeWasm
	fi, err := os.Stat(wasmPath)
	if err != nil {
		t.Fatalf("stat wasm %s: %v", wasmPath, err)
	}

	settle := func() {
		if *moduleProbeSettle > 0 {
			time.Sleep(*moduleProbeSettle)
		}
	}
	sample := func(label string) ResourceSnapshot {
		settle()
		return SampleResource(label)
	}

	logf("=== TestModuleCompileRSS ===")
	logf("time=%s", time.Now().Format(time.RFC3339))
	logf("log_file=%s", logPath)
	logf("wasm=%s size=%.2fMB n=%d settle=%v close_each=%v",
		wasmPath, float64(fi.Size())/1024/1024, n, *moduleProbeSettle, *moduleProbeCloseEach)
	logf("focus: ΔRSS of NewModule (wasm_module_new / LLVM compile); no Instance")
	logf("")

	base := sample("0_baseline")
	logf("%s", base.String())

	logger := logger2.GetLogger("module_probe")
	config := wasmergo.NewConfig()
	config.MaxPagesLimit(512)
	config.PushMeteringMiddleware(1e19, map[wasmergo.Opcode]uint32{wasmergo.Opcode(0): 0}, map[string]uint32{}, "")
	engine := wasmergo.NewEngineWithConfig(config)
	store := wasmergo.NewStore(engine)
	defer store.Close()

	afterStore := sample("1_after_engine_store")
	logf("%s", afterStore.String())
	logf("  %s", afterStore.Sub(base).String())

	byteCode, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read wasm: %v", err)
	}
	afterRead := sample("2_after_ReadFile")
	logf("%s", afterRead.String())
	logf("  %s  (file=%.2fMB)", afterRead.Sub(afterStore).String(), float64(len(byteCode))/1024/1024)

	if err := wasmergo.ValidateModule(store, byteCode); err != nil {
		t.Fatalf("ValidateModule: %v", err)
	}
	afterValidate := sample("3_after_ValidateModule")
	logf("%s", afterValidate.String())
	logf("  %s", afterValidate.Sub(afterRead).String())

	var (
		firstCompileBefore = afterValidate
		firstCompileAfter  ResourceSnapshot
		lastAfterClose     ResourceSnapshot
		modulesKept        []*wasmergo.Module
	)

	for i := 0; i < n; i++ {
		tag := fmt.Sprintf("compile_%d", i)
		before := sample(tag + "_before_NewModule")
		if i == 0 {
			// Prefer contiguous delta from validate → first NewModule for primary verdict.
			before = afterValidate
		}
		t0 := time.Now()
		module, err := wasmergo.NewModule(store, byteCode, logger)
		compileElapsed := time.Since(t0)
		if err != nil {
			t.Fatalf("NewModule #%d: %v", i, err)
		}
		after := sample(fmt.Sprintf("4_%s_after_NewModule", tag))
		d := after.Sub(before)
		logf("--- NewModule #%d/%d elapsed=%v ---", i+1, n, compileElapsed)
		logf("  before: %s", before.String())
		logf("  after:  %s", after.String())
		logf("  %s", d.String())
		logf("  ratio: ΔVmRSS / wasm_file = %.2fx  ΔAnon / wasm_file = %.2fx",
			(float64(d.VmRSSKB)/1024)/(float64(len(byteCode))/1024/1024),
			(float64(d.AnonRSSKB)/1024)/(float64(len(byteCode))/1024/1024))

		if i == 0 {
			firstCompileAfter = after
		}

		if *moduleProbeCloseEach || i == n-1 && !*moduleProbeCloseEach {
			if *moduleProbeCloseEach {
				module.Close()
				module = nil
				runtime.GC()
				afterClose := sample(fmt.Sprintf("5_%s_after_Module_Close", tag))
				logf("  after Close: %s", afterClose.String())
				logf("  %s", afterClose.Sub(after).String())
				lastAfterClose = afterClose
			} else {
				modulesKept = append(modulesKept, module)
			}
		} else {
			modulesKept = append(modulesKept, module)
		}
	}

	for _, m := range modulesKept {
		m.Close()
	}
	modulesKept = nil
	runtime.GC()
	if !*moduleProbeCloseEach {
		lastAfterClose = sample("5_after_all_Module_Close")
		logf("--- after all Module.Close ---")
		logf("%s", lastAfterClose.String())
		logf("  %s", lastAfterClose.Sub(firstCompileAfter).String())
	}

	final := sample("6_final")
	logf("%s", final.String())
	logf("  vs baseline: %s", final.Sub(base).String())

	compileΔ := firstCompileAfter.Sub(firstCompileBefore)
	reclaimΔ := ResourceDelta{}
	if lastAfterClose.VmRSSKB > 0 {
		reclaimΔ = lastAfterClose.Sub(firstCompileAfter)
	}

	logf("")
	logf("=== ANALYSIS ===")
	logf("wasm_file=%.2fMB", float64(len(byteCode))/1024/1024)
	logf("Δ_engine_store: VmRSS=%+.2fMB Anon=%+.2fMB",
		float64(afterStore.Sub(base).VmRSSKB)/1024, float64(afterStore.Sub(base).AnonRSSKB)/1024)
	logf("Δ_ReadFile:     VmRSS=%+.2fMB Anon=%+.2fMB (Go heap for bytecode)",
		float64(afterRead.Sub(afterStore).VmRSSKB)/1024, float64(afterRead.Sub(afterStore).AnonRSSKB)/1024)
	logf("Δ_Validate:     VmRSS=%+.2fMB Anon=%+.2fMB",
		float64(afterValidate.Sub(afterRead).VmRSSKB)/1024, float64(afterValidate.Sub(afterRead).AnonRSSKB)/1024)
	logf("Δ_NewModule:    VmRSS=%+.2fMB Anon=%+.2fMB  ← compile / native artifact",
		float64(compileΔ.VmRSSKB)/1024, float64(compileΔ.AnonRSSKB)/1024)
	if lastAfterClose.VmRSSKB > 0 {
		logf("Δ_Module_Close: VmRSS=%+.2fMB Anon=%+.2fMB",
			float64(reclaimΔ.VmRSSKB)/1024, float64(reclaimΔ.AnonRSSKB)/1024)
		reclaimedKB := -reclaimΔ.VmRSSKB
		if reclaimedKB < 0 {
			reclaimedKB = 0
		}
		if reclaimedKB < compileΔ.VmRSSKB/10 && compileΔ.VmRSSKB > 1024 {
			logf("VERDICT_CLOSE: Module.Close did NOT return most compile RSS to kernel")
		} else if reclaimΔ.VmRSSKB < -compileΔ.VmRSSKB/2 {
			logf("VERDICT_CLOSE: Module.Close reclaimed substantial compile RSS")
		} else {
			logf("VERDICT_CLOSE: partial / noisy reclaim (see deltas)")
		}
	}
	logf("VERDICT_COMPILE: NewModule Host cost ≈ VmRSS %+0.2fMB / Anon %+0.2fMB for this wasm",
		float64(compileΔ.VmRSSKB)/1024, float64(compileΔ.AnonRSSKB)/1024)
	logf("log_file=%s", logPath)
	t.Logf("module compile probe written to %s", logPath)
}
