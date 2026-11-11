package vm

import "chainmaker.org/chainmaker/protocol/v2"

// 本文件把调度器的出块/重放角色传给 Wasmer，用来选择新建或复用实例。
const (
	// WasmerExecDefault keeps the contract install option and CM_WASMER_FORCE_NO_REUSE.
	WasmerExecDefault int32 = 0
	// WasmerExecProposeNoReuse makes the block proposer run each transaction on a new instance.
	WasmerExecProposeNoReuse int32 = 1
	// WasmerExecReplayReuse makes a verifying node replay with instance reuse first.
	WasmerExecReplayReuse int32 = 2
	// WasmerExecForceFresh reruns one transaction on a newly created instance.
	WasmerExecForceFresh int32 = 3
	// WasmerExecHoldFresh keeps one newly created instance while a verifier retries one transaction.
	WasmerExecHoldFresh int32 = 4
	// WasmerMaxInstanceUse is the pool reuse limit and the proposer gas-stabilize limit.
	WasmerMaxInstanceUse = 50
)

// SetWasmerExecMode records how this transaction should obtain a wasmer instance.
func SetWasmerExecMode(ctx protocol.TxSimContext, mode int32) {
	sim, ok := ctx.(*txSimContextImpl)
	if !ok || sim == nil {
		return
	}
	sim.wasmerExecMode = mode
}

// releaseWasmerHold closes the instance kept for a verifier's gas stabilize loop.
var releaseWasmerHold func(txId string)

// RegisterWasmerHoldReleaser lets the wasmer runtime close a held instance.
func RegisterWasmerHoldReleaser(fn func(txId string)) {
	releaseWasmerHold = fn
}

// ReleaseWasmerHold closes every instance held for this transaction.
func ReleaseWasmerHold(txId string) {
	if releaseWasmerHold != nil {
		releaseWasmerHold(txId)
	}
}

// GetWasmerExecMode is read by the wasmer runtime through a type assertion.
func (s *txSimContextImpl) GetWasmerExecMode() int32 {
	if s == nil {
		return WasmerExecDefault
	}
	return s.wasmerExecMode
}
