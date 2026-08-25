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
	commonPb "chainmaker.org/chainmaker/pb-go/v2/common"
	wasmergo "chainmaker.org/chainmaker/vm-wasmer/v2/wasmer-go"
)

var (
	goEnhancedProbeWasm = flag.String("go_enhanced_probe_wasm", "./testdata/fact-go.wasm",
		"wasm for TestGoEnhancedClose10Steps")
	goEnhancedProbeN = flag.Int("go_enhanced_probe_n", 10,
		"instances to create then destroy step-by-step")
	goEnhancedProbeSettle = flag.Duration("go_enhanced_probe_settle", 300*time.Millisecond,
		"settle before each RSS sample")
	goEnhancedProbeLog = flag.String("go_enhanced_probe_log", "",
		"result log; empty => ./resource_probe_go_enhanced_<timestamp>.log")
)

type goEnhancedStepRow struct {
	Step       string
	Alive      int
	VmRSSMB    float64
	AnonMB     float64
	Threads    int
	DeltaRSSMB float64
	DeltaAnon  float64
	DeltaThr   int
}

// TestGoEnhancedClose10Steps creates N instances one-by-one, destroys one-by-one,
// then closes module → store → engine with Go-layer enhanced Close (nil refs + GC).
//
//	go test -run TestGoEnhancedClose10Steps -v -count=1 -timeout 20m
func TestGoEnhancedClose10Steps(t *testing.T) {
	logPath := *goEnhancedProbeLog
	if logPath == "" {
		logPath = fmt.Sprintf("./resource_probe_go_enhanced_%s.log", time.Now().Format("20060102_150405"))
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

	n := *goEnhancedProbeN
	if n < 1 {
		t.Fatal("go_enhanced_probe_n must be >= 1")
	}
	wasmPath := *goEnhancedProbeWasm

	logf("=== TestGoEnhancedClose10Steps (Go enhanced Close) ===")
	logf("time=%s wasm=%s n=%d settle=%v", time.Now().Format(time.RFC3339), wasmPath, n, *goEnhancedProbeSettle)
	logf("log_file=%s", logPath)
	logf("")

	rows, err := runGoEnhancedCloseSteps(logf, wasmPath, n)
	if err != nil {
		t.Fatal(err)
	}

	logf("=== MEMORY TABLE (absolute + Δ vs previous step) ===")
	logf("%-28s %5s %10s %10s %7s %10s %10s %6s",
		"step", "alive", "VmRSS_MB", "Anon_MB", "threads", "ΔRSS_MB", "ΔAnon_MB", "Δthr")
	for _, r := range rows {
		logf("%-28s %5d %10.2f %10.2f %7d %10.2f %10.2f %6d",
			r.Step, r.Alive, r.VmRSSMB, r.AnonMB, r.Threads,
			r.DeltaRSSMB, r.DeltaAnon, r.DeltaThr)
	}
	logf("")
	logf("log_file=%s", logPath)
	t.Logf("go enhanced close probe written to %s", logPath)
}

func runGoEnhancedCloseSteps(
	logf func(string, ...interface{}),
	wasmPath string,
	n int,
) ([]goEnhancedStepRow, error) {
	settle := func() {
		if *goEnhancedProbeSettle > 0 {
			time.Sleep(*goEnhancedProbeSettle)
		}
	}
	sample := func(label string) ResourceSnapshot {
		settle()
		return SampleResourceNoGC(label)
	}
	mb := func(kb int64) float64 { return float64(kb) / 1024 }

	byteCode, err := os.ReadFile(wasmPath)
	if err != nil {
		return nil, err
	}
	contractName := strings.TrimSuffix(filepath.Base(wasmPath), filepath.Ext(wasmPath))
	logger := logger2.GetLogger("go_enhanced_" + contractName)
	contract := &commonPb.Contract{
		Name:        contractName,
		Version:     "1.0.0",
		RuntimeType: commonPb.RuntimeType_WASMER,
	}

	var rows []goEnhancedStepRow
	var prev ResourceSnapshot

	record := func(step string, alive int, snap ResourceSnapshot) {
		d := snap.Sub(prev)
		row := goEnhancedStepRow{
			Step:       step,
			Alive:      alive,
			VmRSSMB:    mb(snap.VmRSSKB),
			AnonMB:     mb(snap.AnonRSSKB),
			Threads:    snap.Threads,
			DeltaRSSMB: mb(d.VmRSSKB),
			DeltaAnon:  mb(d.AnonRSSKB),
			DeltaThr:   d.Threads,
		}
		rows = append(rows, row)
		logf("%-28s alive=%d %s Δ %s", step, alive, snap.String(), d.String())
		prev = snap
	}

	prev = sample("0_baseline")
	record("0_baseline", 0, prev)

	config := wasmergo.NewConfig()
	config.MaxPagesLimit(512)
	config.CompilerNumThreads(4)
	config.PushMeteringMiddleware(1e19, map[wasmergo.Opcode]uint32{wasmergo.Opcode(0): 0}, map[string]uint32{}, "")
	engine := wasmergo.NewEngineWithConfig(config)
	store := wasmergo.NewStore(engine)
	snap := sample("1_engine_store")
	record("1_engine_store", 0, snap)

	module, err := wasmergo.NewModule(store, byteCode, logger)
	if err != nil {
		closeGoEnhanced(engine, store, nil)
		return rows, fmt.Errorf("NewModule: %w", err)
	}
	snap = sample("2_module")
	record("2_module", 0, snap)

	pool := &vmPool{
		contractId: contract,
		byteCode:   byteCode,
		store:      store,
		module:     module,
		instances:  make(chan *wrappedInstance, n+1),
		log:        logger,
	}

	var instances []*wrappedInstance
	for i := 0; i < n; i++ {
		inst, err := createInstanceWithProbe(logf, pool, NewResourceProbe("x"), contractName, "enh", i, false)
		if err != nil {
			for _, x := range instances {
				closeWrappedInstanceGoEnhanced(pool, x)
			}
			closeGoEnhanced(engine, store, module)
			return rows, fmt.Errorf("create inst #%d: %w", i, err)
		}
		instances = append(instances, inst)
		snap = sample(fmt.Sprintf("3_create_%02d", i+1))
		record(fmt.Sprintf("3_create_%02d", i+1), i+1, snap)
	}

	for i := len(instances) - 1; i >= 0; i-- {
		closeWrappedInstanceGoEnhanced(pool, instances[i])
		instances[i] = nil
		snap = sample(fmt.Sprintf("4_destroy_%02d_alive_%d", n-i, i))
		record(fmt.Sprintf("4_destroy_%02d", n-i), i, snap)
	}
	instances = nil

	snap = sample("5_all_instances_closed")
	record("5_all_instances_closed", 0, snap)

	module.Close()
	pool.module = nil
	snap = sample("6_module_close")
	record("6_module_close", 0, snap)

	store.Close()
	pool.store = nil
	snap = sample("7_store_close")
	record("7_store_close", 0, snap)

	engine.Close()
	snap = sample("8_engine_close")
	record("8_engine_close", 0, snap)

	runtime.GC()
	snap = sample("9_after_gc")
	record("9_after_gc", 0, snap)

	return rows, nil
}

// closeWrappedInstanceGoEnhanced uses enhanced Instance/Wasi Close + nil Go refs + GC.
func closeWrappedInstanceGoEnhanced(p *vmPool, inst *wrappedInstance) {
	closeWrappedInstanceFullRelease(p, inst)
	runtime.GC()
}

func closeGoEnhanced(engine *wasmergo.Engine, store *wasmergo.Store, module *wasmergo.Module) {
	if module != nil {
		module.Close()
	}
	if store != nil {
		store.Close()
	}
	if engine != nil {
		engine.Close()
	}
	runtime.GC()
}
