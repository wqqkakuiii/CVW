/*
Copyright (C) BABEC. All rights reserved.
SPDX-License-Identifier: Apache-2.0
*/

package wasmer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	logger2 "chainmaker.org/chainmaker/logger/v2"
	commonPb "chainmaker.org/chainmaker/pb-go/v2/common"
	wasmergo "chainmaker.org/chainmaker/vm-wasmer/v2/wasmer-go"
)

// TestCloseInstanceStepSmaps diffs /proc/smaps after each CloseInstance sub-step
// to locate which step fails to release WASM linear-memory VMAs (~4.5MB).
//
//	cd vm-wasmer/v2@v2.4.0
//	ln -sf ../resource_probes/tests/resource_probe_{test,close_steps}_test.go .
//	go test -run TestCloseInstanceStepSmaps -v -count=1 -timeout 30m
func TestCloseInstanceStepSmaps(t *testing.T) {
	wasmPath := "./testdata/fact-go.wasm"
	settle := 500 * time.Millisecond
	logPath := fmt.Sprintf("../resource_probes/logs/resource_probe_close_steps_%s.log",
		time.Now().Format("20060102_150405"))
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
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

	byteCode, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}
	contract := &commonPb.Contract{
		Name:        strings.TrimSuffix(filepath.Base(wasmPath), filepath.Ext(wasmPath)),
		Version:     "1.0.0",
		RuntimeType: commonPb.RuntimeType_WASMER,
	}
	logger := logger2.GetLogger("close_steps")

	logf("=== TestCloseInstanceStepSmaps ===")
	logf("time=%s wasm=%s settle=%v", time.Now().Format(time.RFC3339), wasmPath, settle)

	wait := func() { time.Sleep(settle) }
	snap := func(label string) (SmapsRollup, ResourceSnapshot) {
		wait()
		r, err := ReadSmapsRollup(label)
		if err != nil {
			t.Fatal(err)
		}
		proc := SampleResourceNoGC(label)
		logf("[%s] %s smaps_rss=%.2fMB smaps_anon=%.2fMB",
			label, proc.String(), float64(r.TotalRSSKB)/1024, float64(r.TotalAnonKB)/1024)
		return r, proc
	}
	logDiff := func(step string, after, before SmapsRollup, procAfter, procBefore ResourceSnapshot) {
		d := DiffSmaps(after, before)
		dp := procAfter.Sub(procBefore)
		logf("  STEP %s proc VmRSS=%+.2fMB Anon=%+.2fMB threads=%+d | smaps Δrss=%+.2fMB |%s|",
			step, float64(dp.VmRSSKB)/1024, float64(dp.AnonRSSKB)/1024, dp.Threads,
			float64(d.RSSKB)/1024, d.FormatCategories())
		for _, v := range d.TopVMA {
			if v.Category != "anon_rw_large" && v.Category != "anon_rw_small" {
				continue
			}
			if v.DeltaRSS > -512 && v.DeltaRSS < 512 {
				continue
			}
			logf("    [%s] Δrss=%+.2fMB after=%.2fMB | %s %s",
				v.Category, float64(v.DeltaRSS)/1024, float64(v.AfterRSS)/1024, v.Key, v.Path)
		}
	}

	cfg := wasmergo.NewConfig()
	cfg.MaxPagesLimit(512)
	cfg.CompilerNumThreads(4)
	cfg.PushMeteringMiddleware(1e19, map[wasmergo.Opcode]uint32{wasmergo.Opcode(0): 0}, map[string]uint32{}, "")
	engine := wasmergo.NewEngineWithConfig(cfg)
	store := wasmergo.NewStore(engine)
	module, err := wasmergo.NewModule(store, byteCode, logger)
	if err != nil {
		t.Fatal(err)
	}
	pool := &vmPool{
		contractId: contract, byteCode: byteCode,
		store: store, module: module, engine: engine,
		instances: make(chan *wrappedInstance, 2),
		log:       logger,
	}

	baseSmaps, baseProc := snap("0_after_module")
	inst, err := createInstanceWithProbe(logf, pool, NewResourceProbe("x"), contract.Name, "steps", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	afterCreateSmaps, afterCreateProc := snap("1_after_create")

	logf("")
	logf("--- create delta vs module ---")
	logDiff("CREATE", afterCreateSmaps, baseSmaps, afterCreateProc, baseProc)

	prevSmaps, prevProc := afterCreateSmaps, afterCreateProc

	// Step A: CallDeallocate only
	if inst.wasmInstance != nil {
		if err := CallDeallocate(inst.wasmInstance); err != nil {
			logf("CallDeallocate err: %v", err)
		}
	}
	smapsA, procA := snap("2_after_CallDeallocate")
	logDiff("CallDeallocate", smapsA, prevSmaps, procA, prevProc)
	prevSmaps, prevProc = smapsA, procA

	// Step B: Instance.Close (wasm_instance_delete)
	if inst.wasmInstance != nil {
		inst.wasmInstance.Close()
	}
	smapsB, procB := snap("3_after_InstanceClose")
	logDiff("Instance.Close", smapsB, prevSmaps, procB, prevProc)
	prevSmaps, prevProc = smapsB, procB

	// Step C: WasiEnv.Close
	if inst.wasiEnv != nil {
		inst.wasiEnv.Close()
		inst.wasiEnv = nil
	}
	smapsC, procC := snap("4_after_WasiClose")
	logDiff("WasiEnv.Close", smapsC, prevSmaps, procC, prevProc)
	prevSmaps, prevProc = smapsC, procC

	// Step D: Store.Close (wasm_store_delete + clear)
	if inst.store != nil {
		inst.store.Close()
		inst.store = nil
	}
	inst.wasmInstance = nil
	smapsD, procD := snap("5_after_StoreClose")
	logDiff("Store.Close", smapsD, prevSmaps, procD, prevProc)

	logf("")
	logf("--- total destroy delta vs after_create ---")
	logDiff("TOTAL_DESTROY", smapsD, afterCreateSmaps, procD, afterCreateProc)
	logf("--- net cycle delta vs module ---")
	logDiff("NET_CYCLE", smapsD, baseSmaps, procD, baseProc)
	logf("log_file=%s", logPath)
	t.Logf("close steps log: %s", logPath)
}
