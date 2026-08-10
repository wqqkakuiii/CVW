/*
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tbft

import (
	"fmt"

	"chainmaker.org/chainmaker/common/v2/monitor"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	// GaugeTypeHeight is the height type for the metrics of prometheus
	GaugeTypeHeight = 0
	// GaugeTypeRound is the round type for the metrics of prometheus
	GaugeTypeRound = 1

	// ChainId is metrics label of chainId
	ChainId = "chainId"
	// LocalId is metrics label of localId
	LocalId = "localId"
	// NodeId is metrics label of nodeId
	NodeId = "nodeId"
	// SUBSYSTEM_CORE_COMMITTER is subsystem of consensus metrics
	SUBSYSTEM_CORE_COMMITTER = "consensus"
)

// NewGaugeVec builds a height, round or layer gauge vec for prometheus
func (consensus *ConsensusTBFTImpl) NewGaugeVec(nodeId string, gaugeType int) {
	switch gaugeType {
	case GaugeTypeHeight:
		if consensus.metricsHeight == nil {
			consensus.metricsHeight = make(map[string]*prometheus.GaugeVec)
		}
		if _, ok := consensus.metricsHeight[nodeId]; ok {
			consensus.logger.Warnf("metrics[chainmaker_consensus_node_status_height_%s] exist,"+
				"can not register repeatedly", nodeId)
			return
		}
		consensus.metricsHeight[nodeId] = monitor.NewGaugeVec(
			SUBSYSTEM_CORE_COMMITTER,
			fmt.Sprintf("node_status_height_%s", nodeId),
			fmt.Sprintf("block height of node %s", nodeId),
			ChainId, LocalId, NodeId,
		)
		consensus.logger.Infof("register metrics:chainmaker_consensus_node_status_height_%s", nodeId)
	case GaugeTypeRound:
		if consensus.metricsRound == nil {
			consensus.metricsRound = make(map[string]*prometheus.GaugeVec)
		}
		if _, ok := consensus.metricsRound[nodeId]; ok {
			consensus.logger.Warnf("metrics[chainmaker_consensus_node_status_round_%s] exist,"+
				"can not register repeatedly", nodeId)
			return
		}
		consensus.metricsRound[nodeId] = monitor.NewGaugeVec(
			SUBSYSTEM_CORE_COMMITTER,
			fmt.Sprintf("node_status_round_%s", nodeId),
			fmt.Sprintf("consensus round of node %s", nodeId),
			ChainId, LocalId, NodeId,
		)
		consensus.logger.Infof("register metrics:chainmaker_consensus_node_status_round_%s", nodeId)
	default:
		consensus.logger.Errorf("invalid gauge type:%d", gaugeType)
	}
}
