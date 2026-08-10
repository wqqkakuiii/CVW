/*
Copyright (C) BABEC. All rights reserved.
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wasmer

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	logger2 "chainmaker.org/chainmaker/logger/v2"
	commonPb "chainmaker.org/chainmaker/pb-go/v2/common"
)

var (
	oomWasmPath         = flag.String("oom_wasm", "./testdata/fact-go.wasm", "wasm file for pool OOM test")
	oomContractName     = flag.String("oom_contract_name", "oom-load-test", "logical contract name prefix")
	oomContractType     = flag.String("oom_contract_type", "go", "contract type suffix for wasm path (name-type.wasm)")
	oomContractCount    = flag.Int("oom_contract_count", 1, "number of contracts (pools) to load")
	oomDuration         = flag.Duration("oom_duration", 0, "max wait time; 0 means until process exit (OOM/killed)")
	oomSampleInterval   = flag.Duration("oom_sample_interval", 5*time.Second, "memory/pool stats log interval")
	oomMemWarnMB        = flag.Uint64("oom_mem_warn_mb", 2048, "log warning when runtime.MemStats.Sys exceeds this (MB); 0 disables")
	oomUseManager       = flag.Bool("oom_use_manager", true, "load pools via InstancesManager.getVmPool (same as production path)")
)

type poolOOMSnapshot struct {
	time       time.Time
	contract   string
	poolSize   int32
	memAllocMB float64
	memSysMB   float64
	numGC      uint32
	threads    int
}

func readWasmForOOMTest(t *testing.T) []byte {
	t.Helper()
	path := *oomWasmPath
	if path == "" {
		path = fmt.Sprintf("./testdata/%s-%s.wasm", *oomContractName, *oomContractType)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read wasm %q: %v", path, err)
	}
	return data
}

func contractForOOMTest(name, version string) commonPb.Contract {
	return commonPb.Contract{
		Name:        name,
		Version:     version,
		RuntimeType: commonPb.RuntimeType_WASMER,
	}
}

func logPoolOOMSample(t *testing.T, snap poolOOMSnapshot) {
	t.Logf("[oom-sample] time=%s contract=%s poolSize=%d alloc=%.1fMB sys=%.1fMB numGC=%d threads=%d",
		snap.time.Format(time.RFC3339), snap.contract, snap.poolSize,
		snap.memAllocMB, snap.memSysMB, snap.numGC, snap.threads)
	if *oomMemWarnMB > 0 && uint64(snap.memSysMB) >= *oomMemWarnMB {
		t.Logf("[oom-warn] sys=%.1fMB >= warn threshold %dMB", snap.memSysMB, *oomMemWarnMB)
	}
	if snap.threads > 14000 {
		t.Logf("[oom-warn] threads=%d is approaching the limit (ulimit -u=16384)", snap.threads)
	}
}

func sampleMemStats() (allocMB, sysMB float64, numGC uint32) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return float64(mem.Alloc) / 1024 / 1024, float64(mem.Sys) / 1024 / 1024, mem.NumGC
}

// getCurrentThreadCount 读取 /proc/self/status 中的 Threads 字段
func getCurrentThreadCount() int {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "Threads:") {
			var count int
			fmt.Sscanf(line, "Threads: %d", &count)
			return count
		}
	}
	return -1
}

// TestPoolOOMLoadOnly 仅装载合约（编译 wasm、创建 vmPool），不 Invoke、不 GetInstance；
// 串行手动 grow 实例，观察内存与池规模直至 OOM 或超时。
//
// 用法（由 run_testoom.go 调用，或手动）:
//
//	go test -run TestPoolOOMLoadOnly -v -count=1 -timeout 0 \
//	  -args -oom_contract_count=1 -oom_sample_interval=5s
func TestPoolOOMLoadOnly(t *testing.T) {
	wasmBytes := readWasmForOOMTest(t)
	logger := logger2.GetLogger("pool_oom_test")
	contractCount := *oomContractCount
	if contractCount < 1 {
		contractCount = 1
	}

	type loadedPool struct {
		name string
		pool *vmPool
	}
	var pools []loadedPool
	var manager *InstancesManager
	if *oomUseManager {
		manager = NewInstancesManager("oom-chain")
		defer manager.CloseAllVmPool()
	}

	t.Logf("=== pool OOM load-only test ===")
	t.Logf("config: contracts=%d wasm=%s changeSize=%d maxSize=%d minSize=%d sample=%v duration=%v",
		contractCount, *oomWasmPath,
		defaultChangeSize, defaultMaxSize, defaultMinSize, *oomSampleInterval, *oomDuration)
	t.Logf("action: load pools only (no Invoke / no GetInstance), serial manual grow")

	for i := 0; i < contractCount; i++ {
		name := fmt.Sprintf("%s-%03d", *oomContractName, i)
		contract := contractForOOMTest(name, "1.0.0")
		if *oomUseManager {
			pool, err := manager.getVmPool(&contract, wasmBytes)
			if err != nil {
				t.Fatalf("getVmPool %s: %v", name, err)
			}
			pools = append(pools, loadedPool{name: name, pool: pool})
			t.Logf("loaded pool via manager: %s currentSize=%d", name, atomic.LoadInt32(&pool.currentSize))
		} else {
			pool, err := newVmPool(&contract, wasmBytes, logger)
			if err != nil {
				t.Fatalf("newVmPool %s: %v", name, err)
			}
			pool.grow(defaultMinSize)
			pools = append(pools, loadedPool{name: name, pool: pool})
			t.Logf("loaded pool direct: %s currentSize=%d", name, atomic.LoadInt32(&pool.currentSize))
		}
	}

	deadline := time.Time{}
	if *oomDuration > 0 {
		deadline = time.Now().Add(*oomDuration)
		t.Logf("will stop after %v unless process exits earlier", *oomDuration)
	} else {
		t.Logf("no duration limit; run until OOM/kill or manual stop (go test -timeout 0)")
	}

	// ===== 串行创建模式：关闭后台自动 grow，手动逐个创建实例 =====
	// 这样可以大幅降低短时间内创建大量实例导致的线程压力
	t.Logf("[oom-mode] Serial instance creation enabled (grow 1 instance at a time)")

	ticker := time.NewTicker(*oomSampleInterval)
	defer ticker.Stop()

	// 立即打一条 baseline
	for _, lp := range pools {
		alloc, sys, ngc := sampleMemStats()
		threads := getCurrentThreadCount()
		logPoolOOMSample(t, poolOOMSnapshot{
			time: time.Now(), contract: lp.name,
			poolSize: atomic.LoadInt32(&lp.pool.currentSize),
			memAllocMB: alloc, memSysMB: sys, numGC: ngc, threads: threads,
		})
	}

	// 串行创建实例的主循环
	for {
		var totalInstances int32
		createdThisRound := false

		for _, lp := range pools {
			size := atomic.LoadInt32(&lp.pool.currentSize)
			totalInstances += size

			// 串行创建：每次只 grow 1 个实例
			if size < defaultMaxSize {
				lp.pool.grow(1)
				createdThisRound = true
				//time.Sleep(200 * time.Millisecond) // 控制创建节奏，降低线程压力
			}

			alloc, sys, ngc := sampleMemStats()
			threads := getCurrentThreadCount()
			logPoolOOMSample(t, poolOOMSnapshot{
				time: time.Now(), contract: lp.name,
				poolSize: atomic.LoadInt32(&lp.pool.currentSize),
				memAllocMB: alloc, memSysMB: sys, numGC: ngc, threads: threads,
			})

			// 提前检测线程数，防止突然崩溃
			if threads > 14000 {
				t.Logf("[oom-stop] threads=%d reached safety threshold, stopping test to avoid crash", threads)
				return
			}
		}

		t.Logf("[oom-summary] pools=%d totalInstances=%d", len(pools), totalInstances)

		if !createdThisRound {
			t.Logf("[oom-info] All pools have reached max size, stopping creation")
			return
		}

		if !deadline.IsZero() && time.Now().After(deadline) {
			t.Logf("duration reached, stopping without error (not OOM)")
			return
		}

		// 等待下一个采样周期
		select {
		case <-ticker.C:
		}
	}
}
