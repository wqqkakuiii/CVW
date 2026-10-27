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
	"strings"
	"testing"
	"time"

	logger2 "chainmaker.org/chainmaker/logger/v2"
	commonPb "chainmaker.org/chainmaker/pb-go/v2/common"
	wasmergo "chainmaker.org/chainmaker/vm-wasmer/v2/wasmer-go"
)

var (
	procIsoProbeWasm = flag.String("proc_iso_probe_wasm", "./testdata/fact-go.wasm",
		"wasm for process-isolation probe")
	procIsoProbeN = flag.Int("proc_iso_probe_n", 4,
		"worker processes to create then kill")
	procIsoProbeSettle = flag.Duration("proc_iso_probe_settle", 400*time.Millisecond,
		"settle before each RSS sample")
	procIsoProbeLog = flag.String("proc_iso_probe_log", "",
		"result log; empty => ./resource_probe_process_iso_<timestamp>.log")
)

// ensureProcessWorkerBin builds cmd/wasmer-process-worker once per test process.
func ensureProcessWorkerBin(t *testing.T) string {
	t.Helper()
	if bin := strings.TrimSpace(os.Getenv(envProcessWorkerBin)); bin != "" {
		if _, err := os.Stat(bin); err == nil {
			return bin
		}
	}
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(pkgDir, "wasmer-process-worker")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/wasmer-process-worker")
	cmd.Dir = pkgDir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build worker: %v\n%s", err, out)
	}
	t.Setenv(envProcessWorkerBin, bin)
	return bin
}

// TestProcessIsolationWorkerEntry documents the former test-binary re-exec entry.
// Prefer WASMER_PROCESS_WORKER_BIN / cmd/wasmer-process-worker (stdout must stay clean).
func TestProcessIsolationWorkerEntry(t *testing.T) {
	if os.Getenv(envProcessWorker) != "1" {
		t.Skip("not a process worker; use wasmer-process-worker binary")
	}
	code := RunProcessIsolationWorker()
	if code != 0 {
		t.Fatalf("worker exit code %d", code)
	}
}

// TestProcessIsolationLifecycleProbe: parent compiles+serializes once, spawns N
// workers that Deserialize+instantiate, then kills them one-by-one and measures
// parent-process RSS/threads (Wasmer cost should live in children).
//
//	go test -run TestProcessIsolationLifecycleProbe -v -count=1 -timeout 20m
func TestProcessIsolationLifecycleProbe(t *testing.T) {
	ensureProcessWorkerBin(t)

	logPath := *procIsoProbeLog
	if logPath == "" {
		logPath = fmt.Sprintf("./resource_probe_process_iso_%s.log", time.Now().Format("20060102_150405"))
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

	n := *procIsoProbeN
	if n < 1 {
		t.Fatal("proc_iso_probe_n must be >= 1")
	}
	wasmPath := *procIsoProbeWasm
	logf("=== TestProcessIsolationLifecycleProbe ===")
	logf("time=%s wasm=%s n=%d settle=%v", time.Now().Format(time.RFC3339), wasmPath, n, *procIsoProbeSettle)
	logf("log_file=%s", logPath)
	logf("worker_bin=%s", os.Getenv(envProcessWorkerBin))
	logf("model: parent Serialize once; each worker NewEngine+NewStore+Deserialize+Instance; Close=Kill")
	logf("")

	settle := func() {
		if *procIsoProbeSettle > 0 {
			time.Sleep(*procIsoProbeSettle)
		}
	}
	sample := func(label string) ResourceSnapshot {
		settle()
		return SampleResourceNoGC(label)
	}
	mb := func(kb int64) float64 { return float64(kb) / 1024 }

	byteCode, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}

	base := sample("0_baseline")
	logf("%s", base.String())

	engine, store, err := newWasmerEngineAndStoreForPool()
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	defer store.Close()

	logger := logger2.GetLogger("proc_iso_" + strings.TrimSuffix(filepath.Base(wasmPath), ".wasm"))
	module, err := wasmergo.NewModule(store, byteCode, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer module.Close()

	afterMod := sample("1_after_parent_module")
	logf("%s", afterMod.String())

	serialized, err := module.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	dir, err := os.MkdirTemp("", "proc-iso-probe-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	artifact := filepath.Join(dir, "module.artifact")
	if err := os.WriteFile(artifact, serialized, 0o600); err != nil {
		t.Fatal(err)
	}
	afterSer := sample("2_after_serialize")
	logf("%s artifact_bytes=%d", afterSer.String(), len(serialized))

	workers := make([]*processWorker, 0, n)
	defer func() {
		for _, wkr := range workers {
			if wkr != nil {
				wkr.kill()
			}
		}
	}()

	type row struct {
		Step    string
		Alive   int
		VmRSSMB float64
		Threads int
		DRSS    float64
		DThr    int
	}
	var rows []row
	prev := afterSer
	rows = append(rows,
		row{"0_baseline", 0, mb(base.VmRSSKB), base.Threads, 0, 0},
		row{"1_after_parent_module", 0, mb(afterMod.VmRSSKB), afterMod.Threads, mb(afterMod.VmRSSKB - base.VmRSSKB), afterMod.Threads - base.Threads},
		row{"2_after_serialize", 0, mb(afterSer.VmRSSKB), afterSer.Threads, mb(afterSer.VmRSSKB - afterMod.VmRSSKB), afterSer.Threads - afterMod.Threads},
	)

	for i := 0; i < n; i++ {
		wkr, err := spawnProcessWorker(artifact)
		if err != nil {
			t.Fatalf("spawn worker %d: %v", i, err)
		}
		workers = append(workers, wkr)
		snap := sample(fmt.Sprintf("3_create_%02d", i+1))
		logf("%s pid=%d", snap.String(), wkr.pid())
		rows = append(rows, row{
			Step:    fmt.Sprintf("3_create_%02d", i+1),
			Alive:   i + 1,
			VmRSSMB: mb(snap.VmRSSKB),
			Threads: snap.Threads,
			DRSS:    mb(snap.VmRSSKB - prev.VmRSSKB),
			DThr:    snap.Threads - prev.Threads,
		})
		prev = snap
	}

	for i := n - 1; i >= 0; i-- {
		workers[i].kill()
		workers[i] = nil
		snap := sample(fmt.Sprintf("4_kill_%02d", n-i))
		logf("%s", snap.String())
		rows = append(rows, row{
			Step:    fmt.Sprintf("4_kill_%02d_alive_%d", n-i, i),
			Alive:   i,
			VmRSSMB: mb(snap.VmRSSKB),
			Threads: snap.Threads,
			DRSS:    mb(snap.VmRSSKB - prev.VmRSSKB),
			DThr:    snap.Threads - prev.Threads,
		})
		prev = snap
	}

	logf("")
	logf("=== PARENT MEMORY TABLE (Wasmer Instance lives in children) ===")
	logf("%-28s %5s %10s %7s %10s %6s", "step", "alive", "VmRSS_MB", "threads", "ΔRSS_MB", "Δthr")
	for _, r := range rows {
		logf("%-28s %5d %10.2f %7d %10.2f %6d", r.Step, r.Alive, r.VmRSSMB, r.Threads, r.DRSS, r.DThr)
	}
	logf("")
	logf("note: parent ΔRSS on create should stay small vs in-process; kill returns child pages to OS")
	logf("log_file=%s", logPath)
	t.Logf("process isolation probe written to %s", logPath)
}

// TestProcessIsolationViaVmPool exercises env-gated vmPool worker path.
func TestProcessIsolationViaVmPool(t *testing.T) {
	ensureProcessWorkerBin(t)
	t.Setenv(envProcessIsolation, "1")

	wasmPath := *procIsoProbeWasm
	byteCode, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}
	logger := logger2.GetLogger("proc_iso_pool")
	contract := commonPb.Contract{Name: "proc_iso", Version: "v1"}
	pool, err := newVmPool(&contract, byteCode, logger)
	if err != nil {
		t.Fatalf("newVmPool: %v", err)
	}
	defer pool.close()
	if !pool.processIsolation {
		t.Fatal("expected processIsolation enabled")
	}
	inst := pool.GetInstance()
	if inst.processWorker == nil {
		t.Fatal("expected processWorker on instance")
	}
	if err := inst.processWorker.ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	pool.CloseInstance(inst)
}
