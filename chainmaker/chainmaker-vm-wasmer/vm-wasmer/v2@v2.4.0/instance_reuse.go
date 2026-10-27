/*
Copyright (C) BABEC. All rights reserved.
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wasmer

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	commonPb "chainmaker.org/chainmaker/pb-go/v2/common"
	"chainmaker.org/chainmaker/pb-go/v2/syscontract"
	"chainmaker.org/chainmaker/protocol/v2"
)

const (
	// wasmerInstanceReuseParam is an install/upgrade parameter.
	// true: return the instance to this contract's pool after the call.
	// false: close it and build a new one.
	// Absent: follow CM_WASMER_FORCE_NO_REUSE.
	wasmerInstanceReuseParam = "wasmer_instance_reuse"

	wasmerInstanceReusePrefix = "WasmerInstanceReuse:"

	revertUnset   int32 = 0
	revertDiscard int32 = 1
	revertKeep    int32 = 2
)

func wasmerInstanceReuseKey(contractName string) []byte {
	return []byte(wasmerInstanceReusePrefix + contractName)
}

func parseWasmerInstanceReuse(raw []byte) (reuse bool, ok bool) {
	switch strings.ToLower(strings.TrimSpace(string(raw))) {
	case "1", "true", "yes", "on", "reuse":
		return true, true
	case "0", "false", "no", "off", "noreuse":
		return false, true
	default:
		return false, false
	}
}

func envForceNoReuse() bool {
	return os.Getenv("CM_WASMER_FORCE_NO_REUSE") != "0"
}

func reuseStateValue(reuse bool) []byte {
	if reuse {
		return []byte("1")
	}
	return []byte("0")
}

// resolveInstanceReuse decides whether this call returns the instance to the pool.
// init_contract and upgrade may set wasmer_instance_reuse. The choice is stored in
// CONTRACT_MANAGE and read back with GetNoRecord, so later calls do not gain a read-set entry.
func resolveInstanceReuse(contract *commonPb.Contract, method string, parameters map[string][]byte,
	txContext protocol.TxSimContext) (bool, error) {
	if contract == nil || txContext == nil {
		return !envForceNoReuse(), nil
	}

	if method == InitContractFunc || method == UpgradeContractFunc {
		if raw, exists := parameters[wasmerInstanceReuseParam]; exists {
			reuse, ok := parseWasmerInstanceReuse(raw)
			if !ok {
				return false, fmt.Errorf("invalid %s=%q, expect true or false",
					wasmerInstanceReuseParam, string(raw))
			}
			delete(parameters, wasmerInstanceReuseParam)
			err := txContext.Put(
				syscontract.SystemContract_CONTRACT_MANAGE.String(),
				wasmerInstanceReuseKey(contract.Name),
				reuseStateValue(reuse),
			)
			if err != nil {
				return false, err
			}
			return reuse, nil
		}
	}

	raw, err := txContext.GetNoRecord(
		syscontract.SystemContract_CONTRACT_MANAGE.String(),
		wasmerInstanceReuseKey(contract.Name),
	)
	if err != nil {
		return false, err
	}
	if len(raw) == 0 {
		return !envForceNoReuse(), nil
	}
	reuse, ok := parseWasmerInstanceReuse(raw)
	if !ok {
		return !envForceNoReuse(), nil
	}
	return reuse, nil
}

func (instance *wrappedInstance) setRevertMode(reuse bool) {
	if instance == nil {
		return
	}
	mode := revertDiscard
	if reuse {
		mode = revertKeep
	}
	atomic.StoreInt32(&instance.revertMode, mode)
}

func instanceDiscardOnRevert(instance *wrappedInstance) bool {
	if instance == nil {
		return envForceNoReuse()
	}
	switch atomic.LoadInt32(&instance.revertMode) {
	case revertDiscard:
		return true
	case revertKeep:
		return false
	default:
		return envForceNoReuse()
	}
}
