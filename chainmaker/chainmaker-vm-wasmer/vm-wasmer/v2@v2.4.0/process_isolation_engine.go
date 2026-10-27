/*
Copyright (C) BABEC. All rights reserved.
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wasmer

import (
	"fmt"

	wasmergo "chainmaker.org/chainmaker/vm-wasmer/v2/wasmer-go"
)

// newWasmerEngineAndStoreForPool builds Engine+Store with the same metering /
// MaxPages / compiler-thread defaults as newVmPool (for worker processes).
func newWasmerEngineAndStoreForPool() (*wasmergo.Engine, *wasmergo.Store, error) {
	config := wasmergo.NewConfig()
	opmap := map[wasmergo.Opcode]uint32{wasmergo.Opcode(0): 0}
	config.PushMeteringMiddleware(1e19, opmap, map[string]uint32{}, "")
	config.MaxPagesLimit(512)
	config.CompilerNumThreads(1)
	engine := wasmergo.NewEngineWithConfig(config)
	if engine == nil {
		return nil, nil, fmt.Errorf("NewEngineWithConfig returned nil")
	}
	store := wasmergo.NewStore(engine)
	if store == nil {
		engine.Close()
		return nil, nil, fmt.Errorf("NewStore returned nil")
	}
	return engine, store, nil
}
