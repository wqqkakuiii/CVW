/*
Copyright (C) BABEC. All rights reserved.
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wasmer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	logger2 "chainmaker.org/chainmaker/logger/v2"
	commonPb "chainmaker.org/chainmaker/pb-go/v2/common"
)

func samplePID(pid int, label string) (ResourceSnapshot, error) {
	s := ResourceSnapshot{Label: label, Time: time.Now(), Threads: -1, VmRSSKB: -1, VmSizeKB: -1, AnonRSSKB: -1}
	if pid <= 0 {
		return s, fmt.Errorf("bad pid %d", pid)
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return s, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "Threads:"):
			fmt.Sscanf(line, "Threads: %d", &s.Threads)
		case strings.HasPrefix(line, "VmRSS:"):
			fmt.Sscanf(line, "VmRSS: %d kB", &s.VmRSSKB)
		case strings.HasPrefix(line, "VmSize:"):
			fmt.Sscanf(line, "VmSize: %d kB", &s.VmSizeKB)
		}
	}
	if roll, err := os.ReadFile(fmt.Sprintf("/proc/%d/smaps_rollup", pid)); err == nil {
		for _, line := range strings.Split(string(roll), "\n") {
			if strings.HasPrefix(line, "Anonymous:") {
				fmt.Sscanf(line, "Anonymous: %d kB", &s.AnonRSSKB)
				break
			}
		}
	}
	return s, nil
}

func pidAlive(pid int) bool {
	_, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
	return err == nil
}

func listWorkerChildren(parentPID int) []int {
	var out []int
	dirs, err := os.ReadDir("/proc")
	if err != nil {
		return out
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		var pid int
		if _, err := fmt.Sscanf(d.Name(), "%d", &pid); err != nil || pid <= 0 {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", d.Name(), "cmdline"))
		if err != nil {
			continue
		}
		if !strings.Contains(string(cmdline), "wasmer-process-worker") {
			continue
		}
		st, err := os.ReadFile(filepath.Join("/proc", d.Name(), "status"))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(st), "\n") {
			if strings.HasPrefix(line, "PPid:") {
				var ppid int
				fmt.Sscanf(line, "PPid: %d", &ppid)
				if ppid == parentPID {
					out = append(out, pid)
				}
			}
		}
	}
	return out
}

// TestProcessWorkerKillReleasesAllMemoryAndThreads verifies that killing the
// warm worker process makes its /proc entry disappear (OS reclaims that PID's
// address space + native threads). Parent must not absorb the worker's RSS/threads.
func TestProcessWorkerKillReleasesAllMemoryAndThreads(t *testing.T) {
	ensureProcessWorkerBinForTest(t)
	t.Setenv(envProcessIsolation, "1")

	wasmPath := "./testdata/fact-go.wasm"
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read wasm: %v", err)
	}
	contractId := commonPb.Contract{
		Name: "fact", Version: ContractVersion, RuntimeType: commonPb.RuntimeType_WASMER,
	}
	logger := logger2.GetLogger("unit_test")

	parentPID := os.Getpid()
	baseParent := SampleResource("parent_baseline")
	t.Log(baseParent.String())

	beforeChildren := listWorkerChildren(parentPID)
	t.Logf("worker children before pool: %v", beforeChildren)

	vmPool, err := newVmPool(&contractId, wasmBytes, logger)
	if err != nil {
		t.Fatalf("newVmPool: %v", err)
	}
	if !vmPool.processIsolation {
		t.Fatal("expected process isolation")
	}
	// Parent still holds compiled Module/Engine; use this as baseline for instance-create deltas.
	parentAfterPool := SampleResource("parent_after_pool_module")
	t.Log(parentAfterPool.String())
	t.Log(parentAfterPool.Sub(baseParent).String())

	const n = 5
	lives := make([]*wrappedInstance, 0, n)
	for i := 0; i < n; i++ {
		inst, err := vmPool.newProcessIsolatedInstance()
		if err != nil {
			t.Fatalf("create #%d: %v", i, err)
		}
		lives = append(lives, inst)
	}

	w := vmPool.worker
	if w == nil || !w.alive() {
		t.Fatal("expected live worker")
	}
	workerPID := w.pid()
	workerAlive, err := samplePID(workerPID, "worker_with_5_instances")
	if err != nil {
		t.Fatalf("sample worker: %v", err)
	}
	t.Log(workerAlive.String())
	if workerAlive.VmRSSKB < 10*1024 {
		t.Fatalf("worker RSS too small: %.2fMB (want >=10MB)", float64(workerAlive.VmRSSKB)/1024)
	}
	if workerAlive.Threads < 2 {
		t.Fatalf("worker threads unexpected: %d", workerAlive.Threads)
	}

	parentBeforeKill := SampleResourceNoGC("parent_before_kill")
	t.Log(parentBeforeKill.String())
	dCreate := parentBeforeKill.Sub(parentAfterPool)
	t.Log(dCreate.String())
	if dCreate.VmRSSKB > workerAlive.VmRSSKB/4 {
		t.Fatalf("parent grew too much while creating child instances: parentΔ=%.2fMB worker=%.2fMB",
			float64(dCreate.VmRSSKB)/1024, float64(workerAlive.VmRSSKB)/1024)
	}

	stats, err := w.stats()
	if err != nil {
		t.Fatalf("worker stats: %v", err)
	}
	t.Logf("worker self-report VmRSS=%.2fMB threads=%d active=%d",
		float64(stats.VmRSSKB)/1024, stats.Threads, stats.Active)

	workerRSSBefore := workerAlive.VmRSSKB
	workerThreadsBefore := workerAlive.Threads

	// Kill worker only; keep parent Module so reclaim effect is isolated.
	vmPool.shutdownProcessWorkers()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(workerPID) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if pidAlive(workerPID) {
		t.Fatalf("worker pid=%d still alive after shutdownProcessWorkers()", workerPID)
	}
	t.Logf("worker pid=%d gone from /proc", workerPID)

	time.Sleep(200 * time.Millisecond)
	afterChildren := listWorkerChildren(parentPID)
	t.Logf("worker children after kill: %v", afterChildren)
	for _, c := range afterChildren {
		found := false
		for _, b := range beforeChildren {
			if b == c {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("orphan wasmer-process-worker pid=%d still parented by test process", c)
		}
	}

	parentAfterKill := SampleResource("parent_after_kill")
	t.Log(parentAfterKill.String())
	dKill := parentAfterKill.Sub(parentBeforeKill)
	t.Log(dKill.String())

	if dKill.VmRSSKB > 20*1024 {
		t.Fatalf("parent RSS jumped after worker kill (absorbed child?): Δ=%.2fMB", float64(dKill.VmRSSKB)/1024)
	}
	if dKill.Threads > 4 {
		t.Fatalf("parent threads jumped after worker kill: Δ=%d", dKill.Threads)
	}
	if _, err := samplePID(workerPID, "should_be_gone"); err == nil {
		t.Fatalf("worker pid=%d still readable in /proc after kill", workerPID)
	}

	fmt.Printf("\n=== VERDICT ===\n")
	fmt.Printf("worker_before_kill: VmRSS=%.2fMB threads=%d\n", float64(workerRSSBefore)/1024, workerThreadsBefore)
	fmt.Printf("worker_after_kill:  /proc/%d gone → kernel reclaimed that PID's memory+threads\n", workerPID)
	fmt.Printf("parent_while_create_vs_pool: VmRSS=%+.2fMB threads=%+d (instances in child; should be small)\n",
		float64(dCreate.VmRSSKB)/1024, dCreate.Threads)
	fmt.Printf("parent_after_kill_vs_before: VmRSS=%+.2fMB threads=%+d (must not absorb worker)\n",
		float64(dKill.VmRSSKB)/1024, dKill.Threads)
	fmt.Printf("orphan_workers: %v\n", afterChildren)

	vmPool.close()
	_ = lives
}
