/*
Copyright (C) BABEC. All rights reserved.
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wasmer

import (
	"fmt"
	"strconv"
	"time"

	"chainmaker.org/chainmaker/common/v2/serialize"
	commonPb "chainmaker.org/chainmaker/pb-go/v2/common"
	"chainmaker.org/chainmaker/protocol/v2"
	wasmergo "chainmaker.org/chainmaker/vm-wasmer/v2/wasmer-go"
)

type processInvokeResult struct {
	GasRemaining uint64
	Err          error
}

// invokeContract runs one contract call inside the worker (parent side IPC).
// Holds w.mu for the whole session so create/destroy cannot interleave.
// Nested invoke on the *same* worker (cross-contract to same pool) is not supported.
func (w *processWorker) invokeContract(
	instanceID string,
	method string,
	parameters map[string][]byte,
	gasUsed uint64,
	ctxPtr int32,
) (*processInvokeResult, error) {
	req := processMsg{
		Op:         procOpInvoke,
		InstanceID: instanceID,
		Method:     method,
		Params:     paramsToWire(parameters),
		GasUsed:    gasUsed,
		CtxPtr:     ctxPtr,
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.dead || w.stdin == nil || w.stdout == nil {
		return nil, fmt.Errorf("process worker closed")
	}
	if err := writeProcessMsg(w.stdin, req); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(30 * time.Minute)
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("invoke timeout")
		}
		msg, err := readProcessMsg(w.stdout, 2*time.Minute)
		if err != nil {
			return nil, err
		}
		switch msg.Op {
		case procOpHostCall:
			resp, herr := handleParentHostCall(msg, w.shm)
			if herr != nil {
				resp = &processMsg{Op: procOpHostResp, OK: false, Error: herr.Error()}
			}
			if werr := writeProcessMsg(w.stdin, *resp); werr != nil {
				return nil, werr
			}
		case procOpInvokeDone:
			if !msg.OK {
				if msg.Error != "" {
					return nil, fmt.Errorf("%s", msg.Error)
				}
				return nil, fmt.Errorf("invoke_done not ok")
			}
			out := &processInvokeResult{GasRemaining: msg.GasRemaining}
			if msg.InvokeErr != "" {
				out.Err = fmt.Errorf("%s", msg.InvokeErr)
			}
			return out, nil
		default:
			// ignore unexpected ops
		}
	}
}

// runInvokeOnWorker executes invoke inside worker (called from worker main loop).
func runInvokeOnWorker(
	instance *wasmergo.Instance,
	method string,
	parameters map[string][]byte,
	gasUsed uint64,
	ctxPtr int32,
	ipc *workerIPC,
) processMsg {
	setWorkerIPC(ipc)
	defer clearWorkerIPC()

	gasLimit := runtimeGasLimit
	availableGas := gasLimit
	if gasUsed < gasLimit {
		availableGas = gasLimit - gasUsed
	} else {
		availableGas = 0
	}
	if err := setGasByExport(instance, availableGas); err != nil {
		return processMsg{Op: procOpInvokeDone, OK: false, Error: err.Error()}
	}

	payload, err := buildInvokePayload(instance, parameters, ctxPtr)
	if err != nil {
		return processMsg{Op: procOpInvokeDone, OK: false, Error: err.Error()}
	}
	callErr := callContractWithPayload(instance, method, payload)

	gasRemaining, gerr := getGasByExport(instance)
	if gerr != nil {
		return processMsg{Op: procOpInvokeDone, OK: false, Error: gerr.Error()}
	}

	resp := processMsg{
		Op:           procOpInvokeDone,
		OK:           true,
		GasRemaining: gasRemaining,
	}
	if callErr != nil {
		resp.InvokeErr = callErr.Error()
	}
	return resp
}

// buildInvokePayload mirrors SimContext.CallMethod payload preparation.
func buildInvokePayload(instance *wasmergo.Instance, parameters map[string][]byte, ctxPtr int32) ([]byte, error) {
	runtimeFn, err := instance.Exports.GetRawFunction(protocol.ContractRuntimeTypeMethod)
	if err != nil {
		return nil, fmt.Errorf("method [%s] not export, err = %v", protocol.ContractRuntimeTypeMethod, err)
	}
	defer runtimeFn.Close()
	sdkType, err := runtimeFn.Call()
	if err != nil {
		return nil, err
	}
	runtimeSdkType, ok := sdkType.(int32)
	if !ok {
		return nil, fmt.Errorf("sdkType is not int32 type")
	}
	if int32(commonPb.RuntimeType_WASMER) != runtimeSdkType && int32(commonPb.RuntimeType_GASM) != runtimeSdkType {
		return nil, fmt.Errorf("runtime type error, expect rust:[%d], but got %d",
			uint64(commonPb.RuntimeType_WASMER), runtimeSdkType)
	}
	if parameters == nil {
		parameters = make(map[string][]byte)
	}
	parameters[protocol.ContractContextPtrParam] = []byte(strconv.Itoa(int(ctxPtr)))
	parameters["CONTRACT_BYTECODE"] = []byte("")
	ec := serialize.NewEasyCodecWithMap(parameters)
	return ec.Marshal(), nil
}

// callContractWithPayload mirrors SimContext.callContract after payload is ready.
func callContractWithPayload(instance *wasmergo.Instance, methodName string, bytes []byte) error {
	lengthOfSubject := len(bytes)

	allocateFunc, err := instance.Exports.GetRawFunction(protocol.ContractAllocateMethod)
	if err != nil {
		return fmt.Errorf("method [%s] not export, err = %v", protocol.ContractAllocateMethod, err)
	}
	defer allocateFunc.Close()

	allocateResult, err := allocateFunc.Call(lengthOfSubject)
	if err != nil {
		return fmt.Errorf("%s invoke failed. There may not be enough memory or CPU", protocol.ContractAllocateMethod)
	}
	dataPtr, ok := allocateResult.(int32)
	if !ok {
		return fmt.Errorf("allocateResult is not int32 type")
	}

	exportMemory, err := instance.Exports.GetMemory("memory")
	if err != nil {
		return fmt.Errorf("[%s] can't get exported memory, err = %v", protocol.ContractAllocateMethod, err)
	}
	memory := exportMemory.Data()[dataPtr:]
	for nth := 0; nth < lengthOfSubject; nth++ {
		memory[nth] = bytes[nth]
	}

	exportFunc, err := instance.Exports.GetRawFunction(methodName)
	if err != nil {
		return fmt.Errorf("find method [%s] failed, err = %v", methodName, err)
	}
	defer exportFunc.Close()
	_, err = exportFunc.Call()
	return err
}
