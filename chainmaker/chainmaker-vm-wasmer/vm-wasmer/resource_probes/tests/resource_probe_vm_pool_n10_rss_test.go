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

// TestVmPoolCreate10ThenDestroy10RSS mirrors vm_pool.newInstanceFromModule +
// CloseInstance: share Engine/Module, CloneStore per instance, hold N=10, then
// destroy all while recording VmRSS/RssAnon.
//
//	cd vm-wasmer/v2@v2.4.0
//	go test -run TestVmPoolCreate10ThenDestroy10RSS -v -count=1 -timeout 30m
func TestVmPoolCreate10ThenDestroy10RSS(t *testing.T) {
	wasmPath := "./testdata/fact-go.wasm"
	n := 10
	settle := 300 * time.Millisecond
	logPath := fmt.Sprintf("../resource_probes/logs/resource_probe_vm_pool_n10_%s.log",
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
	logger := logger2.GetLogger("vm_pool_n10")

	logf("=== TestVmPoolCreate10ThenDestroy10RSS (Go / wasmer-go) ===")
	logf("pattern=vm_pool.newInstanceFromModule + CloseInstance")
	logf("time=%s wasm=%s n=%d settle=%v", time.Now().Format(time.RFC3339), wasmPath, n, settle)

	wait := func() { time.Sleep(settle) }
	printSnap := func(tag string, base, now ResourceSnapshot) {
		logf("  %-28s VmRSS=%8d kB (Δ%+7d)  Anon=%8d kB (Δ%+7d)  thr=%d",
			tag, now.VmRSSKB, now.VmRSSKB-base.VmRSSKB,
			now.AnonRSSKB, now.AnonRSSKB-base.AnonRSSKB, now.Threads)
	}

	// Same setup spine as newVmPool (metering + MaxPages + Engine/Store/Module).
	cfg := wasmergo.NewConfig()
	cfg.MaxPagesLimit(512)
	cfg.CompilerNumThreads(1)
	cfg.PushMeteringMiddleware(1e19,
		map[wasmergo.Opcode]uint32{wasmergo.Opcode(0): 0},
		map[string]uint32{}, "")
	engine := wasmergo.NewEngineWithConfig(cfg)
	store := wasmergo.NewStore(engine)
	module, err := wasmergo.NewModule(store, byteCode, logger)
	if err != nil {
		t.Fatal(err)
	}
	pool := &vmPool{
		contractId: contract,
		byteCode:   byteCode,
		engine:     engine,
		store:      store,
		module:     module,
		instances:  make(chan *wrappedInstance, n+1),
		log:        logger,
	}

	wait()
	base := SampleResourceNoGC("0_after_module")
	printSnap("0 baseline (engine+module)", base, base)

	lives := make([]*wrappedInstance, 0, n)

	// --- grow: create N and keep alive ---
	for i := 1; i <= n; i++ {
		inst, err := createInstanceWithProbe(logf, pool, NewResourceProbe("x"), contract.Name, "n10", i-1, false)
		if err != nil {
			t.Fatalf("create #%d: %v", i, err)
		}
		// Touch linear memory like a real invoke would dirty pages.
		if mem, err := inst.wasmInstance.Exports.GetMemory("memory"); err == nil && mem != nil {
			data := mem.Data()
			step := 4096
			for off := 0; off+1 < len(data) && off < 16*65536; off += step {
				data[off] ^= 0x5a
			}
		}
		lives = append(lives, inst)
		wait()
		printSnap(fmt.Sprintf("+ create #%d (alive=%d)", i, len(lives)), base, SampleResourceNoGC(fmt.Sprintf("c%d", i)))
	}

	wait()
	afterCreate := SampleResourceNoGC("after_create_all")
	printSnap(fmt.Sprintf("* after create all %d", n), base, afterCreate)

	// --- shrink: CloseInstance in reverse (same as vm_pool.CloseInstance) ---
	for i := n; i >= 1; i-- {
		inst := lives[len(lives)-1]
		lives = lives[:len(lives)-1]
		pool.CloseInstance(inst)
		wait()
		printSnap(fmt.Sprintf("- destroy #%d (alive=%d)", i, len(lives)), base, SampleResourceNoGC(fmt.Sprintf("d%d", i)))
	}

	wait()
	afterDestroy := SampleResourceNoGC("after_destroy_all")
	printSnap("* after destroy all", base, afterDestroy)

	createDelta := afterCreate.VmRSSKB - base.VmRSSKB
	destroyDelta := afterDestroy.VmRSSKB - afterCreate.VmRSSKB
	net := afterDestroy.VmRSSKB - base.VmRSSKB
	logf("  summary: create ΔVmRSS=%+d kB, destroy ΔVmRSS=%+d kB, net vs baseline=%+d kB",
		createDelta, destroyDelta, net)
	logf("  Anon: create Δ=%+d kB, destroy Δ=%+d kB, net=%+d kB",
		afterCreate.AnonRSSKB-base.AnonRSSKB,
		afterDestroy.AnonRSSKB-afterCreate.AnonRSSKB,
		afterDestroy.AnonRSSKB-base.AnonRSSKB)
	logf("log_file=%s", logPath)
	t.Logf("vm_pool n10 RSS log: %s", logPath)
}
