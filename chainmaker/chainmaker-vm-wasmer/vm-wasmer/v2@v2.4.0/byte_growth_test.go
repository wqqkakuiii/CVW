/*
Copyright (C) BABEC. All rights reserved.
SPDX-License-Identifier: Apache-2.0
*/

package wasmer

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"testing"

	"chainmaker.org/chainmaker/logger/v2"
	commonPb "chainmaker.org/chainmaker/pb-go/v2/common"
	"chainmaker.org/chainmaker/protocol/v2"
)

// TestByteGrowthPerInvoke prints live Go heap and glibc in-use bytes after
// each call. RSS cannot show a few dozen bytes; HeapAlloc and mallinfo2 can.
//
//	go test -run TestByteGrowthPerInvoke -v -count=1 -timeout 20m
func TestByteGrowthPerInvoke(t *testing.T) {
	os.Setenv("CM_WASMER_FORCE_NO_REUSE", "0")

	contractName := "compute"
	method := "normalCal"
	ContractName = contractName
	filePath := prepareFile(contractName, "go")
	wasmBytes, contractId, logger := prepareContract(filePath, t)
	contractId.Name = contractName

	pool, err := newVmPool(&contractId, wasmBytes, logger)
	if err != nil {
		t.Fatalf("newVmPool: %v", err)
	}
	defer pool.close()

	inst := pool.GetInstance()
	baseParams := prepareFunc(contractName, method)
	fillingBaseParams(baseParams)

	sample := func(tag string, i int) {
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		native := nativeInUseBytes()
		t.Logf("%s i=%02d heap_alloc=%d heap_objects=%d mallocs=%d uordblks=%d",
			tag, i, m.HeapAlloc, m.HeapObjects, m.Mallocs, native)
	}

	for i := 0; i < 3; i++ {
		invokeOnce(t, inst, &contractId, method, baseParams, logger, i)
	}
	debug.FreeOSMemory()
	sample("reuse-base", 0)
	mallocMark("arm")

	var prevHeap, prevNative uint64
	for i := 1; i <= 20; i++ {
		invokeOnce(t, inst, &contractId, method, baseParams, logger, 100+i)
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		native := nativeInUseBytes()
		if prevHeap == 0 {
			prevHeap, prevNative = m.HeapAlloc, native
		}
		t.Logf("reuse i=%02d heap_alloc=%d d_heap=%+d heap_objects=%d uordblks=%d d_native=%+d",
			i, m.HeapAlloc, int64(m.HeapAlloc)-int64(prevHeap), m.HeapObjects, native, int64(native)-int64(prevNative))
		prevHeap, prevNative = m.HeapAlloc, native
		if i == 1 {
			mallocMark("after-1")
		}
	}

	pool.CloseInstance(inst)
	sample("after-close", 0)

	prevHeap, prevNative = 0, 0
	for i := 1; i <= 20; i++ {
		next, err := pool.newInstanceFromModule()
		if err != nil {
			t.Fatalf("newInstanceFromModule: %v", err)
		}
		invokeOnce(t, next, &contractId, method, baseParams, logger, 200+i)
		pool.CloseInstance(next)
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		native := nativeInUseBytes()
		if prevHeap == 0 {
			prevHeap, prevNative = m.HeapAlloc, native
			t.Logf("noreuse i=%02d heap_alloc=%d heap_objects=%d uordblks=%d",
				i, m.HeapAlloc, m.HeapObjects, native)
			continue
		}
		t.Logf("noreuse i=%02d heap_alloc=%d d_heap=%+d heap_objects=%d uordblks=%d d_native=%+d",
			i, m.HeapAlloc, int64(m.HeapAlloc)-int64(prevHeap), m.HeapObjects, native, int64(native)-int64(prevNative))
		prevHeap, prevNative = m.HeapAlloc, native
	}
}

// TestByteGrowthNoReuse measures bytes left after each create-call-close.
// Instance reuse is off. The calling goroutine stays on one OS thread, so a
// new worker thread's first-touch allocation is not counted as per-call growth.
//
//	go test -run TestByteGrowthNoReuse -v -count=1 -timeout 20m
func TestByteGrowthNoReuse(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	os.Setenv("CM_WASMER_FORCE_NO_REUSE", "1")

	contractName := "compute"
	method := "normalCal"
	ContractName = contractName
	filePath := prepareFile(contractName, "go")
	wasmBytes, contractId, logger := prepareContract(filePath, t)
	contractId.Name = contractName

	pool, err := newVmPool(&contractId, wasmBytes, logger)
	if err != nil {
		t.Fatalf("newVmPool: %v", err)
	}
	defer pool.close()

	baseParams := prepareFunc(contractName, method)
	fillingBaseParams(baseParams)

	cycle := func(i int) {
		t.Helper()
		inst, err := pool.newInstanceFromModule()
		if err != nil {
			t.Fatalf("newInstanceFromModule: %v", err)
		}
		invokeOnce(t, inst, &contractId, method, baseParams, logger, i)
		pool.CloseInstance(inst)
	}

	for i := 0; i < 2; i++ {
		cycle(i)
	}
	runtime.GC()
	runtime.GC()

	var prevHeap, prevNative uint64
	var sumHeap, sumNative int64
	var n int
	for i := 1; i <= 20; i++ {
		cycle(100 + i)
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		native := nativeInUseBytes()
		if prevHeap == 0 {
			prevHeap, prevNative = m.HeapAlloc, native
			t.Logf("noreuse i=%02d heap_alloc=%d heap_objects=%d uordblks=%d",
				i, m.HeapAlloc, m.HeapObjects, native)
			continue
		}
		dHeap := int64(m.HeapAlloc) - int64(prevHeap)
		dNative := int64(native) - int64(prevNative)
		sumHeap += dHeap
		sumNative += dNative
		n++
		t.Logf("noreuse i=%02d heap_alloc=%d d_heap=%+d heap_objects=%d uordblks=%d d_native=%+d",
			i, m.HeapAlloc, dHeap, m.HeapObjects, native, dNative)
		prevHeap, prevNative = m.HeapAlloc, native
	}
	t.Logf("noreuse mean over %d steps d_heap=%+d d_native=%+d", n, sumHeap/int64(n), sumNative/int64(n))
}

func invokeOnce(t *testing.T, inst *wrappedInstance, contractId *commonPb.Contract, method string, baseParams map[string][]byte, logger *logger.CMLogger, i int) {
	t.Helper()
	params := cloneParams(baseParams)
	params[protocol.ContractTxIdParam] = []byte(fmt.Sprintf("BYTE_GROWTH_%d", i))
	txCtx := prepareTxSimContext(ChainId, BlockVersion, contractId.Name, method, params, SnapshotMock{})
	_, code, msg := invokeOnFixedInstance(inst, contractId, method, params, txCtx, logger)
	if code != 0 {
		t.Fatalf("invoke i=%d code=%d msg=%s", i, code, msg)
	}
}
