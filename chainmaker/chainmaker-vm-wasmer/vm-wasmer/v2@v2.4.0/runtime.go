/*
Copyright (C) BABEC. All rights reserved.
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wasmer

import (
	"fmt"
	"time"

	"chainmaker.org/chainmaker/logger/v2"
	commonPb "chainmaker.org/chainmaker/pb-go/v2/common"
	"chainmaker.org/chainmaker/protocol/v2"
	"chainmaker.org/chainmaker/utils/v2"
	wasmer "chainmaker.org/chainmaker/vm-wasmer/v2/wasmer-go"
)

const (
	// InitContractFunc means `init_contract` function name
	InitContractFunc = "init_contract"
	// UpgradeContractFunc means `upgrade` function name
	UpgradeContractFunc = "upgrade"
	// runtimeGasLimit is the unified gas limit used by runtime.
	runtimeGasLimit uint64 = 1<<31 - 1
)

// RuntimeInstance wasm runtime
type RuntimeInstance struct {
	pool             *vmPool
	log              *logger.CMLogger
	chainId          string
	instancesManager *InstancesManager
}

// Pool comment at next version
// nolint:revive
func (r *RuntimeInstance) Pool() *vmPool {
	return r.pool
}

// Invoke contract by call vm, implement protocol.RuntimeInstance
func (r *RuntimeInstance) Invoke(contract *commonPb.Contract, method string, byteCode []byte,
	parameters map[string][]byte, txContext protocol.TxSimContext, gasUsed uint64) (
	contractResult *commonPb.ContractResult, specialTxType protocol.ExecOrderTxType) {

	r.log.Debugf("called invoke for tx:%s", txContext.GetTx().Payload.TxId)
	logStr := fmt.Sprintf("wasmer runtime invoke[%s]: ", txContext.GetTx().Payload.TxId)
	startTime := utils.CurrentTimeMillisSeconds()
	blockVersion := txContext.GetBlockVersion()

	// set default return value
	contractResult = &commonPb.ContractResult{
		Code:    uint32(0),
		Result:  nil,
		Message: "",
	}
	specialTxType = protocol.ExecOrderTxTypeNormal

	reuse, reuseErr := resolveInstanceReuse(contract, method, parameters, txContext)
	if reuseErr != nil {
		contractResult.Code = 1
		contractResult.Message = reuseErr.Error()
		r.log.Errorf("resolve instance reuse failed, contract:%s, err:%v", contract.GetName(), reuseErr)
		return
	}
	if method == InitContractFunc || method == UpgradeContractFunc {
		r.log.Infof("contract[%s] instance reuse=%v", contract.GetName(), reuse)
	}

	var instanceInfo *wrappedInstance
	defer func() {
		endTime := utils.CurrentTimeMillisSeconds()
		logStr = fmt.Sprintf("%s used time %d", logStr, endTime-startTime)
		r.log.Debugf(logStr)
		panicErr := recover()
		if panicErr != nil {
			contractResult.Code = 1
			contractResult.Message = fmt.Sprint(panicErr)
			r.log.Infof("panic recover err:%v", panicErr)
			if instanceInfo != nil {
				instanceInfo.errCount++
			}
			specialTxType = protocol.ExecOrderTxTypeNormal
		}
	}()

	// if cross contract call, then new instance
	if txContext.GetDepth() > 0 {
		//var err error
		//instanceInfo, err = r.pool.NewInstance()
		//defer r.pool.CloseInstance(instanceInfo)
		//if err != nil {
		//	panic(err)
		//}
		r.log.Debugf("depth>0 before get instance for tx: %s", txContext.GetTx().Payload.TxId)
		instanceInfo = r.pool.GetInstance()
		r.log.Debugf("depth>0 after get instance for tx: %s", txContext.GetTx().Payload.TxId)
		instanceInfo.setRevertMode(reuse)
		defer r.pool.RevertInstance(instanceInfo)
	} else {
		r.log.Debugf("before get instance for tx: %s", txContext.GetTx().Payload.TxId)
		instanceInfo = r.pool.GetInstance()
		r.log.Debugf("after get instance for tx: %s", txContext.GetTx().Payload.TxId)
		instanceInfo.setRevertMode(reuse)
		defer r.pool.RevertInstance(instanceInfo)
	}

	var sc = NewSimContext(method, r.log, r.chainId)
	defer sc.removeCtxPointer()
	sc.Contract = contract
	sc.TxSimContext = txContext
	sc.ContractResult = contractResult
	sc.parameters = parameters
	sc.SpecialTxType = protocol.ExecOrderTxTypeNormal

	gasLimit := runtimeGasLimit
	var (
		err          error
		gasRemaining uint64
	)

	if instanceInfo.processWorker != nil {
		// Process isolation: execute inside worker; sys_call forwarded via shm IPC.
		result, ierr := instanceInfo.processWorker.invokeContract(
			instanceInfo.processInstID, method, parameters, gasUsed, sc.CtxPtr)
		if ierr != nil {
			panic(ierr)
		}
		err = result.Err
		gasRemaining = result.GasRemaining
		if err != nil {
			r.log.Infof("contract invoke failed, %s, tx: %s", err, txContext.GetTx().Payload.TxId)
		}
		specialTxType = sc.SpecialTxType
	} else {
		instance := instanceInfo.wasmInstance
		sc.Instance = instance
		availableGas := gasLimit
		if gasUsed < gasLimit {
			availableGas = gasLimit - gasUsed
		} else {
			availableGas = 0
		}
		r.log.Debugf("gasLimit:%d", availableGas)
		setGasErr := setGasByExport(instance, availableGas)
		if setGasErr != nil {
			panic(setGasErr)
		}

		//运行中如果死循环或者gas超值，它会自动中止，返回unreachable的err
		err = sc.CallMethod(instance)
		if err != nil {
			r.log.Infof("contract invoke failed, %s, tx: %s", err, txContext.GetTx().Payload.TxId)
		}
		specialTxType = sc.SpecialTxType

		var gerr error
		gasRemaining, gerr = getGasByExport(instance)
		if gerr != nil {
			panic(gerr)
		}
	}

	//这里用于判断是否属于gas超支的err
	//注意这里判断条件是有问题的，以前是判断GetGasRemaining<=0，但是gasremaining是uint64无符号，
	//所以如果gas消耗完，要么是GetGasRemaining=0或GetGasRemaining=uint64的max
	if gasRemaining == 0 || gasRemaining == ^uint64(0) {
		err = fmt.Errorf("contract invoke failed, out of gas %d, tx: %s", runtimeGasLimit,
			txContext.GetTx().Payload.TxId)
	}
	gas := gasLimit - gasRemaining
	logStr += fmt.Sprintf("used gas %d ", gas)
	contractResult.GasUsed = gas

	if err != nil {
		contractResult.Code = 1
		msg := fmt.Sprintf("contract invoke failed, %s, tx: %s", err.Error(), txContext.GetTx().Payload.TxId)
		r.log.Infof(msg)
		contractResult.Message = msg
		if method == InitContractFunc && txContext.GetBlockVersion() >= 2201 {
			r.instancesManager.CloseAVmPool(contract)
		} else {
			instanceInfo.errCount++
		}
		return
	} else if blockVersion >= 2030100 && contractResult.Code != 0 {
		if method == InitContractFunc || method == UpgradeContractFunc {
			r.instancesManager.CloseAVmPool(contract)
		} else {
			instanceInfo.errCount++
		}
	}
	contractResult.ContractEvent = sc.ContractEvent
	contractResult.GasUsed = gas
	r.log.Infof("contract invoke finished, tx:%s, contractName:%s, contractMethod:%s, runtimeContractResult:%d, gasUsed:%d",
		txContext.GetTx().Payload.TxId, contract.Name, method, sc.ContractResult.Code, gas)

	return
}

func setGasByExport(instance *wasmer.Instance, gas uint64) error {
	setGasFn, err := instance.Exports.GetRawFunction("SetGas")
	if err != nil {
		return err
	}
	defer setGasFn.Close()

	_, err = setGasFn.Call(int64(gas))
	return err
}

func getGasByExport(instance *wasmer.Instance) (uint64, error) {
	getGasFn, err := instance.Exports.GetRawFunction("GetGas")
	if err != nil {
		return 0, err
	}
	defer getGasFn.Close()

	result, err := getGasFn.Call()
	if err != nil {
		return 0, err
	}

	switch v := result.(type) {
	case int32:
		return uint64(v), nil
	case uint32:
		return uint64(v), nil
	case int64:
		return uint64(v), nil
	case uint64:
		return v, nil
	default:
		return 0, fmt.Errorf("GetGas return type %T not supported", result)
	}
}

// Invoke contract by call vm, implement protocol.RuntimeInstance
func (r *RuntimeInstance) InvokeTime(contract *commonPb.Contract, method string, byteCode []byte,
	parameters map[string][]byte, txContext protocol.TxSimContext, gasUsed uint64) (
	contractResult *commonPb.ContractResult, specialTxType protocol.ExecOrderTxType, startTime, endTime int64, executionTime float64, callMethodTime float64) {
	//fmt.Println(txContext)
	txId := txContext.GetTx().Payload.TxId
	contractName := contract.Name
	//r.log.Debugf("called invoke for tx:%s", txId)
	logStr := fmt.Sprintf("wasmer runtime invoke[%s]: ", txContext.GetTx().Payload.TxId)
	//startTime := utils.CurrentTimeMillisSeconds()
	startTime = time.Now().UnixNano()
	//fmt.Printf("startInvoke:%d\n", startTime)
	// set default return value
	contractResult = &commonPb.ContractResult{
		Code:    uint32(0),
		Result:  nil,
		Message: "",
	}
	specialTxType = protocol.ExecOrderTxTypeNormal

	reuse, reuseErr := resolveInstanceReuse(contract, method, parameters, txContext)
	if reuseErr != nil {
		contractResult.Code = 1
		contractResult.Message = reuseErr.Error()
		endTime = time.Now().UnixNano()
		executionTime = float64(endTime-startTime) / 1e9
		return
	}

	var instanceInfo *wrappedInstance
	defer func() {
		//endTime := utils.CurrentTimeMillisSeconds()
		endTime = time.Now().UnixNano()
		//fmt.Printf("endInvoke:%d\n", endTime)
		executionTime = float64(endTime-startTime) / 1e9

		panicErr := recover()
		if panicErr != nil {
			contractResult.Code = 1
			contractResult.Message = fmt.Sprint(panicErr)
			if instanceInfo != nil {
				instanceInfo.errCount++
			}
			specialTxType = protocol.ExecOrderTxTypeNormal
		}
		logStr = fmt.Sprintf("invoke vm, tx id:%s, contractName:%+v, contractMethod:%+v, runtimeContractResult:%d ,startTime:%d, endTime:%d, executionTime:%.6f s, callMethodTime:%.6f s",
			txId, contractName, method, contractResult.Code, startTime, endTime, executionTime, callMethodTime)
		r.log.Debugf(logStr)
	}()

	// if cross contract call, then new instance
	if txContext.GetDepth() > 0 {
		//var err error
		//instanceInfo, err = r.pool.NewInstance()
		//defer r.pool.CloseInstance(instanceInfo)
		//if err != nil {
		//	panic(err)
		//}
		//r.log.Debugf("before get instance for tx: %s", txContext.GetTx().Payload.TxId)
		instanceInfo = r.pool.GetInstance()
		//r.log.Debugf("after get instance for tx: %s", txContext.GetTx().Payload.TxId)
		instanceInfo.setRevertMode(reuse)
		defer r.pool.RevertInstance(instanceInfo)
	} else {
		//r.log.Debugf("before get instance for tx: %s", txContext.GetTx().Payload.TxId)
		instanceInfo = r.pool.GetInstance()
		//r.log.Debugf("after get instance for tx: %s", txContext.GetTx().Payload.TxId)
		instanceInfo.setRevertMode(reuse)
		defer r.pool.RevertInstance(instanceInfo)
	}
	instanceInfoSnapshot := *instanceInfo
	defer func() {
		if instanceInfo == nil || instanceInfo.wasmInstance == nil {
			return
		}
		r.log.Infof("instance_compare tx:%s contract:%s method:%s instanceId:%s same_all_fields:%t",
			txId, contractName, method, instanceInfo.id, instanceInfoSnapshot == *instanceInfo)
	}()

	var sc = NewSimContext(method, r.log, r.chainId)
	defer sc.removeCtxPointer()
	sc.Contract = contract
	sc.TxSimContext = txContext
	sc.ContractResult = contractResult
	sc.parameters = parameters
	sc.SpecialTxType = protocol.ExecOrderTxTypeNormal

	gasLimit := runtimeGasLimit
	var (
		err          error
		gasRemaining uint64
	)

	if instanceInfo.processWorker != nil {
		func() {
			t0 := time.Now()
			defer func() { callMethodTime = time.Since(t0).Seconds() }()
			result, ierr := instanceInfo.processWorker.invokeContract(
				instanceInfo.processInstID, method, parameters, gasUsed, sc.CtxPtr)
			if ierr != nil {
				panic(ierr)
			}
			err = result.Err
			gasRemaining = result.GasRemaining
		}()
		specialTxType = sc.SpecialTxType
	} else {
		instance := instanceInfo.wasmInstance
		sc.Instance = instance
		availableGas := gasLimit
		if gasUsed < gasLimit {
			availableGas = gasLimit - gasUsed
		} else {
			availableGas = 0
		}
		r.log.Debugf("gasLimit:%d", availableGas)
		setGasErr := setGasByExport(instance, availableGas)
		if setGasErr != nil {
			panic(setGasErr)
		}

		func() {
			t0 := time.Now()
			defer func() { callMethodTime = time.Since(t0).Seconds() }()
			err = sc.CallMethod(instance)
		}()

		specialTxType = sc.SpecialTxType

		var gerr error
		gasRemaining, gerr = getGasByExport(instance)
		if gerr != nil {
			panic(gerr)
		}
	}

	//注意这里判断条件是有问题的，以前是判断GetGasRemaining<=0，但是gasremaining是uint64无符号，
	//所以如果gas消耗完，要么是GetGasRemaining=0或GetGasRemaining=uint64的max
	if gasRemaining == 0 || gasRemaining == ^uint64(0) {
		err = fmt.Errorf("contract invoke failed, out of gas %d, tx: %s", runtimeGasLimit,
			txContext.GetTx().Payload.TxId)
	}
	gas := gasLimit - gasRemaining
	logStr += fmt.Sprintf("used gas %d ", gas)
	contractResult.GasUsed = gas

	if err != nil {
		contractResult.Code = 1
		msg := fmt.Sprintf("contract invoke failed, %s, tx: %s", err.Error(), txContext.GetTx().Payload.TxId)
		r.log.Debugf(msg)
		contractResult.Message = msg
		if method == "init_contract" && txContext.GetBlockVersion() >= 2201 {
			r.instancesManager.CloseAVmPool(contract)
		} else {
			instanceInfo.errCount++
		}
		return
	}
	contractResult.ContractEvent = sc.ContractEvent
	contractResult.GasUsed = gas

	return
}
