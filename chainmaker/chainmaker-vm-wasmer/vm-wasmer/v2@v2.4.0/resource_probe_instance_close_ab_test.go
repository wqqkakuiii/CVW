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
	instCloseABWasm = flag.String("inst_close_ab_wasm", "./testdata/fact-go.wasm",
		"wasm for Instance.Close A/B probe")
	instCloseABN = flag.Int("inst_close_ab_n", 10,
		"instances to create then destroy")
	instCloseABSettle = flag.Duration("inst_close_ab_settle", 300*time.Millisecond,
		"settle before each RSS sample")
	instCloseABLog = flag.String("inst_close_ab_log", "",
		"result log; empty => ./resource_probe_instance_close_ab_<timestamp>.log")
	instCloseABArm = flag.String("inst_close_ab_arm", "",
		"arm for worker: legacy | enhanced")
	instCloseABResultFile = flag.String("inst_close_ab_result_file", "",
		"CSV one-liner for isolated worker")
)

type instCloseABRow struct {
	Arm           string
	N             int
	PeakRSSMB     float64
	AfterShrinkMB float64
	ReclaimRSSMB  float64 // peak - after (positive = reclaimed)
	PeakAnonMB    float64
	AfterAnonMB   float64
	ReclaimAnonMB float64
	PeakThreads   int
	AfterThreads  int
}

func (r instCloseABRow) csvLine() string {
	return fmt.Sprintf("%s,%d,%.2f,%.2f,%.2f,%.2f,%.2f,%.2f,%d,%d",
		r.Arm, r.N, r.PeakRSSMB, r.AfterShrinkMB, r.ReclaimRSSMB,
		r.PeakAnonMB, r.AfterAnonMB, r.ReclaimAnonMB, r.PeakThreads, r.AfterThreads)
}

// closeInstanceArmLegacy: production CloseInstance shape, but Instance.CloseLegacy only.
func closeInstanceArmLegacy(p *vmPool, inst *wrappedInstance) {
	if inst == nil {
		return
	}
	if inst.wasmInstance != nil {
		if err := CallDeallocate(inst.wasmInstance); err != nil {
			p.log.Errorf("CallDeallocate: %v", err)
		}
		inst.wasmInstance.CloseLegacy()
		inst.wasmInstance = nil
	}
	if inst.wasiEnv != nil {
		inst.wasiEnv.Close()
		inst.wasiEnv = nil
	}
}

// closeInstanceArmEnhanced: production CloseInstance shape, but Instance.Close (enhanced).
func closeInstanceArmEnhanced(p *vmPool, inst *wrappedInstance) {
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
}

// TestInstanceCloseABIsolated compares ONLY Instance.Close vs CloseLegacy.
//
//	go test -run TestInstanceCloseABIsolated -v -count=1 -timeout 20m
func TestInstanceCloseABIsolated(t *testing.T) {
	logPath := *instCloseABLog
	if logPath == "" {
		logPath = fmt.Sprintf("./resource_probe_instance_close_ab_%s.log", time.Now().Format("20060102_150405"))
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
	tmpDir, err := os.MkdirTemp("", "inst_close_ab_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	n := *instCloseABN
	wasmPath := *instCloseABWasm
	logf("=== TestInstanceCloseABIsolated ===")
	logf("time=%s", time.Now().Format(time.RFC3339))
	logf("log_file=%s", logPath)
	logf("wasm=%s n=%d settle=%v", wasmPath, n, *instCloseABSettle)
	logf("ONLY variable: Instance.CloseLegacy vs Instance.Close (enhanced)")
	logf("identical: CallDeallocate, WasiEnv.Close, Module/Store/Engine kept alive until after measure")
	logf("")

	arms := []string{"legacy", "enhanced"}
	rows := make([]instCloseABRow, 0, 2)
	for _, arm := range arms {
		resultFile := filepath.Join(tmpDir, arm+".csv")
		cmd := exec.Command("go", "test",
			"-run", "^TestInstanceCloseABSingle$",
			"-count=1", "-timeout", "15m",
			"-args",
			"-inst_close_ab_arm="+arm,
			"-inst_close_ab_wasm="+wasmPath,
			fmt.Sprintf("-inst_close_ab_n=%d", n),
			fmt.Sprintf("-inst_close_ab_settle=%s", (*instCloseABSettle).String()),
			"-inst_close_ab_result_file="+resultFile,
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
		if len(parts) < 10 {
			t.Errorf("bad result: %q", string(data))
			continue
		}
		row := instCloseABRow{Arm: parts[0], N: n}
		fmt.Sscanf(parts[2], "%f", &row.PeakRSSMB)
		fmt.Sscanf(parts[3], "%f", &row.AfterShrinkMB)
		fmt.Sscanf(parts[4], "%f", &row.ReclaimRSSMB)
		fmt.Sscanf(parts[5], "%f", &row.PeakAnonMB)
		fmt.Sscanf(parts[6], "%f", &row.AfterAnonMB)
		fmt.Sscanf(parts[7], "%f", &row.ReclaimAnonMB)
		fmt.Sscanf(parts[8], "%d", &row.PeakThreads)
		fmt.Sscanf(parts[9], "%d", &row.AfterThreads)
		rows = append(rows, row)
		logf("RESULT arm=%s peak=%.2f after=%.2f reclaim=%.2fMB threads %d→%d",
			row.Arm, row.PeakRSSMB, row.AfterShrinkMB, row.ReclaimRSSMB, row.PeakThreads, row.AfterThreads)
	}

	logf("")
	logf("=== COMPARISON (only Instance.Close differs) ===")
	logf("%-10s %4s %10s %12s %12s %10s %12s %8s %8s",
		"arm", "N", "peak_RSS", "after_RSS", "reclaim_RSS", "peak_Anon", "reclaim_Anon", "thr_pk", "thr_af")
	for _, r := range rows {
		logf("%-10s %4d %10.2f %12.2f %12.2f %10.2f %12.2f %8d %8d",
			r.Arm, r.N, r.PeakRSSMB, r.AfterShrinkMB, r.ReclaimRSSMB,
			r.PeakAnonMB, r.ReclaimAnonMB, r.PeakThreads, r.AfterThreads)
	}
	if len(rows) == 2 {
		d := rows[1].ReclaimRSSMB - rows[0].ReclaimRSSMB
		logf("delta_reclaim_RSS(enhanced-legacy)=%+.2fMB", d)
		if d > 1.0 {
			logf("verdict: enhanced reclaims MORE RSS than legacy")
		} else if d < -1.0 {
			logf("verdict: enhanced reclaims LESS RSS than legacy")
		} else {
			logf("verdict: no meaningful RSS reclaim difference (|Δ|<1MB)")
			logf("note: Instance.Close enhancement is correctness (nil refs / idempotent); OS reclaim comes from wasm_instance_delete which both arms call")
		}
	}
	logf("log_file=%s", logPath)
}

// TestInstanceCloseABSingle is the isolated worker.
func TestInstanceCloseABSingle(t *testing.T) {
	arm := strings.TrimSpace(*instCloseABArm)
	if arm != "legacy" && arm != "enhanced" {
		t.Skip("worker only; set -inst_close_ab_arm=legacy|enhanced")
	}
	n := *instCloseABN
	if n < 1 {
		t.Fatal("n>=1")
	}
	w := io.MultiWriter(os.Stdout, probeTestWriter{t})
	logf := func(format string, args ...interface{}) {
		line := fmt.Sprintf(format, args...)
		if !strings.HasSuffix(line, "\n") {
			line += "\n"
		}
		_, _ = io.WriteString(w, line)
	}
	row, err := runInstanceCloseABArm(logf, *instCloseABWasm, arm, n)
	if err != nil {
		t.Fatal(err)
	}
	if *instCloseABResultFile == "" {
		t.Fatal("inst_close_ab_result_file required")
	}
	if err := os.WriteFile(*instCloseABResultFile, []byte(row.csvLine()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("RESULT %s", row.csvLine())
}

func runInstanceCloseABArm(
	logf func(string, ...interface{}),
	wasmPath, arm string,
	n int,
) (instCloseABRow, error) {
	row := instCloseABRow{Arm: arm, N: n}
	settle := func() {
		if *instCloseABSettle > 0 {
			time.Sleep(*instCloseABSettle)
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
	name := strings.TrimSuffix(filepath.Base(wasmPath), filepath.Ext(wasmPath))
	logger := logger2.GetLogger("inst_close_ab_" + name)
	contract := &commonPb.Contract{
		Name:        name,
		Version:     "1.0.0",
		RuntimeType: commonPb.RuntimeType_WASMER,
	}

	logf("=== arm=%s n=%d (ONLY Instance.Close differs) ===", arm, n)
	base := sample("0_baseline")
	logf("%s", base.String())

	config := wasmergo.NewConfig()
	config.MaxPagesLimit(512)
	config.CompilerNumThreads(4)
	config.PushMeteringMiddleware(1e19, map[wasmergo.Opcode]uint32{wasmergo.Opcode(0): 0}, map[string]uint32{}, "")
	engine := wasmergo.NewEngineWithConfig(config)
	store := wasmergo.NewStore(engine)
	module, err := wasmergo.NewModule(store, byteCode, logger)
	if err != nil {
		return row, err
	}
	afterMod := sample("1_after_module")
	logf("%s  %s", afterMod.String(), afterMod.Sub(base).String())

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
		inst, err := createInstanceWithProbe(logf, pool, NewResourceProbe("x"), name, arm, i, false)
		if err != nil {
			for _, x := range instances {
				closeInstanceArmEnhanced(pool, x)
			}
			return row, err
		}
		instances = append(instances, inst)
	}
	peak := sample("2_peak")
	logf("PEAK %s", peak.String())
	row.PeakRSSMB = mb(peak.VmRSSKB)
	row.PeakAnonMB = mb(peak.AnonRSSKB)
	row.PeakThreads = peak.Threads

	logf("--- SHRINK arm=%s ---", arm)
	for i := len(instances) - 1; i >= 0; i-- {
		if arm == "legacy" {
			closeInstanceArmLegacy(pool, instances[i])
		} else {
			closeInstanceArmEnhanced(pool, instances[i])
		}
		instances[i] = nil
		after := sample(fmt.Sprintf("shrink_alive_%d", i))
		logf("  alive=%d %s", i, after.Sub(peak).String())
	}
	instances = nil
	// Same GC for both arms so only Instance.Close differs.
	runtime.GC()
	settle()
	after := sample("3_after_shrink")
	logf("AFTER %s  vs peak %s", after.String(), after.Sub(peak).String())
	row.AfterShrinkMB = mb(after.VmRSSKB)
	row.AfterAnonMB = mb(after.AnonRSSKB)
	row.AfterThreads = after.Threads
	row.ReclaimRSSMB = row.PeakRSSMB - row.AfterShrinkMB
	row.ReclaimAnonMB = row.PeakAnonMB - row.AfterAnonMB
	logf("RECLAIM arm=%s RSS=%.2fMB Anon=%.2fMB threads %d→%d",
		arm, row.ReclaimRSSMB, row.ReclaimAnonMB, row.PeakThreads, row.AfterThreads)

	module.Close()
	store.Close()
	engine.Close()
	return row, nil
}
