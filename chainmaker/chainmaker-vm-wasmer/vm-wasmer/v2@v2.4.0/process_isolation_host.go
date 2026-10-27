/*
Copyright (C) BABEC. All rights reserved.
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wasmer

import (
	"bufio"
	"errors"
	"io"
	"sync"

	"chainmaker.org/chainmaker/common/v2/serialize"
	"chainmaker.org/chainmaker/protocol/v2"
	wasmer "chainmaker.org/chainmaker/vm-wasmer/v2/wasmer-go"
)

// workerIPC forwards env.sys_call from the worker process to the parent node.
// Large WASM memory travels via processShm; stdin/stdout carries tiny control msgs.
type workerIPC struct {
	mu     sync.Mutex
	stdin  *bufio.Reader
	stdout io.Writer
	shm    *processShm
}

var activeWorkerIPC *workerIPC

func setWorkerIPC(ipc *workerIPC) {
	activeWorkerIPC = ipc
}

func clearWorkerIPC() {
	activeWorkerIPC = nil
}

// dispatchSysCall runs the chain host handler (same logic as in-process sysCall).
// gasIn/gasOut round-trip WASM GetGas across process isolation (sc.Instance is nil on parent).
func dispatchSysCall(memory []byte, requestHeaderBytes, requestBodyBytes []byte, gasIn uint64) (ret int32, gasOut uint64) {
	gasOut = gasIn
	if len(requestHeaderBytes) == 0 {
		log.Error("wasmer-go log >> requestHeader is null.")
		return protocol.ContractSdkSignalResultFail, gasOut
	}
	requestHeader := serialize.NewEasyCodecWithBytes(requestHeaderBytes)
	ctxPtrVal, err := requestHeader.GetValue("ctx_ptr", serialize.EasyKeyType_SYSTEM)
	if err != nil {
		log.Error("get ctx_ptr failed requestHeader=%s requestBody=%s err=%v",
			string(requestHeaderBytes), string(requestBodyBytes), err)
		return protocol.ContractSdkSignalResultFail, gasOut
	}
	method, err := requestHeader.GetValue("method", serialize.EasyKeyType_SYSTEM)
	if err != nil {
		log.Error("get method failed requestHeader=%s requestBody=%s err=%v",
			string(requestHeaderBytes), string(requestBodyBytes), err)
		return protocol.ContractSdkSignalResultFail, gasOut
	}
	ctxPtr, ok := ctxPtrVal.(int32)
	if !ok {
		return protocol.ContractSdkSignalResultFail, gasOut
	}
	vbm := GetVmBridgeManager()
	simContext := vbm.get(ctxPtr)
	if simContext == nil {
		log.Errorf("dispatchSysCall: simContext nil for ctx_ptr=%d", ctxPtr)
		return protocol.ContractSdkSignalResultFail, gasOut
	}
	simContext.GasRemaining = gasIn
	waciInstance := &WaciInstance{
		Sc:          simContext,
		RequestBody: requestBodyBytes,
		Memory:      memory,
		ChainId:     simContext.ChainId,
	}
	ret = waciInstance.invoke(method)
	if ret == protocol.ContractSdkSignalResultFail {
		log.Infof("invoke WaciInstance error: method = %v", method)
	}
	return ret, simContext.GasRemaining
}

// sysCallProcessWorker is the worker-side sys_call when IPC is active.
func sysCallProcessWorker(environment interface{}, args []wasmer.Value) ([]wasmer.Value, error) {
	ipc := activeWorkerIPC
	if ipc == nil {
		return nil, errors.New("process worker IPC not initialized")
	}
	env, ok := environment.(*CMEnvironment)
	if !ok {
		return nil, errors.New("args 'environment' is not *CMEnvironment type")
	}
	instance := env.instance
	if instance == nil {
		return nil, errors.New("instance at Environment is nil")
	}
	exportMemory, err := instance.Exports.GetMemory("memory")
	if err != nil {
		return nil, err
	}
	requestHeaderPtr := args[0].I32()
	requestHeaderLen := args[1].I32()
	requestBodyPtr := args[2].I32()
	requestBodyLen := args[3].I32()
	mem := exportMemory.Data()

	ret, err := ipc.forwardHostCall(instance, mem, requestHeaderPtr, requestHeaderLen, requestBodyPtr, requestBodyLen)
	if err != nil {
		return nil, err
	}
	return []wasmer.Value{wasmer.NewValue(ret, wasmer.I32)}, nil
}

func (ipc *workerIPC) forwardHostCall(
	instance *wasmer.Instance,
	memory []byte,
	headerOff, headerLen, bodyOff, bodyLen int32,
) (int32, error) {
	ipc.mu.Lock()
	defer ipc.mu.Unlock()

	if ipc.shm == nil {
		return 0, errors.New("process worker shm not initialized")
	}
	if err := ipc.shm.PutMemory(memory, headerOff, headerLen, bodyOff, bodyLen); err != nil {
		return 0, err
	}
	gasRem, gerr := getGasByExport(instance)
	if gerr != nil {
		return 0, gerr
	}
	// Tiny control message only — no MemoryB64 / HeaderB64 / BodyB64.
	if err := writeProcessMsg(ipc.stdout, processMsg{
		Op: procOpHostCall, OK: true, GasRemaining: gasRem,
	}); err != nil {
		return 0, err
	}
	resp, err := readProcessMsg(ipc.stdin, 0)
	if err != nil {
		return 0, err
	}
	if resp.Op != procOpHostResp || !resp.OK {
		if resp.Error != "" {
			return 0, errors.New(resp.Error)
		}
		return 0, errors.New("host_resp not ok")
	}
	if err := ipc.shm.CopyMemoryOut(memory); err != nil {
		return 0, err
	}
	// Apply gas possibly updated by CallContract on the parent.
	if err := setGasByExport(instance, resp.GasRemaining); err != nil {
		return 0, err
	}
	ret := resp.Ret
	if ret == 0 {
		ret = ipc.shm.Ret()
	}
	return ret, nil
}

// handleParentHostCall services one host_call from worker during invoke.
func handleParentHostCall(msg *processMsg, shm *processShm) (*processMsg, error) {
	if shm != nil {
		memory, header, body, err := shm.RequestView()
		if err != nil {
			return nil, err
		}
		ret, gasOut := dispatchSysCall(memory, header, body, msg.GasRemaining)
		shm.SetRet(ret)
		return &processMsg{Op: procOpHostResp, OK: true, Ret: ret, GasRemaining: gasOut}, nil
	}
	// Legacy fallback (tests / old workers): base64 over JSON.
	header, err := b64dec(msg.HeaderB64)
	if err != nil {
		return nil, err
	}
	body, err := b64dec(msg.BodyB64)
	if err != nil {
		return nil, err
	}
	memory, err := b64dec(msg.MemoryB64)
	if err != nil {
		return nil, err
	}
	ret, gasOut := dispatchSysCall(memory, header, body, msg.GasRemaining)
	return &processMsg{
		Op:           procOpHostResp,
		OK:           true,
		Ret:          ret,
		GasRemaining: gasOut,
		MemoryB64:    b64enc(memory),
	}, nil
}
