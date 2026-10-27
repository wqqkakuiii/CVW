/*
Copyright (C) BABEC. All rights reserved.
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wasmer

import (
	"testing"

	commonPb "chainmaker.org/chainmaker/pb-go/v2/common"
	"chainmaker.org/chainmaker/pb-go/v2/syscontract"
)

func TestParseWasmerInstanceReuse(t *testing.T) {
	cases := []struct {
		raw   string
		reuse bool
		ok    bool
	}{
		{"true", true, true},
		{"TRUE", true, true},
		{"1", true, true},
		{"reuse", true, true},
		{"false", false, true},
		{"0", false, true},
		{"noreuse", false, true},
		{"", false, false},
		{"maybe", false, false},
	}
	for _, c := range cases {
		reuse, ok := parseWasmerInstanceReuse([]byte(c.raw))
		if reuse != c.reuse || ok != c.ok {
			t.Fatalf("parse %q: got reuse=%v ok=%v, want reuse=%v ok=%v", c.raw, reuse, ok, c.reuse, c.ok)
		}
	}
}

func TestResolveInstanceReusePersistsAcrossCalls(t *testing.T) {
	contract := &commonPb.Contract{Name: "reuse_contract", Version: "1.0"}
	txSimContext := prepareTxSimContext("chain1", 2030100, contract.Name, InitContractFunc, nil, SnapshotMock{})

	installParams := map[string][]byte{
		wasmerInstanceReuseParam: []byte("true"),
		"init_key":               []byte("v"),
	}
	reuse, err := resolveInstanceReuse(contract, InitContractFunc, installParams, txSimContext)
	if err != nil {
		t.Fatal(err)
	}
	if !reuse {
		t.Fatal("install option true should reuse")
	}
	if _, exists := installParams[wasmerInstanceReuseParam]; exists {
		t.Fatal("host option should be removed before it is passed to the contract")
	}
	if string(installParams["init_key"]) != "v" {
		t.Fatal("other init parameters should stay")
	}

	reuse, err = resolveInstanceReuse(contract, "increase", map[string][]byte{
		wasmerInstanceReuseParam: []byte("false"),
	}, txSimContext)
	if err != nil {
		t.Fatal(err)
	}
	if !reuse {
		t.Fatal("a later invoke must keep the value stored at install, not the call parameter")
	}

	stored, err := txSimContext.GetNoRecord(
		syscontract.SystemContract_CONTRACT_MANAGE.String(),
		wasmerInstanceReuseKey(contract.Name),
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != "1" {
		t.Fatalf("stored reuse flag = %q, want 1", stored)
	}
}

func TestResolveInstanceReuseRejectsInvalidOption(t *testing.T) {
	contract := &commonPb.Contract{Name: "bad_contract", Version: "1.0"}
	txSimContext := prepareTxSimContext("chain1", 2030100, contract.Name, InitContractFunc, nil, SnapshotMock{})
	_, err := resolveInstanceReuse(contract, InitContractFunc, map[string][]byte{
		wasmerInstanceReuseParam: []byte("maybe"),
	}, txSimContext)
	if err == nil {
		t.Fatal("invalid option should fail contract install")
	}
}
