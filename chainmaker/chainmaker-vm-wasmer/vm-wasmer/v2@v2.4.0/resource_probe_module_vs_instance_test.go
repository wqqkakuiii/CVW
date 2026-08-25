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
	"strings"
	"testing"
	"time"

	logger2 "chainmaker.org/chainmaker/logger/v2"
	commonPb "chainmaker.org/chainmaker/pb-go/v2/common"
	wasmergo "chainmaker.org/chainmaker/vm-wasmer/v2/wasmer-go"
)

var (
	mvsiProbeWasms = flag.String("mvsi_probe_wasms",
		strings.Join([]string{
			"./testdata/fact-go.wasm",
			"./testdata/exchange-go.wasm",
			"./testdata/erc721-go.wasm",
			"./testdata/compute-go.wasm",
			"./testdata/identity-go.wasm",
			"./testdata/raffle-go.wasm",
			"./testdata/itinerary-go.wasm",
			"./testdata/bigInput-go.wasm",
			"./testdata/test.wasm",
		}, ","),
		"comma-separated wasm paths for TestModuleVsInstanceRSS")
	mvsiProbeInstances = flag.Int("mvsi_probe_instances", 3,
		"instances to create per contract (kept alive; avg = totalΔ/N)")
	mvsiProbeSettle = flag.Duration("mvsi_probe_settle", 250*time.Millisecond,
		"settle before each RSS sample")
	mvsiProbeLog = flag.String("mvsi_probe_log", "",
		"result log; empty => ./resource_probe_mvsi_<timestamp>.log")
)

type mvsiRow struct {
	Name          string
	WasmMB        float64
	ModuleVmRSS   float64 // MB
	ModuleAnon    float64
	InstN         int
	InstTotalRSS  float64 // MB for N instances
	InstTotalAnon float64
	InstAvgRSS    float64
	InstAvgAnon   float64
	Inst1RSS      float64 // first instance alone
	Inst1Anon     float64
	Err           string
}

// TestModuleVsInstanceRSS compares Host RSS of NewModule vs NewInstance(+WASI)
// across multiple contracts. Each instance creates a fresh wasiEnv via Finalize.
//
//	go test -run TestModuleVsInstanceRSS -v -count=1 -timeout 60m \
//	  -args -mvsi_probe_instances=3 -mvsi_probe_log=./resource_probe_mvsi.log
func TestModuleVsInstanceRSS(t *testing.T) {
	logPath := *mvsiProbeLog
	if logPath == "" {
		logPath = fmt.Sprintf("./resource_probe_mvsi_%s.log", time.Now().Format("20060102_150405"))
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

	n := *mvsiProbeInstances
	if n < 1 {
		t.Fatal("mvsi_probe_instances must be >= 1")
	}
	wasms := splitCSV(*mvsiProbeWasms)
	if len(wasms) == 0 {
		t.Fatal("no wasm in -mvsi_probe_wasms")
	}

	logf("=== TestModuleVsInstanceRSS ===")
	logf("time=%s", time.Now().Format(time.RFC3339))
	logf("log_file=%s", logPath)
	logf("instances=%d settle=%v", n, *mvsiProbeSettle)
	logf("contracts=%v", wasms)
	logf("note: Module = NewModule only; Instance = wasiEnv+imports+NewInstance+WASI Init")
	logf("note: each instance Finalize()s a new wasiEnv; CloseInstance deletes it")
	logf("")

	var rows []mvsiRow
	for i, wasmPath := range wasms {
		name := strings.TrimSuffix(filepath.Base(wasmPath), filepath.Ext(wasmPath))
		logf("########## [%d/%d] %s (%s) ##########", i+1, len(wasms), name, wasmPath)
		row := runModuleVsInstance(t, logf, wasmPath, name, n)
		rows = append(rows, row)
		if row.Err != "" {
			t.Errorf("%s: %s", name, row.Err)
		}
		logf("")
	}

	logf("=== SUMMARY (MB) ===")
	logf("%-16s %8s %10s %10s %6s %10s %10s %10s %10s %10s %10s",
		"contract", "wasm", "mod_RSS", "mod_Anon", "N",
		"inst1_RSS", "inst1_Anon", "instAvgRSS", "instAvgAnon", "instTotRSS", "instTotAnon")
	for _, r := range rows {
		if r.Err != "" {
			logf("%-16s ERROR: %s", r.Name, r.Err)
			continue
		}
		logf("%-16s %8.2f %10.2f %10.2f %6d %10.2f %10.2f %10.2f %10.2f %10.2f %10.2f",
			r.Name, r.WasmMB, r.ModuleVmRSS, r.ModuleAnon, r.InstN,
			r.Inst1RSS, r.Inst1Anon, r.InstAvgRSS, r.InstAvgAnon, r.InstTotalRSS, r.InstTotalAnon)
	}
	logf("")
	logf("legend: mod_* = Δ after NewModule; inst1_* = first instance; instAvg_* = totalΔ/N")
	logf("log_file=%s", logPath)
	t.Logf("module vs instance probe written to %s", logPath)
}

func runModuleVsInstance(
	t *testing.T,
	logf func(string, ...interface{}),
	wasmPath, contractName string,
	n int,
) mvsiRow {
	t.Helper()
	row := mvsiRow{Name: contractName, InstN: n}

	settle := func() {
		if *mvsiProbeSettle > 0 {
			time.Sleep(*mvsiProbeSettle)
		}
	}
	sample := func(label string) ResourceSnapshot {
		settle()
		return SampleResource(label)
	}
	mb := func(kb int64) float64 { return float64(kb) / 1024 }

	fi, err := os.Stat(wasmPath)
	if err != nil {
		row.Err = err.Error()
		return row
	}
	row.WasmMB = float64(fi.Size()) / 1024 / 1024

	logger := logger2.GetLogger("mvsi_" + contractName)
	contract := &commonPb.Contract{
		Name:        contractName,
		Version:     "1.0.0",
		RuntimeType: commonPb.RuntimeType_WASMER,
	}

	base := sample(contractName + "/0_baseline")
	logf("%s", base.String())

	config := wasmergo.NewConfig()
	config.MaxPagesLimit(512)
	config.PushMeteringMiddleware(1e19, map[wasmergo.Opcode]uint32{wasmergo.Opcode(0): 0}, map[string]uint32{}, "")
	engine := wasmergo.NewEngineWithConfig(config)
	store := wasmergo.NewStore(engine)

	afterStore := sample(contractName + "/1_after_engine_store")
	logf("%s", afterStore.String())
	logf("  %s", afterStore.Sub(base).String())

	byteCode, err := os.ReadFile(wasmPath)
	if err != nil {
		store.Close()
		row.Err = err.Error()
		return row
	}
	afterRead := sample(contractName + "/2_after_ReadFile")
	logf("%s", afterRead.String())
	logf("  %s  (file=%.2fMB)", afterRead.Sub(afterStore).String(), row.WasmMB)

	if err := wasmergo.ValidateModule(store, byteCode); err != nil {
		store.Close()
		row.Err = "ValidateModule: " + err.Error()
		return row
	}
	afterValidate := sample(contractName + "/3_after_Validate")
	logf("%s", afterValidate.String())
	logf("  %s", afterValidate.Sub(afterRead).String())

	t0 := time.Now()
	module, err := wasmergo.NewModule(store, byteCode, logger)
	compileElapsed := time.Since(t0)
	if err != nil {
		store.Close()
		row.Err = "NewModule: " + err.Error()
		return row
	}
	afterModule := sample(contractName + "/4_after_NewModule")
	modΔ := afterModule.Sub(afterValidate)
	row.ModuleVmRSS = mb(modΔ.VmRSSKB)
	row.ModuleAnon = mb(modΔ.AnonRSSKB)
	logf("--- MODULE NewModule elapsed=%v ---", compileElapsed)
	logf("  before: %s", afterValidate.String())
	logf("  after:  %s", afterModule.String())
	logf("  %s", modΔ.String())
	logf("  MODULE_COST VmRSS=%+.2fMB Anon=%+.2fMB (×file=%.1fx)",
		row.ModuleVmRSS, row.ModuleAnon, row.ModuleVmRSS/row.WasmMB)

	pool := &vmPool{
		contractId: contract,
		byteCode:   byteCode,
		store:      store,
		module:     module,
		instances:  make(chan *wrappedInstance, n+1),
		log:        logger,
	}

	beforeInst := afterModule
	var instances []*wrappedInstance
	var afterFirst ResourceSnapshot

	logf("--- INSTANCE create × %d (keep alive) ---", n)
	for i := 0; i < n; i++ {
		before := sample(fmt.Sprintf("%s/inst%d_before", contractName, i))
		inst, err := createInstanceWithProbe(logf, pool, NewResourceProbe("x"), contractName, "mvsi", i, false)
		if err != nil {
			for _, x := range instances {
				pool.CloseInstance(x)
			}
			module.Close()
			store.Close()
			row.Err = fmt.Sprintf("instance #%d: %v", i, err)
			return row
		}
		instances = append(instances, inst)
		after := sample(fmt.Sprintf("%s/inst%d_after", contractName, i))
		d := after.Sub(before)
		logf("  inst#%d Δ: %s", i, d.String())
		if i == 0 {
			afterFirst = after
			row.Inst1RSS = mb(afterFirst.Sub(beforeInst).VmRSSKB)
			row.Inst1Anon = mb(afterFirst.Sub(beforeInst).AnonRSSKB)
		}
	}

	peak := sample(contractName + "/5_peak_N_alive")
	totΔ := peak.Sub(beforeInst)
	row.InstTotalRSS = mb(totΔ.VmRSSKB)
	row.InstTotalAnon = mb(totΔ.AnonRSSKB)
	row.InstAvgRSS = row.InstTotalRSS / float64(n)
	row.InstAvgAnon = row.InstTotalAnon / float64(n)

	logf("--- INSTANCES peak N=%d ---", n)
	logf("  before_instances: %s", beforeInst.String())
	logf("  peak:             %s", peak.String())
	logf("  %s", totΔ.String())
	logf("  INSTANCE_COST_1st VmRSS=%+.2fMB Anon=%+.2fMB", row.Inst1RSS, row.Inst1Anon)
	logf("  INSTANCE_COST_avg VmRSS=%+.2fMB Anon=%+.2fMB (total/N)", row.InstAvgRSS, row.InstAvgAnon)
	logf("  INSTANCE_COST_tot VmRSS=%+.2fMB Anon=%+.2fMB", row.InstTotalRSS, row.InstTotalAnon)
	logf("  COMPARE module_RSS=%.2fMB vs inst_avg_RSS=%.2fMB (ratio inst/mod=%.2f)",
		row.ModuleVmRSS, row.InstAvgRSS, row.InstAvgRSS/maxFloat(row.ModuleVmRSS, 0.01))

	for _, x := range instances {
		pool.CloseInstance(x)
	}
	module.Close()
	store.Close()
	settle()
	afterCleanup := SampleResource(contractName + "/6_after_cleanup")
	logf("%s", afterCleanup.String())
	logf("  vs peak: %s", afterCleanup.Sub(peak).String())

	return row
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
