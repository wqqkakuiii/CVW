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

// TestVMAAttributionCreateDestroy attributes create/destroy RSS asymmetry via /proc/smaps.
//
//	go test -run TestVMAAttributionCreateDestroy -v -count=1 -timeout 30m
func TestVMAAttributionCreateDestroy(t *testing.T) {
	wasmPath := "./testdata/fact-go.wasm"
	n := 10
	settle := 500 * time.Millisecond
	logPath := fmt.Sprintf("../resource_probes/logs/resource_probe_vma_attribution_%s.log",
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
		Name: strings.TrimSuffix(filepath.Base(wasmPath), filepath.Ext(wasmPath)),
		Version: "1.0.0", RuntimeType: commonPb.RuntimeType_WASMER,
	}
	logger := logger2.GetLogger("vma_attr")

	logf("=== TestVMAAttributionCreateDestroy ===")
	logf("time=%s wasm=%s n=%d settle=%v", time.Now().Format(time.RFC3339), wasmPath, n, settle)

	wait := func() { time.Sleep(settle) }
	snap := func(label string) SmapsRollup {
		wait()
		r, err := ReadSmapsRollup(label)
		if err != nil {
			t.Fatal(err)
		}
		proc := SampleResourceNoGC(label)
		logf("[%s] proc %s smaps_rss=%.2fMB smaps_anon=%.2fMB",
			label, proc.String(), float64(r.TotalRSSKB)/1024, float64(r.TotalAnonKB)/1024)
		return r
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
		instances: make(chan *wrappedInstance, n+1),
		log:       logger,
	}

	base := snap("0_after_module")
	var sumCreateRSS, sumDestroyRSS, sumNetRSS int64
	var sumCreateAnon, sumDestroyAnon int64
	var sumCreateProcRSS, sumDestroyProcRSS, sumNetProcRSS int64
	createCat := make(map[string]int64)
	destroyCat := make(map[string]int64)
	netCat := make(map[string]int64)

	prev := base
	prevProc := SampleResourceNoGC("module_proc")
	for i := 0; i < n; i++ {
		inst, err := createInstanceWithProbe(logf, pool, NewResourceProbe("x"), contract.Name, "vma", i, false)
		if err != nil {
			t.Fatal(err)
		}
		afterCreate := snap(fmt.Sprintf("create_%02d", i+1))
		procCreate := SampleResourceNoGC(fmt.Sprintf("create_%02d_proc", i+1))
		dCreate := DiffSmaps(afterCreate, prev)
		sumCreateRSS += dCreate.RSSKB
		sumCreateAnon += dCreate.AnonKB
		for c, v := range dCreate.Category {
			createCat[c] += v
		}
		dProcCreate := procCreate.Sub(prevProc)
		sumCreateProcRSS += dProcCreate.VmRSSKB
		logf("  CREATE_%02d smaps Δrss=%+.2fMB proc VmRSS=%+.2fMB Anon=%+.2fMB threads=%+d |%s|%s",
			i+1, float64(dCreate.RSSKB)/1024, float64(dProcCreate.VmRSSKB)/1024,
			float64(dProcCreate.AnonRSSKB)/1024, dProcCreate.Threads,
			dCreate.FormatCategories(), dCreate.FormatTopVMA())

		closeWrappedInstanceBasic(pool, inst)
		afterDestroy := snap(fmt.Sprintf("destroy_%02d", i+1))
		procDestroy := SampleResourceNoGC(fmt.Sprintf("destroy_%02d_proc", i+1))
		dDestroy := DiffSmaps(afterDestroy, afterCreate)
		sumDestroyRSS += dDestroy.RSSKB
		sumDestroyAnon += dDestroy.AnonKB
		for c, v := range dDestroy.Category {
			destroyCat[c] += v
		}
		dProcDestroy := procDestroy.Sub(procCreate)
		sumDestroyProcRSS += dProcDestroy.VmRSSKB
		logf("  DESTROY_%02d smaps Δrss=%+.2fMB proc VmRSS=%+.2fMB Anon=%+.2fMB threads=%+d |%s|%s",
			i+1, float64(dDestroy.RSSKB)/1024, float64(dProcDestroy.VmRSSKB)/1024,
			float64(dProcDestroy.AnonRSSKB)/1024, dProcDestroy.Threads,
			dDestroy.FormatCategories(), dDestroy.FormatTopVMA())

		dNet := DiffSmaps(afterDestroy, prev)
		sumNet := dNet.RSSKB
		sumNetRSS += sumNet
		dProcNet := procDestroy.Sub(prevProc)
		sumNetProcRSS += dProcNet.VmRSSKB
		for c, v := range dNet.Category {
			netCat[c] += v
		}
		logf("  NET_%02d smaps Δrss=%+.2fMB proc VmRSS=%+.2fMB Anon=%+.2fMB |%s|",
			i+1, float64(dNet.RSSKB)/1024, float64(dProcNet.VmRSSKB)/1024,
			float64(dProcNet.AnonRSSKB)/1024, dNet.FormatCategories())
		prev = afterDestroy
		prevProc = procDestroy
	}

	final := snap("final_after_n_cycles")
	dFinal := DiffSmaps(final, base)
	logf("")
	logf("=== SUMMARY (%d cycles, equal %v settle) ===", n, settle)
	logf("AVG per CREATE:  smaps Δrss=%+.2fMB proc VmRSS=%+.2fMB Δanon=%+.2fMB",
		float64(sumCreateRSS)/1024/float64(n), float64(sumCreateProcRSS)/1024/float64(n),
		float64(sumCreateAnon)/1024/float64(n))
	logf("AVG per DESTROY: smaps Δrss=%+.2fMB proc VmRSS=%+.2fMB Δanon=%+.2fMB",
		float64(sumDestroyRSS)/1024/float64(n), float64(sumDestroyProcRSS)/1024/float64(n),
		float64(sumDestroyAnon)/1024/float64(n))
	logf("AVG per NET:     smaps Δrss=%+.2fMB proc VmRSS=%+.2fMB (create+destroy residual per cycle)",
		float64(sumNetRSS)/1024/float64(n), float64(sumNetProcRSS)/1024/float64(n))
	logf("CUMULATIVE create categories (RSS):%s", formatCatMap(createCat))
	logf("CUMULATIVE destroy categories (RSS):%s", formatCatMap(destroyCat))
	logf("CUMULATIVE net categories (RSS):%s", formatCatMap(netCat))
	logf("FINAL vs module baseline: Δrss=%+.2fMB Δanon=%+.2fMB |%s|%s",
		float64(dFinal.RSSKB)/1024, float64(dFinal.AnonKB)/1024,
		dFinal.FormatCategories(), dFinal.FormatTopVMA())
	logf("log_file=%s", logPath)
	t.Logf("vma attribution log: %s", logPath)
}

func formatCatMap(m map[string]int64) string {
	d := SmapsDelta{Category: m}
	return d.FormatCategories()
}
