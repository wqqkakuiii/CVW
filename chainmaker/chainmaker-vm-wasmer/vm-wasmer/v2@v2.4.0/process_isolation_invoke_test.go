/*
Copyright (C) BABEC. All rights reserved.
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wasmer

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	logger2 "chainmaker.org/chainmaker/logger/v2"
	commonPb "chainmaker.org/chainmaker/pb-go/v2/common"
)

func ensureProcessWorkerBinForTest(t *testing.T) string {
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
	if st, err := os.Stat(bin); err != nil || st.IsDir() {
		cmd := exec.Command("go", "build", "-o", bin, "./cmd/wasmer-process-worker")
		cmd.Dir = pkgDir
		cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
		out, berr := cmd.CombinedOutput()
		if berr != nil {
			t.Fatalf("build worker: %v\n%s", berr, out)
		}
	}
	t.Setenv(envProcessWorkerBin, bin)
	return bin
}

// TestProcessIsolationInvoke smoke-tests invoke + host IPC through a worker process.
func TestProcessIsolationInvoke(t *testing.T) {
	ensureProcessWorkerBinForTest(t)
	t.Setenv(envProcessIsolation, "1")

	wasmPath := "./testdata/bigInput-go.wasm"
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read wasm: %v", err)
	}

	contractName := "bigInput"
	contractMethod := "inputSize"
	contractId := commonPb.Contract{
		Name:        contractName,
		Version:     ContractVersion,
		RuntimeType: commonPb.RuntimeType_WASMER,
	}
	_, _, logger := prepareContract(wasmPath, t)

	vmPool, err := newVmPool(&contractId, wasmBytes, logger)
	if err != nil {
		t.Fatalf("newVmPool: %v", err)
	}
	defer vmPool.close()
	if !vmPool.processIsolation {
		t.Fatal("expected processIsolation enabled")
	}

	runtimeInst := RuntimeInstance{
		pool:    vmPool,
		log:     logger,
		chainId: ChainId,
	}

	dataSize := 128
	parameters := map[string][]byte{"data": bytes.Repeat([]byte("X"), dataSize)}
	fillingBaseParams(parameters)
	txSimContext := prepareTxSimContext(ChainId, BlockVersion, contractName, contractMethod, parameters, SnapshotMock{})

	contractResult, errType := runtimeInst.Invoke(
		&contractId, contractMethod, wasmBytes, parameters, txSimContext, 0)
	if contractResult.Code != 0 {
		t.Fatalf("invoke failed code=%d msg=%s", contractResult.Code, contractResult.Message)
	}
	resultStr := strings.TrimSpace(string(contractResult.Result))
	expected := strconv.Itoa(dataSize)
	if resultStr != expected {
		t.Fatalf("result mismatch: got %q want %q", resultStr, expected)
	}
	if contractResult.GasUsed == 0 {
		t.Fatal("expected non-zero gas used")
	}
	if errType != 0 {
		t.Fatalf("unexpected special tx type %v", errType)
	}
}

func TestProcessIsolationInvokeFact(t *testing.T) {
	ensureProcessWorkerBinForTest(t)
	t.Setenv(envProcessIsolation, "1")

	wasmPath := "./testdata/fact-go.wasm"
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read wasm: %v", err)
	}

	contractName := "fact"
	contractMethod := "save"
	contractId := commonPb.Contract{
		Name:        contractName,
		Version:     ContractVersion,
		RuntimeType: commonPb.RuntimeType_WASMER,
	}
	logger := logger2.GetLogger("unit_test")

	vmPool, err := newVmPool(&contractId, wasmBytes, logger)
	if err != nil {
		t.Fatalf("newVmPool: %v", err)
	}
	defer vmPool.close()

	runtimeInst := RuntimeInstance{
		pool:    vmPool,
		log:     logger,
		chainId: ChainId,
	}

	parameters := map[string][]byte{
		"file_hash": []byte("hash001"),
		"file_name": []byte("name001"),
		"time":      []byte("123"),
	}
	fillingBaseParams(parameters)
	txSimContext := prepareTxSimContext(ChainId, BlockVersion, contractName, contractMethod, parameters, SnapshotMock{})

	contractResult, _ := runtimeInst.Invoke(
		&contractId, contractMethod, wasmBytes, parameters, txSimContext, 0)
	if contractResult.Code != 0 {
		t.Fatalf("fact save failed code=%d msg=%s", contractResult.Code, contractResult.Message)
	}
}
