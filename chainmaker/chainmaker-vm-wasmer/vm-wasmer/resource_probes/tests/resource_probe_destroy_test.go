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
	destroyProbeWasm = flag.String("destroy_probe_wasm", "./testdata/fact-go.wasm",
		"single wasm for TestInstanceDestroyStepsAF")
	destroyProbeN = flag.Int("destroy_probe_n", 8,
		"instances per arm (A–F); keep modest to limit Tokio thread growth")
	destroyProbeSettle = flag.Duration("destroy_probe_settle", 300*time.Millisecond,
		"settle before each sample")
	destroyProbeLog = flag.String("destroy_probe_log", "",
		"result log; empty => ./resource_probe_destroy_<timestamp>.log")
	// RSS drop below this fraction of create-cost is treated as "did not reclaim".
	destroyProbeReclaimFrac = flag.Float64("destroy_probe_reclaim_frac", 0.15,
		"if |ΔRSS| after destroy < frac * createΔRSS, treat as not reclaimed")
)

type destroyStepSample struct {
	Name     string
	Before   ResourceSnapshot
	After    ResourceDelta // After.Sub(Before) stored via helper
	AfterAbs ResourceSnapshot
	CloseN   int // F: number of Instance.Close / CloseNativeOnly calls
	Note     string
}

// TestInstanceDestroyStepsAF isolates which Close step releases Host Instance RSS.
// wasiEnv is retained in a held slice (not deleted) so Tokio noise stays flat within an arm.
//
//	A peak after create N
//	B CallDeallocate only
//	C full Instance.Close (no recreate)
//	D Close without CallDeallocate (fresh N)
//	E Exports.Close only (fresh N)
//	F Close count == N and wasmInstance nil
//
//	go test -run TestInstanceDestroyStepsAF -v -count=1 -timeout 30m \
//	  -args -destroy_probe_n=8 -destroy_probe_wasm=./testdata/fact-go.wasm
func TestInstanceDestroyStepsAF(t *testing.T) {
	logPath := *destroyProbeLog
	if logPath == "" {
		logPath = fmt.Sprintf("./resource_probe_destroy_%s.log", time.Now().Format("20060102_150405"))
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

	n := *destroyProbeN
	if n < 1 {
		t.Fatal("destroy_probe_n must be >= 1")
	}
	wasmPath := *destroyProbeWasm
	contractName := strings.TrimSuffix(filepath.Base(wasmPath), filepath.Ext(wasmPath))

	logf("=== TestInstanceDestroyStepsAF ===")
	logf("time=%s", time.Now().Format(time.RFC3339))
	logf("log_file=%s", logPath)
	logf("wasm=%s n=%d settle=%v reclaim_frac=%.2f", wasmPath, n, *destroyProbeSettle, *destroyProbeReclaimFrac)
	logf("note: wasiEnv held alive (no wasi_env_delete); focus = Instance / linear memory RSS")
	logf("")

	pool, cleanup, err := newDestroyProbePool(wasmPath, contractName, n)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer cleanup()

	var heldWasi []*wasmergo.WasiEnvironment // keep Tokio alive; ignore for verdict
	defer func() {
		// Drop refs at end; finalizers may run — outside measured arms.
		heldWasi = nil
		runtime.GC()
	}()

	var samples []destroyStepSample
	record := func(name string, before, after ResourceSnapshot, closeN int, note string) {
		d := after.Sub(before)
		samples = append(samples, destroyStepSample{
			Name: name, Before: before, After: d, AfterAbs: after, CloseN: closeN, Note: note,
		})
		logf("--- %s ---", name)
		logf("  before: %s", before.String())
		logf("  after:  %s", after.String())
		logf("  %s", d.String())
		if note != "" {
			logf("  note: %s", note)
		}
	}

	settle := func() {
		if *destroyProbeSettle > 0 {
			time.Sleep(*destroyProbeSettle)
		}
	}

	// ----- Arm A+B+C on one batch (create → dealloc → close, no recreate) -----
	logf("===== arm A/B/C: create → dealloc-only → full Close =====")
	batch, err := createDestroyBatch(pool, contractName, "abc", n)
	if err != nil {
		t.Fatalf("create abc: %v", err)
	}
	for _, inst := range batch {
		heldWasi = append(heldWasi, inst.wasiEnv)
		inst.wasiEnv = nil // detach so we never release/delete during arms
	}
	settle()
	aPeak := SampleResource("A_peak_alive")
	logf("%s alive=%d", aPeak.String(), len(batch))
	// synthetic "before create" approx: use module-ready sample from pool setup — we use A as peak only
	aBeforeCreate := SampleResourceNoGC("A_ref_before_measure") // same process; delta B/C vs APeak

	// B: deallocate only
	for _, inst := range batch {
		if err := CallDeallocate(inst.wasmInstance); err != nil {
			logf("  CallDeallocate warn: %v", err)
		}
	}
	settle()
	bAfter := SampleResource("B_after_deallocate_only")
	record("B_deallocate_only", aPeak, bAfter, 0, "expect ~0 RSS drop (Guest args only)")

	// C: full Instance.Close, no NewInstance
	closeN := 0
	for i, inst := range batch {
		if inst.wasmInstance != nil {
			inst.wasmInstance.Close()
			inst.wasmInstance = nil
			closeN++
		}
		batch[i] = nil
	}
	settle()
	runtime.GC()
	settle()
	cAfter := SampleResource("C_after_full_Close")
	record("C_full_Close_no_recreate", aPeak, cAfter, closeN,
		"expect reclaim if wasm_instance_delete returns pages; compare |Δ| to create cost")

	_ = aBeforeCreate

	// ----- Arm D: Close without deallocate -----
	logf("===== arm D: create → Close (skip deallocate) =====")
	batchD, err := createDestroyBatch(pool, contractName, "d", n)
	if err != nil {
		t.Fatalf("create d: %v", err)
	}
	for _, inst := range batchD {
		heldWasi = append(heldWasi, inst.wasiEnv)
		inst.wasiEnv = nil
	}
	settle()
	dPeak := SampleResource("D_peak_alive")
	closeN = 0
	for i, inst := range batchD {
		if inst.wasmInstance != nil {
			inst.wasmInstance.Close()
			inst.wasmInstance = nil
			closeN++
		}
		batchD[i] = nil
	}
	settle()
	runtime.GC()
	settle()
	dAfter := SampleResource("D_after_Close_no_dealloc")
	record("D_Close_skip_deallocate", dPeak, dAfter, closeN, "compare to C; should be similar if dealloc irrelevant to Host RSS")

	// ----- Arm E: Exports.Close only -----
	logf("===== arm E: create → Exports.Close only =====")
	batchE, err := createDestroyBatch(pool, contractName, "e", n)
	if err != nil {
		t.Fatalf("create e: %v", err)
	}
	for _, inst := range batchE {
		heldWasi = append(heldWasi, inst.wasiEnv)
		inst.wasiEnv = nil
	}
	settle()
	ePeak := SampleResource("E_peak_alive")
	for _, inst := range batchE {
		if inst.wasmInstance != nil && inst.wasmInstance.Exports != nil {
			inst.wasmInstance.Exports.Close()
		}
	}
	settle()
	eAfter := SampleResource("E_after_Exports_Close_only")
	record("E_Exports_Close_only", ePeak, eAfter, 0, "expect ~0 RSS drop (exports vec only)")
	// cleanup without double vec_delete
	closeN = 0
	for i, inst := range batchE {
		if inst.wasmInstance != nil {
			inst.wasmInstance.CloseNativeOnly()
			inst.wasmInstance = nil
			closeN++
		}
		batchE[i] = nil
	}
	settle()
	logf("  E cleanup CloseNativeOnly count=%d (not part of E delta)", closeN)

	// ----- Arm F: Close count integrity -----
	logf("===== arm F: Close count == N =====")
	batchF, err := createDestroyBatch(pool, contractName, "f", n)
	if err != nil {
		t.Fatalf("create f: %v", err)
	}
	for _, inst := range batchF {
		heldWasi = append(heldWasi, inst.wasiEnv)
		inst.wasiEnv = nil
	}
	settle()
	fPeak := SampleResource("F_peak_alive")
	closeN = 0
	nilOK := true
	for i, inst := range batchF {
		if inst.wasmInstance != nil {
			inst.wasmInstance.Close()
			inst.wasmInstance = nil
			closeN++
		}
		if inst.wasmInstance != nil {
			nilOK = false
		}
		batchF[i] = nil
	}
	settle()
	fAfter := SampleResource("F_after_Close")
	note := fmt.Sprintf("closeN=%d want=%d wasmInstance_nil=%v", closeN, n, nilOK)
	record("F_close_count", fPeak, fAfter, closeN, note)
	if closeN != n || !nilOK {
		t.Errorf("F failed: %s", note)
	}

	// ----- Analysis -----
	logf("")
	logf("=== ANALYSIS ===")
	// Estimate create cost from A peak vs C after (same batch): createCost ≈ -ΔC if C reclaimed, else use D peak-after or A-C gap
	// Better: use growth during first create — we didn't sample pre-create cleanly.
	// Use |ΔRSS| of (Apeak - C_after) as "reclaimed amount"; createCost ≈ max(Apeak.RSS - baselineModule, 1)
	// Pool setup left module in process; use first peak after create relative to C after as unreclaimed remainder.

	analyzeDestroySteps(logf, samples, n, *destroyProbeReclaimFrac)

	logf("log_file=%s", logPath)
	t.Logf("destroy-step results written to %s", logPath)
}

func analyzeDestroySteps(logf func(string, ...interface{}), samples []destroyStepSample, n int, reclaimFrac float64) {
	byName := map[string]destroyStepSample{}
	for _, s := range samples {
		byName[s.Name] = s
	}

	mb := func(kb int64) float64 { return float64(kb) / 1024 }

	// Create cost proxy: RSS at A_peak vs after C (same instances). If C reclaimed fully,
	// createCost ≈ -deltaC; unreclaimed = peak - after still high.
	c, hasC := byName["C_full_Close_no_recreate"]
	b, hasB := byName["B_deallocate_only"]
	d, hasD := byName["D_Close_skip_deallocate"]
	e, hasE := byName["E_Exports_Close_only"]
	f, hasF := byName["F_close_count"]

	var createCostRSS, createCostAnon float64
	if hasC {
		// Peak was C.Before; after close C.AfterAbs. Cost of holding N ≈ peak - min(after,peak) wait
		// createCost ≈ RSS added by instances ≈ we use max(0, Before.RSS - AfterAbs.RSS) as reclaimed
		// and Before relative — unreclaimedFraction = AfterAbs / Before if before was mostly instances
		createCostRSS = mb(c.Before.VmRSSKB - c.AfterAbs.VmRSSKB) // reclaimed (may be ~0 or negative)
		if createCostRSS < 0 {
			createCostRSS = 0
		}
		createCostAnon = mb(c.Before.AnonRSSKB - c.AfterAbs.AnonRSSKB)
		if createCostAnon < 0 {
			createCostAnon = 0
		}
		heldRSS := mb(c.Before.VmRSSKB)
		afterRSS := mb(c.AfterAbs.VmRSSKB)
		reclaimed := mb(c.Before.VmRSSKB - c.AfterAbs.VmRSSKB)
		logf("C: peak_VmRSS=%.2fMB after_Close=%.2fMB reclaimed=%.2fMB (Anon peak=%.2f after=%.2f reclaimed=%.2f)",
			heldRSS, afterRSS, reclaimed, mb(c.Before.AnonRSSKB), mb(c.AfterAbs.AnonRSSKB), createCostAnon)
		logf("C: close_calls=%d", c.CloseN)

		// Heuristic: if reclaimed < frac * (typical ~4MB * n) use absolute threshold 1MB * n * frac
		expectMin := float64(n) * 2.0 * reclaimFrac // at least ~2MB/instance * frac
		if reclaimed < expectMin && mb(c.Before.VmRSSKB-c.AfterAbs.VmRSSKB) < expectMin {
			logf("VERDICT_C: Host RSS NOT reclaimed after wasm_instance_delete (reclaimed=%.2fMB < expectMin=%.2fMB)",
				reclaimed, expectMin)
			logf("  → bottleneck is after delete (allocator/Wasmer), not a missing Go Close step")
		} else if reclaimed >= expectMin {
			logf("VERDICT_C: meaningful RSS drop after full Close (reclaimed=%.2fMB)", reclaimed)
		} else {
			logf("VERDICT_C: ambiguous (reclaimed=%.2fMB); check Anon and smaps", reclaimed)
		}
	}

	if hasB {
		br := mb(b.After.VmRSSKB)
		ba := mb(b.After.AnonRSSKB)
		logf("B: ΔVmRSS=%+.2fMB ΔAnon=%+.2fMB", br, ba)
		if abs64(b.After.VmRSSKB) < 512 { // <0.5MB
			logf("VERDICT_B: deallocate does not free Host RSS (as expected)")
		} else {
			logf("VERDICT_B: unexpected RSS move on deallocate-only")
		}
	}

	if hasD && hasC {
		cr := mb(c.Before.VmRSSKB - c.AfterAbs.VmRSSKB)
		dr := mb(d.Before.VmRSSKB - d.AfterAbs.VmRSSKB)
		logf("D vs C reclaimed: D=%.2fMB C=%.2fMB", dr, cr)
		if absFloat(cr-dr) < 2.0 {
			logf("VERDICT_D: skip deallocate ≈ full Close → CallDeallocate not the Host leak point")
		} else {
			logf("VERDICT_D: D and C differ; inspect deallocate side effects")
		}
	}

	if hasE {
		er := mb(e.After.VmRSSKB)
		logf("E: ΔVmRSS=%+.2fMB ΔAnon=%+.2fMB", er, mb(e.After.AnonRSSKB))
		if abs64(e.After.VmRSSKB) < 512 {
			logf("VERDICT_E: Exports.Close alone does not free Instance linear memory (as expected)")
		} else {
			logf("VERDICT_E: unexpected RSS move on Exports.Close-only")
		}
	}

	if hasF {
		logf("F: %s closeN=%d", f.Note, f.CloseN)
		if f.CloseN == n {
			logf("VERDICT_F: Close count matches N — Go path invokes delete N times")
		} else {
			logf("VERDICT_F: Close count mismatch — check lifecycle bugs")
		}
	}

	logf("SUMMARY:")
	logf("  Host Instance pages are released (logically) only in Instance.Close → wasm_instance_delete.")
	logf("  If C shows little/no VmRSS/Anon drop while F confirms N deletes, pages stay in native allocator.")
	logf("  B/E ~flat confirms CallDeallocate / Exports.Close are not the reclaim step.")
	_ = createCostRSS
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func newDestroyProbePool(wasmPath, contractName string, n int) (*vmPool, func(), error) {
	byteCode, err := os.ReadFile(wasmPath)
	if err != nil {
		return nil, nil, err
	}
	logger := logger2.GetLogger("destroy_probe_" + contractName)
	contract := &commonPb.Contract{
		Name:        contractName,
		Version:     "1.0.0",
		RuntimeType: commonPb.RuntimeType_WASMER,
	}
	config := wasmergo.NewConfig()
	config.MaxPagesLimit(512)
	config.PushMeteringMiddleware(1e19, map[wasmergo.Opcode]uint32{wasmergo.Opcode(0): 0}, map[string]uint32{}, "")
	engine := wasmergo.NewEngineWithConfig(config)
	store := wasmergo.NewStore(engine)
	if err := wasmergo.ValidateModule(store, byteCode); err != nil {
		return nil, nil, err
	}
	module, err := wasmergo.NewModule(store, byteCode, logger)
	if err != nil {
		return nil, nil, err
	}
	pool := &vmPool{
		contractId: contract,
		byteCode:   byteCode,
		store:      store,
		module:     module,
		instances:  make(chan *wrappedInstance, n+1),
		log:        logger,
	}
	cleanup := func() {
		module.Close()
		store.Close()
	}
	return pool, cleanup, nil
}

func createDestroyBatch(pool *vmPool, contractName, tag string, n int) ([]*wrappedInstance, error) {
	out := make([]*wrappedInstance, 0, n)
	for i := 0; i < n; i++ {
		inst, err := createInstanceWithProbe(func(string, ...interface{}) {}, pool, NewResourceProbe("x"), contractName, tag, i, false)
		if err != nil {
			for _, x := range out {
				if x.wasmInstance != nil {
					x.wasmInstance.Close()
				}
			}
			return nil, err
		}
		out = append(out, inst)
	}
	return out, nil
}
