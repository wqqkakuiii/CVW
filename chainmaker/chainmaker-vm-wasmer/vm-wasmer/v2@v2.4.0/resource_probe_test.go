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
	createProbeWasms = flag.String("create_probe_wasms",
		strings.Join([]string{
			"./testdata/fact-go.wasm",
			"./testdata/exchange-go.wasm",
			"./testdata/erc721-go.wasm",
			"./testdata/compute-go.wasm",
			"./testdata/identity-go.wasm",
			"./testdata/raffle-go.wasm",
			"./testdata/itinerary-go.wasm",
		}, ","),
		"comma-separated wasm paths for TestCreateInstanceResourcePhases")
	createProbeRepeat = flag.Int("create_probe_repeat", 10,
		"instances to create per grow phase")
	createProbeCycles = flag.Int("create_probe_cycles", 2,
		"how many grow→shrink-to-0 cycles to run per contract")
	createProbeSettle = flag.Duration("create_probe_settle", 200*time.Millisecond,
		"sleep after each stage before sampling (lets Tokio threads show up in /proc)")
	createProbeLog = flag.String("create_probe_log", "",
		"result log path; empty => ./resource_probe_<timestamp>.log")
	createProbeDetailFirst = flag.Bool("create_probe_detail_first", true,
		"log Finalize/imports/NewInstance/Init only for the first instance of the first cycle")
)

// TestCreateInstanceResourcePhases: per contract, compile once then run
// grow N → shrink to 0 → grow N → shrink to 0 (cycles configurable).
//
//	cd vm-wasmer/v2@v2.4.0
//	go test -run TestCreateInstanceResourcePhases -v -count=1 -timeout 60m \
//	  -args -create_probe_repeat=10 -create_probe_cycles=2 \
//	        -create_probe_log=./resource_probe_results.log
func TestCreateInstanceResourcePhases(t *testing.T) {
	logPath := *createProbeLog
	if logPath == "" {
		logPath = fmt.Sprintf("./resource_probe_%s.log", time.Now().Format("20060102_150405"))
	}
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create log file %q: %v", logPath, err)
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

	wasms := splitCSV(*createProbeWasms)
	if len(wasms) == 0 {
		t.Fatal("no wasm paths in -create_probe_wasms")
	}
	cycles := *createProbeCycles
	if cycles < 1 {
		t.Fatal("create_probe_cycles must be >= 1")
	}

	logf("=== TestCreateInstanceResourcePhases ===")
	logf("time=%s", time.Now().Format(time.RFC3339))
	logf("log_file=%s", logPath)
	logf("repeat=%d cycles=%d settle=%v detail_first=%v",
		*createProbeRepeat, cycles, *createProbeSettle, *createProbeDetailFirst)
	logf("pattern=grow(N)→shrink(0) × %d", cycles)
	logf("destroy=CloseInstance deletes wasiEnv (wasi_env_delete)")
	logf("contracts=%v", wasms)
	logf("note: process RSS/threads accumulate across contracts; compare within each contract section.")
	logf("")

	for i, wasmPath := range wasms {
		name := strings.TrimSuffix(filepath.Base(wasmPath), filepath.Ext(wasmPath))
		logf("########## contract[%d/%d] %s (%s) ##########", i+1, len(wasms), name, wasmPath)
		if err := runCreateDestroyProbe(t, logf, wasmPath, name, *createProbeRepeat, cycles); err != nil {
			logf("ERROR: %v", err)
			t.Errorf("contract %s: %v", name, err)
			continue
		}
		logf("")
	}

	logf("=== all contracts finished ===")
	t.Logf("resource probe results written to %s", logPath)
}

type probeTestWriter struct{ t *testing.T }

func (p probeTestWriter) Write(b []byte) (int, error) {
	p.t.Helper()
	s := strings.TrimRight(string(b), "\n")
	if s != "" {
		p.t.Log(s)
	}
	return len(b), nil
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func settleProbe() {
	if *createProbeSettle > 0 {
		time.Sleep(*createProbeSettle)
	}
}

func runCreateDestroyProbe(
	t *testing.T,
	logf func(string, ...interface{}),
	wasmPath, contractName string,
	n, cycles int,
) error {
	t.Helper()
	if n < 1 {
		return fmt.Errorf("repeat must be >= 1")
	}

	byteCode, err := os.ReadFile(wasmPath)
	if err != nil {
		return fmt.Errorf("read wasm: %w", err)
	}
	logger := logger2.GetLogger("create_probe_" + contractName)
	contract := &commonPb.Contract{
		Name:        contractName,
		Version:     "1.0.0",
		RuntimeType: commonPb.RuntimeType_WASMER,
	}

	probe := NewResourceProbe(contractName + "/0_baseline")
	logf("%s", probe.Baseline.String())

	config := wasmergo.NewConfig()
	config.MaxPagesLimit(512)
	config.PushMeteringMiddleware(1e19, map[wasmergo.Opcode]uint32{wasmergo.Opcode(0): 0}, map[string]uint32{}, "")
	engine := wasmergo.NewEngineWithConfig(config)
	store := wasmergo.NewStore(engine)
	settleProbe()
	logf("%s", probe.Mark(contractName+"/1_after_engine_store").String())

	if err := wasmergo.ValidateModule(store, byteCode); err != nil {
		return fmt.Errorf("ValidateModule: %w", err)
	}
	settleProbe()
	logf("%s", probe.Mark(contractName+"/2_after_validate").String())

	module, err := wasmergo.NewModule(store, byteCode, logger)
	if err != nil {
		return fmt.Errorf("NewModule: %w", err)
	}
	settleProbe()
	logf("%s", probe.Mark(contractName+"/3_after_NewModule").String())

	pool := &vmPool{
		contractId: contract,
		byteCode:   byteCode,
		store:      store,
		module:     module,
		instances:  make(chan *wrappedInstance, n+1),
		log:        logger,
	}

	for c := 1; c <= cycles; c++ {
		cycle := fmt.Sprintf("c%d", c)
		logf("===== %s cycle %d/%d: GROW %d =====", contractName, c, cycles, n)

		var instances []*wrappedInstance
		for i := 0; i < n; i++ {
			detail := *createProbeDetailFirst && c == 1 && i == 0
			inst, err := createInstanceWithProbe(logf, pool, probe, contractName, cycle, i, detail)
			if err != nil {
				for _, x := range instances {
					pool.CloseInstance(x)
				}
				module.Close()
				store.Close()
				return fmt.Errorf("%s create #%d: %w", cycle, i, err)
			}
			instances = append(instances, inst)
			logf("%s alive=%d",
				probe.Mark(fmt.Sprintf("%s/%s/create_done_%d", contractName, cycle, i)).String(),
				len(instances))
		}
		logf("--- %s/%s all %d created ---", contractName, cycle, len(instances))
		logf("%s", probe.Mark(fmt.Sprintf("%s/%s/peak_all_alive", contractName, cycle)).String())

		logf("===== %s cycle %d/%d: SHRINK to 0 =====", contractName, c, cycles)
		for i := len(instances) - 1; i >= 0; i-- {
			closedID := instances[i].id
			pool.CloseInstance(instances[i])
			instances[i] = nil
			settleProbe()
			alive := i
			logf("%s closed=%s alive=%d",
				probe.Mark(fmt.Sprintf("%s/%s/after_close_alive_%d", contractName, cycle, alive)).String(),
				closedID, alive)
		}
		logf("%s",
			probe.Mark(fmt.Sprintf("%s/%s/after_shrink_zero", contractName, cycle)).String())
	}

	module.Close()
	store.Close()
	settleProbe()
	logf("%s", probe.Mark(contractName+"/9_after_module_store_close").String())
	logf("%s", SampleResource(contractName+"/9b_after_GC").String())

	logf("--- report for %s ---", contractName)
	logf("%s", probe.Report())
	return nil
}

func createInstanceWithProbe(
	logf func(string, ...interface{}),
	p *vmPool,
	probe *ResourceProbe,
	contractName, cycle string,
	idx int,
	detail bool,
) (*wrappedInstance, error) {
	prefix := fmt.Sprintf("%s/%s/inst%d", contractName, cycle, idx)

	vb := GetVmBridgeManager()
	env := CMEnvironment{instance: nil, memory: nil}

	// Same path as production newInstanceFromModule.
	wasiEnv, err := p.newWasiEnv()
	if err != nil {
		return nil, fmt.Errorf("newWasiEnv: %w", err)
	}
	settleProbe()
	if detail {
		logf("%s", probe.Mark(prefix+"_4_after_WASI_new").String())
	}

	importObject, err := wasiEnv.GenerateImportObject(p.store, p.module)
	if err != nil {
		p.deleteWasiEnv(wasiEnv)
		return nil, fmt.Errorf("GenerateImportObject: %w", err)
	}
	imports, err := vb.GetImports(p.store, &env, importObject)
	if imports == nil && err != nil {
		p.deleteWasiEnv(wasiEnv)
		return nil, fmt.Errorf("GetImports: %w", err)
	}
	settleProbe()
	if detail {
		logf("%s", probe.Mark(prefix+"_5_after_imports").String())
	}

	wasmInstance, err := wasmergo.NewInstance(p.module, imports)
	if err != nil {
		p.deleteWasiEnv(wasiEnv)
		return nil, fmt.Errorf("NewInstance: %w", err)
	}
	settleProbe()
	if detail {
		logf("%s", probe.Mark(prefix+"_6_after_NewInstance").String())
	}

	if err := wasiEnv.Initialize(p.store, wasmInstance); err != nil {
		wasmInstance.Close()
		p.deleteWasiEnv(wasiEnv)
		return nil, fmt.Errorf("WASI Initialize: %w", err)
	}
	if start, _ := wasmInstance.Exports.GetWasiStartRawFunction(); start != nil {
		start.Call()
	}
	settleProbe()
	if detail {
		logf("%s", probe.Mark(prefix+"_7_after_WASI_Init").String())
	}

	env.instance = wasmInstance
	env.memory, _ = wasmInstance.Exports.GetMemory("memory")

	return &wrappedInstance{
		id:           fmt.Sprintf("probe-%s-%s-%d", contractName, cycle, idx),
		wasmInstance: wasmInstance,
		wasiEnv:      wasiEnv,
		lastUseTime:  time.Now().UnixMilli(),
		createTime:   time.Now().UnixMilli(),
	}, nil
}
