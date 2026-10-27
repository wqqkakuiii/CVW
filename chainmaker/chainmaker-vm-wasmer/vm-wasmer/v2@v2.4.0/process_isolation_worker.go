/*
Copyright (C) BABEC. All rights reserved.
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wasmer

import (
	"bufio"
	"fmt"
	"os"

	wasmergo "chainmaker.org/chainmaker/vm-wasmer/v2/wasmer-go"
)

type workerHeldInstance struct {
	id           string
	wasmInstance *wasmergo.Instance
	store        *wasmergo.Store
	wasiEnv      *wasmergo.WasiEnvironment
}

// RunProcessIsolationWorker deserializes Module once, then serves create/destroy
// for many instances until exit. Stdout must stay JSON-only (stderr for logs).
func RunProcessIsolationWorker() int {
	artifact := os.Getenv(envProcessArtifact)
	if artifact == "" {
		writeWorkerMsg(processMsg{Op: procOpReady, OK: false, Error: "missing WASMER_PROCESS_ARTIFACT"})
		return 2
	}
	var shm *processShm
	if shmPath := os.Getenv(envProcessShm); shmPath != "" {
		s, err := openProcessShm(shmPath)
		if err != nil {
			writeWorkerMsg(processMsg{Op: procOpReady, OK: false, Error: "open shm: " + err.Error()})
			return 2
		}
		shm = s
		defer func() { _ = shm.Close() }()
	}
	bytes, err := os.ReadFile(artifact)
	if err != nil {
		writeWorkerMsg(processMsg{Op: procOpReady, OK: false, Error: err.Error()})
		return 2
	}

	engine, store, err := newWasmerEngineAndStoreForPool()
	if err != nil {
		writeWorkerMsg(processMsg{Op: procOpReady, OK: false, Error: err.Error()})
		return 2
	}
	defer func() {
		store.Close()
		engine.Close()
	}()

	module, err := wasmergo.DeserializeModule(store, bytes)
	if err != nil {
		writeWorkerMsg(processMsg{Op: procOpReady, OK: false, Error: "deserialize: " + err.Error()})
		return 2
	}
	defer module.Close()

	held := make(map[string]*workerHeldInstance)
	defer func() {
		for id, h := range held {
			closeHeldInstance(h)
			delete(held, id)
		}
	}()

	writeWorkerMsg(processMsg{Op: procOpReady, OK: true, PID: os.Getpid(), Active: 0})

	in := bufio.NewReader(os.Stdin)
	for {
		msg, err := readProcessMsg(in, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "worker stdin: %v\n", err)
			return 1
		}
		switch msg.Op {
		case procOpPing:
			writeWorkerMsg(processMsg{Op: procOpPing, OK: true, PID: os.Getpid(), Active: len(held)})
		case procOpStats:
			writeWorkerMsg(workerStatsMsg(len(held)))
		case procOpCreate:
			id, cerr := createHeldInstance(module, store, msg.InstanceID)
			if cerr != nil {
				writeWorkerMsg(processMsg{Op: procOpCreate, OK: false, Error: cerr.Error(), Active: len(held)})
				continue
			}
			held[id.id] = id
			snap := SampleResourceNoGC("worker_create")
			writeWorkerMsg(processMsg{
				Op:         procOpCreate,
				OK:         true,
				PID:        os.Getpid(),
				InstanceID: id.id,
				Active:     len(held),
				Threads:    snap.Threads,
				VmRSSKB:    snap.VmRSSKB,
				AnonRSSKB:  snap.AnonRSSKB,
			})
		case procOpDestroy:
			h := held[msg.InstanceID]
			if h == nil {
				writeWorkerMsg(processMsg{
					Op: procOpDestroy, OK: false,
					Error: "unknown instance_id", Active: len(held),
				})
				continue
			}
			closeHeldInstance(h)
			delete(held, msg.InstanceID)
			snap := SampleResourceNoGC("worker_destroy")
			writeWorkerMsg(processMsg{
				Op:         procOpDestroy,
				OK:         true,
				PID:        os.Getpid(),
				InstanceID: msg.InstanceID,
				Active:     len(held),
				Threads:    snap.Threads,
				VmRSSKB:    snap.VmRSSKB,
				AnonRSSKB:  snap.AnonRSSKB,
			})
		case procOpInvoke:
			h := held[msg.InstanceID]
			if h == nil {
				writeWorkerMsg(processMsg{
					Op: procOpInvokeDone, OK: false,
					Error: "unknown instance_id", Active: len(held),
				})
				continue
			}
			params, perr := paramsFromWire(msg.Params)
			if perr != nil {
				writeWorkerMsg(processMsg{
					Op: procOpInvokeDone, OK: false,
					Error: "params: " + perr.Error(), Active: len(held),
				})
				continue
			}
			ipc := &workerIPC{stdin: in, stdout: os.Stdout, shm: shm}
			resp := runInvokeOnWorker(h.wasmInstance, msg.Method, params, msg.GasUsed, msg.CtxPtr, ipc)
			resp.Active = len(held)
			writeWorkerMsg(resp)
		case procOpExit:
			for id, h := range held {
				closeHeldInstance(h)
				delete(held, id)
			}
			writeWorkerMsg(processMsg{Op: procOpExit, OK: true, Active: 0})
			return 0
		default:
			writeWorkerMsg(processMsg{Op: msg.Op, OK: false, Error: "unknown op", Active: len(held)})
		}
	}
}

func writeWorkerMsg(m processMsg) {
	_ = writeProcessMsg(os.Stdout, m)
}

func workerStatsMsg(active int) processMsg {
	snap := SampleResourceNoGC("worker_stats")
	return processMsg{
		Op: procOpStats, OK: true, PID: os.Getpid(),
		Active: active, Threads: snap.Threads,
		VmRSSKB: snap.VmRSSKB, AnonRSSKB: snap.AnonRSSKB,
	}
}

func createHeldInstance(module *wasmergo.Module, moduleStore *wasmergo.Store, wantID string) (*workerHeldInstance, error) {
	instStore := wasmergo.CloneStore(moduleStore)
	wasiEnv, err := wasmergo.NewWasiStateBuilder("wasi-program").Finalize(instStore.Inner())
	if err != nil {
		instStore.Close()
		return nil, fmt.Errorf("wasi: %w", err)
	}
	importObject, err := wasiEnv.GenerateImportObject(instStore, module)
	if err != nil {
		wasiEnv.Close()
		instStore.Close()
		return nil, fmt.Errorf("imports wasi: %w", err)
	}
	env := CMEnvironment{}
	vb := GetVmBridgeManager()
	imports, err := vb.GetImports(instStore, &env, importObject)
	if imports == nil && err != nil {
		wasiEnv.Close()
		instStore.Close()
		return nil, fmt.Errorf("imports env: %w", err)
	}
	wasmInstance, err := wasmergo.NewInstanceByStore(module, imports, instStore)
	if err != nil {
		wasiEnv.Close()
		instStore.Close()
		return nil, fmt.Errorf("instance: %w", err)
	}
	if err := wasiEnv.Initialize(instStore, wasmInstance); err != nil {
		wasmInstance.Close()
		wasiEnv.Close()
		instStore.Close()
		return nil, fmt.Errorf("wasi init: %w", err)
	}
	if start, _ := wasmInstance.Exports.GetWasiStartRawFunction(); start != nil {
		_, _ = start.Call()
		start.Close()
	}
	env.instance = wasmInstance
	env.memory, _ = wasmInstance.Exports.GetMemory("memory")

	id := wantID
	if id == "" {
		id = fmt.Sprintf("%d-%p", os.Getpid(), wasmInstance)
	}
	return &workerHeldInstance{
		id:           id,
		wasmInstance: wasmInstance,
		store:        instStore,
		wasiEnv:      wasiEnv,
	}, nil
}

func closeHeldInstance(h *workerHeldInstance) {
	if h == nil {
		return
	}
	if h.wasmInstance != nil {
		_ = CallDeallocate(h.wasmInstance)
		h.wasmInstance.Close()
		h.wasmInstance = nil
	}
	if h.wasiEnv != nil {
		h.wasiEnv.Close()
		h.wasiEnv = nil
	}
	if h.store != nil {
		h.store.Close()
		h.store = nil
	}
	wasmergo.DrainStackPool()
	wasmergo.MallocTrim()
}
