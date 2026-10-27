/*
Copyright (C) BABEC. All rights reserved.
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wasmer

import (
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"chainmaker.org/chainmaker/logger/v2"
	commonPb "chainmaker.org/chainmaker/pb-go/v2/common"
	"chainmaker.org/chainmaker/protocol/v2"
	"chainmaker.org/chainmaker/utils/v2"
)

// liveManagers is every InstancesManager created in this process.
// POST /debug/close-vm-pool closes them only when CM_WASMER_DEBUG_CLOSE_POOL=1.
var (
	liveManagersMu sync.Mutex
	liveManagers   []*InstancesManager
)

func init() {
	http.HandleFunc("/debug/close-vm-pool", handleCloseVmPool)
}

func handleCloseVmPool(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("CM_WASMER_DEBUG_CLOSE_POOL") != "1" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	liveManagersMu.Lock()
	managers := append([]*InstancesManager(nil), liveManagers...)
	liveManagersMu.Unlock()
	closed := 0
	for _, m := range managers {
		n, err := m.closeAllAndWait(45 * time.Second)
		closed += n
		if err != nil {
			http.Error(w, fmt.Sprintf("closed_pools=%d err=%v", closed, err), http.StatusGatewayTimeout)
			return
		}
	}
	fmt.Fprintf(w, "closed_pools=%d\n", closed)
}

// InstancesManager manages vm pools for all contracts
type InstancesManager struct {
	// chain identifier
	chainId string
	// control map operations
	m sync.RWMutex
	// contractName_contractVersion -> vm pool
	instanceMap map[string]*vmPool
	// module log
	log *logger.CMLogger
}

// NewInstancesManager return InstancesManager for every chain
func NewInstancesManager(chainId string) *InstancesManager {
	vmPoolManager := &InstancesManager{
		instanceMap: make(map[string]*vmPool),
		log:         logger.GetLoggerByChain(logger.MODULE_VM, chainId),
		chainId:     chainId,
	}
	liveManagersMu.Lock()
	liveManagers = append(liveManagers, vmPoolManager)
	liveManagersMu.Unlock()
	return vmPoolManager
}

// NewRuntimeInstance init vm pool and check byteCode correctness
func (m *InstancesManager) NewRuntimeInstance(
	txSimContext protocol.TxSimContext,
	chainId, method, codePath string,
	contract *commonPb.Contract,
	byteCode []byte,
	log protocol.Logger) (protocol.RuntimeInstance, error) {
	var err error
	if contract == nil || contract.Name == "" || contract.Version == "" {
		err = fmt.Errorf("contract id is nil")
		m.log.Warn(err)
		return nil, err
	}

	if len(byteCode) == 0 {
		err = fmt.Errorf("[%s_%s], byte code is nil", contract.Name, contract.Version)
		m.log.Warn(err)
		return nil, err
	}

	pool, err := m.getVmPool(contract, byteCode)
	if err != nil || pool == nil {
		return nil, err
	}

	runtime := &RuntimeInstance{
		pool:             pool,
		log:              m.log,
		chainId:          m.chainId,
		instancesManager: m,
	}

	return runtime, nil
}

// CloseRuntimeInstance comment at next version
func (m *InstancesManager) CloseRuntimeInstance(
	contractName string,
	contractVersion string) error {
	m.log.Debugf("wasmer instance manager => close runtime instance => %v(%v)", contractName, contractVersion)
	contract := commonPb.Contract{
		Name:    contractName,
		Version: contractVersion,
	}

	m.CloseAVmPool(&contract)
	return nil
}

func (m *InstancesManager) getVmPool(contractId *commonPb.Contract, byteCode []byte) (*vmPool, error) {
	var err error
	key := contractId.Name + "_" + contractId.Version

	m.m.RLock()
	pool, ok := m.instanceMap[key]
	m.m.RUnlock()
	if !ok {
		m.m.Lock()
		defer m.m.Unlock()

		pool, ok = m.instanceMap[key]
		if !ok {
			start := utils.CurrentTimeMillisSeconds()
			m.log.Infof("[%s] init vm pool start", key)

			pool, err = newVmPool(contractId, byteCode, m.log)
			if err != nil {
				return nil, err
			}

			pool.grow(defaultMinSize)
			m.instanceMap[key] = pool
			end := utils.CurrentTimeMillisSeconds()
			m.log.Infof("[%s] init vmPool done, currentSize=%d, spend %dms", key, pool.currentSize, end-start)
		}
	}
	return pool, err
}

// CloseAVmPool close the contract vm pool
func (m *InstancesManager) CloseAVmPool(contractId *commonPb.Contract) {
	m.m.Lock()
	defer m.m.Unlock()

	key := contractId.Name + "_" + contractId.Version
	pool, ok := m.instanceMap[key]
	if ok {
		m.log.Infof("close pool %s", key)
		pool.close()
		delete(m.instanceMap, key)
	}
}

// CloseAllVmPool close all contract vm pool
func (m *InstancesManager) CloseAllVmPool() {
	_, _ = m.closeAllAndWait(45 * time.Second)
}

// closeAllAndWait signals every pool to tear down and waits until each
// refresh loop has finished CloseInstance, module, store, and engine.
func (m *InstancesManager) closeAllAndWait(timeout time.Duration) (int, error) {
	m.m.Lock()
	pools := make([]*vmPool, 0, len(m.instanceMap))
	for key, pool := range m.instanceMap {
		m.log.Infof("close pool %s", key)
		pools = append(pools, pool)
		pool.close()
	}
	m.instanceMap = make(map[string]*vmPool)
	m.m.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for _, pool := range pools {
		select {
		case <-pool.closed:
		case <-timer.C:
			return 0, fmt.Errorf("timeout waiting for vm pool close")
		}
	}
	return len(pools), nil
}

// ResetAVmPool reset a contract vm pool install
// FIXME: 确认函数名是否多了字符A？@taifu
func (m *InstancesManager) ResetAVmPool(contractId *commonPb.Contract) {
	m.m.Lock()
	defer m.m.Unlock()

	key := contractId.Name + "_" + contractId.Version
	pool, ok := m.instanceMap[key]
	if ok {
		m.log.Infof("reset pool %s", key)
		pool.reset()
	}
}

// ResetAllPool reset all contract pool instance
func (m *InstancesManager) ResetAllPool() {
	m.m.Lock()
	defer m.m.Unlock()

	for key, pool := range m.instanceMap {
		m.log.Infof("reset pool %s", key)
		pool.reset()
	}
}

// StartVM comment at next version
func (m *InstancesManager) StartVM() error {
	return nil
}

// StopVM comment at next version
func (m *InstancesManager) StopVM() error {
	return nil
}

// BeforeSchedule add request before block schedule
func (m *InstancesManager) BeforeSchedule(blockFingerprint string, blockHeight uint64) {
}

// AfterSchedule print tx log after block schedule
func (m *InstancesManager) AfterSchedule(blockFingerprint string, blockHeight uint64) {
}
