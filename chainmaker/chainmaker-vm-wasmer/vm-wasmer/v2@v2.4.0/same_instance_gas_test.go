/*
Copyright (C) BABEC. All rights reserved.
SPDX-License-Identifier: Apache-2.0
*/

package wasmer

import (
	"flag"
	"fmt"
	"sort"
	"testing"

	"chainmaker.org/chainmaker/logger/v2"
	commonPb "chainmaker.org/chainmaker/pb-go/v2/common"
	"chainmaker.org/chainmaker/protocol/v2"
)

var (
	sameInstN            = flag.Int("same_inst_n", 100, "repeat count on one instance")
	sameInstContract     = flag.String("same_inst_contract", "compute", "contract name (testdata/<name>-go.wasm)")
	sameInstMethod       = flag.String("same_inst_method", "normalCal", "contract method")
	sameInstContractType = flag.String("same_inst_type", "go", "wasm flavor suffix")
)

// TestSameInstanceGasRepeat runs the same contract method N times on ONE
// wrappedInstance (no Revert/Get between calls) and reports gasUsed distribution.
//
//	cd vm-wasmer/v2@v2.4.0
//	go test -run TestSameInstanceGasRepeat -v -count=1 -timeout 30m
//	go test -run TestSameInstanceGasRepeat -v -count=1 -- \
//	  -same_inst_n=100 -same_inst_contract=compute -same_inst_method=normalCal
func TestSameInstanceGasRepeat(t *testing.T) {
	n := *sameInstN
	contractName := *sameInstContract
	method := *sameInstMethod
	ctype := *sameInstContractType

	ContractName = contractName
	filePath := prepareFile(contractName, ctype)
	wasmBytes, contractId, logger := prepareContract(filePath, t)
	contractId.Name = contractName

	pool, err := newVmPool(&contractId, wasmBytes, logger)
	if err != nil {
		t.Fatalf("newVmPool: %v", err)
	}
	defer pool.close()

	inst := pool.GetInstance()
	defer pool.CloseInstance(inst)

	baseParams := prepareFunc(contractName, method)
	fillingBaseParams(baseParams)

	gasDist := make(map[uint64]int)
	gases := make([]uint64, 0, n)
	ok := 0

	t.Logf("instance_id=%s contract=%s method=%s n=%d", inst.id, contractName, method, n)

	for i := 0; i < n; i++ {
		params := cloneParams(baseParams)
		params[protocol.ContractTxIdParam] = []byte(fmt.Sprintf("SAME_INST_TX_%d", i))
		txCtx := prepareTxSimContext(ChainId, BlockVersion, contractName, method, params, SnapshotMock{})

		gas, code, msg := invokeOnFixedInstance(inst, &contractId, method, params, txCtx, logger)
		gases = append(gases, gas)
		gasDist[gas]++
		if code == 0 {
			ok++
		} else if i < 5 || i == n-1 {
			t.Logf("i=%d code=%d gas=%d msg=%s", i, code, gas, msg)
		}
		if i < 10 || i == n-1 || (i+1)%20 == 0 {
			t.Logf("i=%03d gasUsed=%d code=%d", i, gas, code)
		}
	}

	keys := make([]uint64, 0, len(gasDist))
	for g := range gasDist {
		keys = append(keys, g)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	minG, maxG := keys[0], keys[len(keys)-1]
	variant := 0.0
	if minG > 0 {
		variant = float64(maxG-minG) / float64(minG) * 100
	}

	t.Logf("success=%d/%d", ok, n)
	t.Logf("gasDist=%v", gasDist)
	t.Logf("min=%d max=%d delta=%d variant=%.6f%% distinct=%d",
		minG, maxG, maxG-minG, variant, len(gasDist))
	fmt.Printf("=== SameInstanceGasRepeat ===\n")
	fmt.Printf("instance=%s %s.%s n=%d\n", inst.id, contractName, method, n)
	fmt.Printf("gasDist=%v\n", gasDist)
	fmt.Printf("min=%d max=%d delta=%d variant=%.6f%%\n", minG, maxG, maxG-minG, variant)
	for i, g := range gases {
		if i < 20 || i == len(gases)-1 {
			fmt.Printf("  [%03d] %d\n", i, g)
		}
	}
}

func cloneParams(in map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(in))
	for k, v := range in {
		cp := make([]byte, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

// invokeOnFixedInstance mirrors RuntimeInstance.Invoke gas path on a pinned instance.
func invokeOnFixedInstance(
	wi *wrappedInstance,
	contract *commonPb.Contract,
	method string,
	parameters map[string][]byte,
	txContext protocol.TxSimContext,
	log *logger.CMLogger,
) (gas uint64, code uint32, message string) {
	instance := wi.wasmInstance
	gasLimit := runtimeGasLimit
	if err := setGasByExport(instance, gasLimit); err != nil {
		return 0, 1, err.Error()
	}

	sc := NewSimContext(method, log, ChainId)
	defer sc.removeCtxPointer()
	sc.Contract = contract
	sc.TxSimContext = txContext
	sc.ContractResult = &commonPb.ContractResult{Code: 0}
	sc.parameters = parameters
	sc.Instance = instance

	callErr := sc.CallMethod(instance)
	gasRemaining, gerr := getGasByExport(instance)
	if gerr != nil {
		return 0, 1, gerr.Error()
	}
	if gasRemaining == 0 || gasRemaining == ^uint64(0) {
		callErr = fmt.Errorf("out of gas")
	}
	gas = gasLimit - gasRemaining
	if callErr != nil {
		return gas, 1, callErr.Error()
	}
	if sc.ContractResult != nil && sc.ContractResult.Code != 0 {
		return gas, sc.ContractResult.Code, sc.ContractResult.Message
	}
	return gas, 0, ""
}
