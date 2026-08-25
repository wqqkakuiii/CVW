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
	"strings"
	"testing"
	"time"

	logger2 "chainmaker.org/chainmaker/logger/v2"
	commonPb "chainmaker.org/chainmaker/pb-go/v2/common"
	wasmergo "chainmaker.org/chainmaker/vm-wasmer/v2/wasmer-go"
)

var (
	storeReleaseProbeWasm = flag.String("store_release_probe_wasm", "./testdata/fact-go.wasm",
		"wasm for store-ref release grow/shrink probe")
	storeReleaseProbeN = flag.Int("store_release_probe_n", 8,
		"instances to create per grow phase")
	storeReleaseProbeCycles = flag.Int("store_release_probe_cycles", 1,
		"grow→shrink cycles per arm")
	storeReleaseProbeSettle = flag.Duration("store_release_probe_settle", 300*time.Millisecond,
		"settle before each RSS sample")
	storeReleaseProbeLog = flag.String("store_release_probe_log", "",
		"result log; empty => ./resource_probe_store_release_<timestamp>.log")
	storeReleaseProbeArm = flag.String("store_release_probe_arm", "",
		"arm for TestStoreReleaseGrowShrinkSingle: basic | full_release")
	storeReleaseProbeResultFile = flag.String("store_release_probe_result_file", "",
		"CSV one-liner output for isolated worker")
	storeReleaseProbeReclaimFrac = flag.Float64("store_release_probe_reclaim_frac", 0.15,
		"|ΔRSS| after shrink < frac * growΔ → treat as not reclaimed")
)

type storeRefAudit struct {
	WasmInstanceNil bool
	WasiEnvNil      bool
	AliveSliceLen   int
}

func (a storeRefAudit) String() string {
	return fmt.Sprintf("wasmInstance_nil=%v wasiEnv_nil=%v alive_slice_len=%d",
		a.WasmInstanceNil, a.WasiEnvNil, a.AliveSliceLen)
}

func auditWrappedInstance(inst *wrappedInstance, aliveLen int) storeRefAudit {
	a := storeRefAudit{AliveSliceLen: aliveLen}
	if inst == nil {
		a.WasmInstanceNil = true
		a.WasiEnvNil = true
		return a
	}
	a.WasmInstanceNil = inst.wasmInstance == nil
	a.WasiEnvNil = inst.wasiEnv == nil
	return a
}

// closeWrappedInstanceBasic mirrors production CloseInstance (no extra GC).
func closeWrappedInstanceBasic(p *vmPool, inst *wrappedInstance) {
	p.CloseInstance(inst)
}

// closeWrappedInstanceFullRelease closes native objects and clears all Go-side
// references to store-managed exports/imports/instance so Rust Drop can run.
func closeWrappedInstanceFullRelease(p *vmPool, inst *wrappedInstance) {
	if inst == nil {
		return
	}
	if inst.wasmInstance != nil {
		if err := CallDeallocate(inst.wasmInstance); err != nil {
			p.log.Errorf("CallDeallocate: %v", err)
		}
		inst.wasmInstance.Close()
		inst.wasmInstance = nil
	}
	if inst.wasiEnv != nil {
		inst.wasiEnv.Close()
		inst.wasiEnv = nil
	}
	inst.id = ""
	inst.lastUseTime = 0
	inst.createTime = 0
	inst.errCount = 0
}

type storeReleaseRow struct {
	Arm          string
	N            int
	GrowRSSMB    float64
	GrowAnonMB   float64
	ShrinkRSSMB  float64 // negative = reclaimed
	ShrinkAnonMB float64
	Reclaimed    bool
	RefAuditOK   bool
}

func (r storeReleaseRow) csvLine() string {
	return fmt.Sprintf("%s,%d,%.2f,%.2f,%.2f,%.2f,%v,%v",
		r.Arm, r.N, r.GrowRSSMB, r.GrowAnonMB, r.ShrinkRSSMB, r.ShrinkAnonMB, r.Reclaimed, r.RefAuditOK)
}

// TestStoreReleaseGrowShrinkSingle runs one arm in a fresh process (isolated worker).
func TestStoreReleaseGrowShrinkSingle(t *testing.T) {
	arm := strings.TrimSpace(*storeReleaseProbeArm)
	if arm != "basic" && arm != "full_release" {
		t.Fatalf("store_release_probe_arm must be basic or full_release, got %q", arm)
	}
	n := *storeReleaseProbeN
	cycles := *storeReleaseProbeCycles
	if n < 1 || cycles < 1 {
		t.Fatal("store_release_probe_n and store_release_probe_cycles must be >= 1")
	}

	w := io.MultiWriter(os.Stdout, probeTestWriter{t})
	logf := func(format string, args ...interface{}) {
		line := fmt.Sprintf(format, args...)
		if !strings.HasSuffix(line, "\n") {
			line += "\n"
		}
		_, _ = io.WriteString(w, line)
	}

	row, err := runStoreReleaseGrowShrinkArm(logf, *storeReleaseProbeWasm, arm, n, cycles)
	if err != nil {
		t.Fatal(err)
	}
	resultPath := *storeReleaseProbeResultFile
	if resultPath == "" {
		t.Fatal("store_release_probe_result_file required for TestStoreReleaseGrowShrinkSingle")
	}
	if err := os.WriteFile(resultPath, []byte(row.csvLine()+"\n"), 0o644); err != nil {
		t.Fatalf("write result: %v", err)
	}
	t.Logf("RESULT %s", row.csvLine())
}

// TestStoreReleaseGrowShrinkIsolated compares basic vs full_release in fresh processes.
//
//	go test -run TestStoreReleaseGrowShrinkIsolated -v -count=1 -timeout 30m
func TestStoreReleaseGrowShrinkIsolated(t *testing.T) {
	logPath := *storeReleaseProbeLog
	if logPath == "" {
		logPath = fmt.Sprintf("./resource_probe_store_release_%s.log", time.Now().Format("20060102_150405"))
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

	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir, err := os.MkdirTemp("", "store_release_probe_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	n := *storeReleaseProbeN
	cycles := *storeReleaseProbeCycles
	wasmPath := *storeReleaseProbeWasm

	logf("=== TestStoreReleaseGrowShrinkIsolated ===")
	logf("time=%s", time.Now().Format(time.RFC3339))
	logf("log_file=%s", logPath)
	logf("wasm=%s n=%d cycles=%d settle=%v reclaim_frac=%.2f",
		wasmPath, n, cycles, *storeReleaseProbeSettle, *storeReleaseProbeReclaimFrac)
	logf("arms=basic (CloseInstance) | full_release (nil Go refs + GC)")
	logf("")

	arms := []string{"basic", "full_release"}
	rows := make([]storeReleaseRow, 0, len(arms))
	for _, arm := range arms {
		resultFile := filepath.Join(tmpDir, arm+".csv")
		cmd := exec.Command("go", "test",
			"-run", "^TestStoreReleaseGrowShrinkSingle$",
			"-count=1", "-timeout", "20m",
			"-args",
			"-store_release_probe_arm="+arm,
			"-store_release_probe_wasm="+wasmPath,
			fmt.Sprintf("-store_release_probe_n=%d", n),
			fmt.Sprintf("-store_release_probe_cycles=%d", cycles),
			fmt.Sprintf("-store_release_probe_settle=%v", *storeReleaseProbeSettle),
			"-store_release_probe_result_file="+resultFile,
			fmt.Sprintf("-store_release_probe_reclaim_frac=%g", *storeReleaseProbeReclaimFrac),
		)
		cmd.Dir = pkgDir
		cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
		out, err := cmd.CombinedOutput()
		logf("----- subprocess arm=%s -----", arm)
		logf("%s", string(out))
		if err != nil {
			t.Errorf("arm %s: %v", arm, err)
			continue
		}
		data, err := os.ReadFile(resultFile)
		if err != nil {
			t.Errorf("read result %s: %v", resultFile, err)
			continue
		}
		parts := strings.Split(strings.TrimSpace(string(data)), ",")
		if len(parts) < 8 {
			t.Errorf("bad result line: %q", string(data))
			continue
		}
		row := storeReleaseRow{
			Arm: parts[0],
			N:   n,
		}
		fmt.Sscanf(parts[2], "%f", &row.GrowRSSMB)
		fmt.Sscanf(parts[3], "%f", &row.GrowAnonMB)
		fmt.Sscanf(parts[4], "%f", &row.ShrinkRSSMB)
		fmt.Sscanf(parts[5], "%f", &row.ShrinkAnonMB)
		row.Reclaimed = parts[6] == "true"
		row.RefAuditOK = parts[7] == "true"
		rows = append(rows, row)
		logf("RESULT arm=%s grow_RSS=%+.2fMB shrink_RSS=%+.2fMB reclaimed=%v refs_ok=%v",
			row.Arm, row.GrowRSSMB, row.ShrinkRSSMB, row.Reclaimed, row.RefAuditOK)
	}

	logf("")
	logf("=== SUMMARY ===")
	logf("%-14s %4s %10s %10s %12s %12s %10s %8s",
		"arm", "N", "grow_RSS", "grow_Anon", "shrink_RSS", "shrink_Anon", "reclaimed", "refs_ok")
	for _, r := range rows {
		logf("%-14s %4d %10.2f %10.2f %12.2f %12.2f %10v %8v",
			r.Arm, r.N, r.GrowRSSMB, r.GrowAnonMB, r.ShrinkRSSMB, r.ShrinkAnonMB, r.Reclaimed, r.RefAuditOK)
	}
	logf("log_file=%s", logPath)
}

func runStoreReleaseGrowShrinkArm(
	logf func(string, ...interface{}),
	wasmPath, arm string,
	n, cycles int,
) (storeReleaseRow, error) {
	row := storeReleaseRow{Arm: arm, N: n}
	settle := func() {
		if *storeReleaseProbeSettle > 0 {
			time.Sleep(*storeReleaseProbeSettle)
		}
	}
	sample := func(label string) ResourceSnapshot {
		settle()
		return SampleResourceNoGC(label)
	}
	mb := func(kb int64) float64 { return float64(kb) / 1024 }

	byteCode, err := os.ReadFile(wasmPath)
	if err != nil {
		return row, err
	}
	contractName := strings.TrimSuffix(filepath.Base(wasmPath), filepath.Ext(wasmPath))
	logger := logger2.GetLogger("store_release_" + contractName)
	contract := &commonPb.Contract{
		Name:        contractName,
		Version:     "1.0.0",
		RuntimeType: commonPb.RuntimeType_WASMER,
	}

	logf("=== arm=%s wasm=%s n=%d cycles=%d ===", arm, wasmPath, n, cycles)

	base := sample("0_baseline")
	logf("%s", base.String())

	config := wasmergo.NewConfig()
	config.MaxPagesLimit(512)
	config.CompilerNumThreads(4)
	config.PushMeteringMiddleware(1e19, map[wasmergo.Opcode]uint32{wasmergo.Opcode(0): 0}, map[string]uint32{}, "")
	engine := wasmergo.NewEngineWithConfig(config)
	store := wasmergo.NewStore(engine)
	afterStore := sample("1_after_engine_store")
	logf("%s  %s", afterStore.String(), afterStore.Sub(base).String())

	module, err := wasmergo.NewModule(store, byteCode, logger)
	if err != nil {
		store.Close()
		return row, fmt.Errorf("NewModule: %w", err)
	}
	afterModule := sample("2_after_NewModule")
	logf("%s  %s", afterModule.String(), afterModule.Sub(afterStore).String())

	pool := &vmPool{
		contractId: contract,
		byteCode:   byteCode,
		store:      store,
		module:     module,
		instances:  make(chan *wrappedInstance, n+1),
		log:        logger,
	}

	var peak ResourceSnapshot
	var shrinkZero ResourceSnapshot
	refOK := true

	for c := 1; c <= cycles; c++ {
		cycle := fmt.Sprintf("c%d", c)
		logf("--- %s GROW %d ---", cycle, n)
		beforeGrow := afterModule
		var instances []*wrappedInstance

		for i := 0; i < n; i++ {
			inst, err := createInstanceWithProbe(logf, pool, NewResourceProbe("x"), contractName, cycle, i, false)
			if err != nil {
				for _, x := range instances {
					closeWrappedInstanceFullRelease(pool, x)
				}
				module.Close()
				store.Close()
				return row, fmt.Errorf("%s create #%d: %w", cycle, i, err)
			}
			instances = append(instances, inst)
			after := sample(fmt.Sprintf("%s_grow_%d", cycle, i))
			logf("  inst#%d %s audit=%s", i, after.Sub(beforeGrow).String(), auditWrappedInstance(inst, len(instances)).String())
		}

		peak = sample(fmt.Sprintf("%s_peak_%d", cycle, n))
		growΔ := peak.Sub(beforeGrow)
		row.GrowRSSMB = mb(growΔ.VmRSSKB)
		row.GrowAnonMB = mb(growΔ.AnonRSSKB)
		logf("PEAK %s growΔ %s (RSS=%+.2fMB Anon=%+.2fMB threads=%+d)",
			peak.String(), growΔ.String(), row.GrowRSSMB, row.GrowAnonMB, growΔ.Threads)

		logf("--- %s SHRINK to 0 (arm=%s) ---", cycle, arm)
		for i := len(instances) - 1; i >= 0; i-- {
			inst := instances[i]
			if arm == "full_release" {
				closeWrappedInstanceFullRelease(pool, inst)
				instances[i] = nil
				runtime.GC()
			} else {
				closeWrappedInstanceBasic(pool, inst)
				instances[i] = nil
			}
			after := sample(fmt.Sprintf("%s_shrink_alive_%d", cycle, i))
			audit := auditWrappedInstance(inst, i)
			logf("  closed alive=%d %s audit=%s", i, after.Sub(peak).String(), audit.String())
			if !audit.WasmInstanceNil || !audit.WasiEnvNil {
				refOK = false
			}
		}
		instances = nil
		runtime.GC()
		settle()
		shrinkZero = sample(fmt.Sprintf("%s_shrink_zero", cycle))
		shrinkΔ := shrinkZero.Sub(peak)
		row.ShrinkRSSMB = mb(shrinkΔ.VmRSSKB)
		row.ShrinkAnonMB = mb(shrinkΔ.AnonRSSKB)
		logf("SHRINK_ZERO %s shrinkΔ %s (RSS=%+.2fMB Anon=%+.2fMB threads=%+d)",
			shrinkZero.String(), shrinkΔ.String(), row.ShrinkRSSMB, row.ShrinkAnonMB, shrinkΔ.Threads)
	}

	reclaimThreshold := *storeReleaseProbeReclaimFrac * row.GrowRSSMB
	if row.GrowRSSMB <= 0 {
		row.Reclaimed = true
	} else {
		row.Reclaimed = (-row.ShrinkRSSMB) >= reclaimThreshold
	}
	row.RefAuditOK = refOK

	logf("VERDICT arm=%s grow=%+.2fMB shrink=%+.2fMB reclaimed=%v refs_ok=%v (threshold=%.2fMB)",
		arm, row.GrowRSSMB, row.ShrinkRSSMB, row.Reclaimed, row.RefAuditOK, reclaimThreshold)

	module.Close()
	store.Close()
	runtime.GC()
	settle()
	afterTeardown := sample("3_after_module_store_engine_close")
	logf("TEARDOWN %s vs shrink_zero %s",
		afterTeardown.String(), afterTeardown.Sub(shrinkZero).String())

	return row, nil
}
