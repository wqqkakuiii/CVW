/*
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tbft

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"math"
	"testing"
	"time"

	"chainmaker.org/chainmaker/chainconf/v2"
	"chainmaker.org/chainmaker/common/v2/msgbus"
	"chainmaker.org/chainmaker/consensus-utils/v2/consistent_service"
	"chainmaker.org/chainmaker/consensus-utils/v2/testframework"
	"chainmaker.org/chainmaker/consensus-utils/v2/wal_service"
	"chainmaker.org/chainmaker/logger/v2"
	"chainmaker.org/chainmaker/pb-go/v2/accesscontrol"
	commonpb "chainmaker.org/chainmaker/pb-go/v2/common"
	configpb "chainmaker.org/chainmaker/pb-go/v2/config"
	consensuspb "chainmaker.org/chainmaker/pb-go/v2/consensus"
	tbftpb "chainmaker.org/chainmaker/pb-go/v2/consensus/tbft"
	netpb "chainmaker.org/chainmaker/pb-go/v2/net"
	"chainmaker.org/chainmaker/protocol/v2"
	"chainmaker.org/chainmaker/protocol/v2/mock"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
)

// TestConsensusTBFTImpl_Type
// @Description: TestConsensusTBFTImpl_Type
// @param t
func TestConsensusTBFTImpl_Type(t *testing.T) {
	tbftImpl.RLock()
	defer tbftImpl.RUnlock()
	typeInt := tbftImpl.Type()
	require.Equal(t, typeInt, int8(TypeLocalTBFTState))
}

// TestConsensusTBFTImpl_Data
// @Description: TestConsensusTBFTImpl_Data
// @param t
func TestConsensusTBFTImpl_Data(t *testing.T) {
	tbftImpl.RLock()
	defer tbftImpl.RUnlock()
	date := tbftImpl.Data()
	require.Equal(t, date, tbftImpl)
}

// TestConsensusTBFTImpl_Update
// @Description: TestConsensusTBFTImpl_Update
// @param t
func TestConsensusTBFTImpl_Update(t *testing.T) {
	tbftImpl.Lock()
	defer tbftImpl.Unlock()
	status := make(map[int8]consistent_service.Status)
	s := &RemoteState{Id: "node1", Height: 1, Round: 1, Step: 1}
	status[s.Type()] = s
	tbftImpl.Update(s)

}

// TestConsensusTBFTImpl_InitExtendHandler
// @Description: TestConsensusTBFTImpl_InitExtendHandler
// @param t
func TestConsensusTBFTImpl_InitExtendHandler(t *testing.T) {
	tbftImpl.Lock()
	defer tbftImpl.Unlock()
	handler := &mock.MockConsensusExtendHandler{}
	tbftImpl.InitExtendHandler(handler)
}

// TestConsensusTBFTImpl_Verify
// @Description: TestConsensusTBFTImpl_Verify
// @param t
func TestConsensusTBFTImpl_Verify(t *testing.T) {
	tbftImpl.Lock()
	defer tbftImpl.Unlock()
	chainConfig := &configpb.ChainConfig{
		Consensus: &configpb.ConsensusConfig{
			Type: consensuspb.ConsensusType_TBFT,
			Nodes: []*configpb.OrgConfig{
				{
					OrgId:  org1Id,
					NodeId: []string{org1NodeId},
				},
				{
					OrgId:  org2Id,
					NodeId: []string{org2NodeId},
				},
				{
					OrgId:  org3Id,
					NodeId: []string{org3NodeId},
				},
				{
					OrgId:  org4Id,
					NodeId: []string{org4NodeId},
				},
			},
		},
	}
	err := tbftImpl.Verify(consensuspb.ConsensusType_SOLO, chainConfig)
	require.NotNil(t, err)
}

// TestConsensusTBFTImpl_ExtractProposeTimeout
// @Description: TestConsensusTBFTImpl_ExtractProposeTimeout
// @param t
func TestConsensusTBFTImpl_ExtractProposeTimeout(t *testing.T) {
	tbftImpl.Lock()
	defer tbftImpl.Unlock()

	_, err := tbftImpl.extractProposeTimeout("100")
	require.NotNil(t, err)

	_, err = tbftImpl.extractProposeTimeout((time.Millisecond * 100).String())
	require.Nil(t, err)
}

// TestConsensusTBFTImpl_ExtractProposeTimeoutDelta
// @Description: TestConsensusTBFTImpl_ExtractProposeTimeoutDelta
// @param t
func TestConsensusTBFTImpl_ExtractProposeTimeoutDelta(t *testing.T) {
	tbftImpl.Lock()
	defer tbftImpl.Unlock()

	_, err := tbftImpl.extractProposeTimeoutDelta("100")
	require.NotNil(t, err)

	_, err = tbftImpl.extractProposeTimeoutDelta((time.Millisecond * 100).String())
	require.Nil(t, err)
}

// TestConsensusTBFTImpl_ExtractBlocksPerProposer
// @Description: TestConsensusTBFTImpl_ExtractBlocksPerProposer
// @param t
func TestConsensusTBFTImpl_ExtractBlocksPerProposer(t *testing.T) {
	tbftImpl.Lock()
	defer tbftImpl.Unlock()

	_, err := tbftImpl.extractBlocksPerProposer("100")
	require.Nil(t, err)

	_, err = tbftImpl.extractBlocksPerProposer((time.Millisecond * 100).String())
	require.NotNil(t, err)

	_, err = tbftImpl.extractBlocksPerProposer("-1")
	require.NotNil(t, err)
}

// extractTimeoutProposeOptimal
// @Description: extractTimeoutProposeOptimal
// @param t
func TestConsensusTBFTImpl_ExtractextractTimeoutProposeOptimal(t *testing.T) {
	tbftImpl.Lock()
	defer tbftImpl.Unlock()

	_, err := tbftImpl.extractTimeoutProposeOptimal("100")
	require.NotNil(t, err)

	_, err = tbftImpl.extractTimeoutProposeOptimal((time.Millisecond * 100).String())
	require.Nil(t, err)
}

// TestConsensusTBFTImpl_ExtractTimeoutProposeOptimal
// @Description: TestConsensusTBFTImpl_ExtractTimeoutProposeOptimal
// @param t
func TestConsensusTBFTImpl_ExtractTimeoutProposeOptimal(t *testing.T) {
	tbftImpl.Lock()
	defer tbftImpl.Unlock()

	_, err := tbftImpl.extractProposeOptimal("true")
	require.Nil(t, err)

	_, err = tbftImpl.extractProposeOptimal("1")
	require.Nil(t, err)

	_, err = tbftImpl.extractProposeOptimal("t")
	require.Nil(t, err)

	_, err = tbftImpl.extractProposeOptimal("false")
	require.Nil(t, err)

	_, err = tbftImpl.extractProposeOptimal("0")
	require.Nil(t, err)

	_, err = tbftImpl.extractProposeOptimal("f")
	require.Nil(t, err)

	_, err = tbftImpl.extractProposeOptimal("yes")
	require.NotNil(t, err)

	_, err = tbftImpl.extractProposeOptimal("-1")
	require.NotNil(t, err)

	_, err = tbftImpl.extractProposeOptimal("a")
	require.NotNil(t, err)

}

// TestConsensusTBFTImpl_handleTimeout
// @Description: TestConsensusTBFTImpl_handleTimeout
// @param t
func TestConsensusTBFTImpl_getProposeTimeout(t *testing.T) {
	tbftImpl.Lock()
	defer tbftImpl.Unlock()

	tbftImpl.TimeoutPropose = 30 * time.Second
	tbftImpl.TimeoutProposeDelta = 1 * time.Second
	tbftImpl.ProposeOptimal = false
	tbftImpl.ProposeOptimalTimer = time.NewTimer(0 * time.Second)
	tbftImpl.ProposeOptimalTimer.Stop()
	id := validator0
	tbftImpl.Id = id
	timeout := tbftImpl.getProposeTimeout(id, 100, 100, 0)
	require.Equal(t, 30*time.Second, timeout)

	timeout = tbftImpl.getProposeTimeout(id, 97, 100, 0)
	require.Equal(t, 10*time.Second, timeout)

	timeout = tbftImpl.getProposeTimeout(id, 80, 100, 0)
	require.Equal(t, 2*time.Second, timeout)

	tbftImpl.ProposeOptimal = true
	timeout = tbftImpl.getProposeTimeout(id, 101, 100, 0)
	require.Equal(t, 30*time.Second, timeout)

	validator := validator1
	tbftImpl.validatorSet.validatorsBeatTime[validator] = time.Now().UnixNano() / 1e6
	timeout = tbftImpl.getProposeTimeout(validator, 100, 100, 0)
	require.Equal(t, 30*time.Second, timeout)

	timeout = tbftImpl.getProposeTimeout(validator, 99, 100, 0)
	require.Equal(t, 30*time.Second, timeout)

	timeout = tbftImpl.getProposeTimeout(validator, 98, 100, 0)
	require.Equal(t, 30*time.Second, timeout)

	timeout = tbftImpl.getProposeTimeout(validator, 97, 100, 0)
	require.Equal(t, 0*time.Second, timeout)

	tbftImpl.validatorSet.validatorsBeatTime[validator] = time.Now().UnixNano()/1e6 - TimeDisconnet*2
	timeout = tbftImpl.getProposeTimeout(validator, 100, 100, 0)
	require.Equal(t, 0*time.Second, timeout)

	timeout = tbftImpl.getProposeTimeout(validator, 0, 0, 0)
	require.Equal(t, 30*time.Second, timeout)

	timeout = tbftImpl.getProposeTimeout(validator, 0, 1, 0)
	require.Equal(t, 30*time.Second, timeout)

	timeout = tbftImpl.getProposeTimeout(validator, 1, 3, 0)
	require.Equal(t, 0*time.Second, timeout)

}

// TestConsensusTBFTImpl_handleTimeout
// @Description: TestConsensusTBFTImpl_handleTimeout
// @param t
func TestConsensusTBFTImpl_handleTimeout(t *testing.T) {

	timeoutInfo := &tbftpb.TimeoutInfo{Duration: 1, Height: 1, Round: 1, Step: -1}
	tbftImpl.handleTimeout(*timeoutInfo)

	timeoutInfo = &tbftpb.TimeoutInfo{Duration: 1, Height: 1, Round: 1, Step: tbftpb.Step_PREVOTE}
	tbftImpl.handleTimeout(*timeoutInfo)

	timeoutInfo = &tbftpb.TimeoutInfo{Duration: 1, Height: 1, Round: 1, Step: tbftpb.Step_PRECOMMIT}
	tbftImpl.handleTimeout(*timeoutInfo)

	timeoutInfo = &tbftpb.TimeoutInfo{Duration: 1, Height: 1, Round: 1, Step: tbftpb.Step_COMMIT}
	tbftImpl.handleTimeout(*timeoutInfo)
}

// TestConsensusTBFTImpl_handleProposeOptimalTimeout
// @Description: TestConsensusTBFTImpl_handleProposeOptimalTimeout
// @param t
func TestConsensusTBFTImpl_handleProposeOptimalTimeout(t *testing.T) {

	tbftImpl.ProposeOptimalTimer = time.NewTimer(0 * time.Second)
	tbftImpl.ProposeOptimalTimer.Stop()
	tbftImpl.validatorSet.blocksPerProposer = 1

	tbftImpl.Step = tbftpb.Step_PREVOTE
	tbftImpl.handleProposeOptimalTimeout()

	tbftImpl.Step = tbftpb.Step_PROPOSE
	tbftImpl.validatorSet.validatorsBeatTime[validator0] = time.Now().UnixNano() / 1e6
	tbftImpl.validatorSet.validatorsBeatTime[validator1] = time.Now().UnixNano() / 1e6
	tbftImpl.handleProposeOptimalTimeout()
}

// TestConsensusTBFTImpl_PrevoteTimeout
// @Description: TestConsensusTBFTImpl_PrevoteTimeout
// @param t
func TestConsensusTBFTImpl_PrevoteTimeout(t *testing.T) {
	t1 := time.Duration(
		TimeoutPrevote.Nanoseconds()+TimeoutPrevoteDelta.Nanoseconds()*int64(0),
	) * time.Nanosecond
	t2 := tbftImpl.PrevoteTimeout(0)
	require.Equal(t, t1, t2)
}

// TestConsensusTBFTImpl_PrecommitTimeout
// @Description: TestConsensusTBFTImpl_PrecommitTimeout
// @param t
func TestConsensusTBFTImpl_PrecommitTimeout(t *testing.T) {
	t1 := time.Duration(
		TimeoutPrecommit.Nanoseconds()+TimeoutPrecommitDelta.Nanoseconds()*int64(0),
	) * time.Nanosecond
	t2 := tbftImpl.PrecommitTimeout(0)
	require.Equal(t, t1, t2)
}

// TestConsensusTBFTImpl_CommitTimeout
// @Description: TestConsensusTBFTImpl_CommitTimeout
// @param t
func TestConsensusTBFTImpl_CommitTimeout(t *testing.T) {
	t1 := time.Duration(TimeoutCommit.Nanoseconds()*int64(0)) * time.Nanosecond
	t2 := tbftImpl.CommitTimeout(0)
	require.Equal(t, t1, t2)
}

// TestConsensusTBFTImpl_ToProto
// @Description: TestConsensusTBFTImpl_ToProto
// @param t
func TestConsensusTBFTImpl_ToProto(t *testing.T) {
	tbftImpl.RLock()
	defer tbftImpl.RUnlock()
	consensusState := tbftImpl.ToProto()
	require.NotNil(t, consensusState)
}

// TestConsensusTBFTImpl_ToGossipStateProto
// @Description: TestConsensusTBFTImpl_ToGossipStateProto
// @param t
func TestConsensusTBFTImpl_ToGossipStateProto(t *testing.T) {
	tbftImpl.RLock()
	defer tbftImpl.RUnlock()
	gssipState := tbftImpl.ToGossipStateProto()
	require.NotNil(t, gssipState)
}

// TestConsensusTBFTImpl_EnterPrecommitFromReplayWal
// @Description: TestConsensusTBFTImpl_EnterPrecommitFromReplayWal
// @param t
func TestConsensusTBFTImpl_EnterPrecommitFromReplayWal(t *testing.T) {
	tbftImpl.RLock()
	defer tbftImpl.RUnlock()
	err := tbftImpl.enterPrecommitFromReplayWal(nil)
	require.NotNil(t, err)
}

// TestConsensusTBFTImpl_GetValidators
// @Description: TestConsensusTBFTImpl_GetValidators
// @param t
func TestConsensusTBFTImpl_GetValidators(t *testing.T) {
	tbftImpl.RLock()
	defer tbftImpl.RUnlock()
	tbftImpl.state = stop
	validators, err := tbftImpl.GetValidators()
	require.NotNil(t, err)
	require.Nil(t, validators)
	tbftImpl.state = start
	validators, err = tbftImpl.GetValidators()
	require.Nil(t, err)
	require.NotNil(t, validators)
}

// TestConsensusTBFTImpl_GetLastHeight
// @Description: TestConsensusTBFTImpl_GetLastHeight
// @param t
func TestConsensusTBFTImpl_GetLastHeight(t *testing.T) {
	tbftImpl.RLock()
	defer tbftImpl.RUnlock()
	tbftImpl.state = stop
	height := tbftImpl.GetLastHeight()
	require.True(t, height == math.MaxUint64)
	tbftImpl.state = start
	height = tbftImpl.GetLastHeight()
	require.False(t, height == math.MaxUint64)
}

// TestConsensusTBFTImpl_GetConsensusStateJSON
// @Description: TestConsensusTBFTImpl_GetConsensusStateJSON
// @param t
func TestConsensusTBFTImpl_GetConsensusStateJSON(t *testing.T) {
	tbftImpl.RLock()
	defer tbftImpl.RUnlock()
	tbftImpl.state = stop
	stat, err := tbftImpl.GetConsensusStateJSON()
	require.Nil(t, stat)
	require.NotNil(t, err)
	tbftImpl.state = start
	stat, err = tbftImpl.GetConsensusStateJSON()
	require.NotNil(t, stat)
	require.Nil(t, err)
}

func TestConsensusTBFTImpl_getValidatorSet(t *testing.T) {
	vs := tbftImpl.getValidatorSet()
	require.NotNil(t, vs)
}

func TestConsensusTBFTImpl_onQuit(t *testing.T) {
	tbftImpl.OnQuit()
}

// TestProcVerifyBlockWithRwSets
// @Description: TestProcVerifyBlockWithRwSets
// @param t
func TestProcVerifyBlockWithRwSets(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	tbftImpl.RLock()
	defer tbftImpl.RUnlock()
	var blockHeight uint64 = 10
	blockHash := sha256.Sum256(nil)
	_, _ = rand.Read(blockHash[:])
	block := &commonpb.Block{
		Header: &commonpb.BlockHeader{
			BlockHeight: blockHeight,
			BlockHash:   blockHash[:],
		},
		AdditionalData: &commonpb.AdditionalData{
			ExtraData: map[string][]byte{
				TBFTAddtionalDataKey: nil,
			},
		},
	}

	var votes []*tbftpb.Vote
	for _, id := range tbftImpl.validatorSet.Validators {
		vote := NewVote(tbftpb.VoteType_VOTE_PREVOTE, id, blockHeight, 0, blockHash[:])
		votes = append(votes, vote)
	}

	proposal := &tbftpb.Proposal{
		Voter:    "node1",
		Height:   1,
		Round:    1,
		PolRound: 1,
		Block:    block,
		Qc:       votes,
	}

	tbftProposal := NewTBFTProposal(proposal, true)
	oldLen := len(tbftProposal.Bytes)
	tbftProposal.Marshal()
	require.Equal(t, oldLen, len(tbftProposal.Bytes))
	core := testframework.NewCoreEngineForTest(&testframework.TestNodeConfig{}, cmLogger)
	ac := mock.NewMockAccessControlProvider(ctrl)
	ac.EXPECT().CreatePrincipal(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil, nil)
	ac.EXPECT().VerifyMsgPrincipal(gomock.Any(), gomock.Any()).AnyTimes().Return(true, nil)
	tbftImpl.ac = ac
	tbftImpl.blockVerifier = core.GetBlockVerifier()
	err := tbftImpl.procVerifyBlockWithRwSets(proposal)
	require.Nil(t, err)
}

// TestProcRoundQc
// @Description: TestProcRoundQc
// @param t
func TestProcRoundQc(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	tbftImpl.RLock()
	defer tbftImpl.RUnlock()

	tbftImpl.heightRoundVoteSet = newHeightRoundVoteSet(
		cmLogger, 1, 1, tbftImpl.validatorSet)

	blockHash := sha256.Sum256(nil)
	_, _ = rand.Read(blockHash[:])
	for _, id := range tbftImpl.validatorSet.Validators {
		vote := NewVote(tbftpb.VoteType_VOTE_PRECOMMIT, id, 1, 1, blockHash[:])
		_, _ = tbftImpl.heightRoundVoteSet.addVote(vote)
	}

	voteSet := NewVoteSet(cmLogger, tbftpb.VoteType_VOTE_PRECOMMIT, 1, 2, tbftImpl.validatorSet)

	for _, id := range tbftImpl.validatorSet.Validators {
		vote := NewVote(tbftpb.VoteType_VOTE_PRECOMMIT, id, 1, 2, blockHash[:])
		_, _ = voteSet.AddVote(vote, false)
	}

	roundqc := &tbftpb.RoundQC{
		Height: 1,
		Round:  2,
		Qc:     voteSet.ToProto(),
	}

	ac := mock.NewMockAccessControlProvider(ctrl)
	ac.EXPECT().CreatePrincipal(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil, nil)
	ac.EXPECT().VerifyMsgPrincipal(gomock.Any(), gomock.Any()).AnyTimes().Return(true, nil)
	tbftImpl.ac = ac
	tbftImpl.procRoundQC(roundqc)
}

// TestProcRoundQcByPC
// @Description: TestProcRoundQcByPC
// @param t
func TestProcRoundQcByPC(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	tbftImpl.RLock()
	defer tbftImpl.RUnlock()

	tbftImpl.metrics = newHeightMetrics(1)
	tbftImpl.heightRoundVoteSet = newHeightRoundVoteSet(
		cmLogger, 1, 1, tbftImpl.validatorSet)

	blockHash := sha256.Sum256(nil)
	_, _ = rand.Read(blockHash[:])

	voteSet := NewVoteSet(cmLogger, tbftpb.VoteType_VOTE_PREVOTE, 1, 2, tbftImpl.validatorSet)

	for _, id := range tbftImpl.validatorSet.Validators {
		vote := NewVote(tbftpb.VoteType_VOTE_PRECOMMIT, id, 1, 2, blockHash[:])
		_, _ = voteSet.AddVote(vote, false)
	}

	roundqc := &tbftpb.RoundQC{
		Height: 1,
		Round:  2,
		Qc:     voteSet.ToProto(),
	}

	ac := mock.NewMockAccessControlProvider(ctrl)
	ac.EXPECT().CreatePrincipal(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil, nil)
	ac.EXPECT().VerifyMsgPrincipal(gomock.Any(), gomock.Any()).AnyTimes().Return(true, nil)
	tbftImpl.ac = ac
	tbftImpl.procRoundQC(roundqc)
}

// TestDelInvalidTxs
// @Description: TestDelInvalidTxs
// @param t
func TestDelInvalidTxs(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	tbftImpl.RLock()
	defer tbftImpl.RUnlock()

	//blockHash := sha256.Sum256(nil)
	//_, _ = rand.Read(blockHash[:])

	voteSet := NewVoteSet(cmLogger, tbftpb.VoteType_VOTE_PREVOTE, 1, 2, tbftImpl.validatorSet)

	for _, id := range tbftImpl.validatorSet.Validators {
		vote := NewVote(tbftpb.VoteType_VOTE_PREVOTE, id, 1, 2, nilHash)
		vote.InvalidTxs = []string{"hash1", "hash2"}
		_, _ = voteSet.AddVote(vote, true)
	}

	tbftImpl.delInvalidTxs(voteSet, nilHash)
}

func TestTBFTConsensus_OnQuit(t *testing.T) {
	type fields struct {
		log      *logger.CMLogger
		msgBus   msgbus.MessageBus
		messageC chan interface{}
	}
	tests := []struct {
		name   string
		fields fields
	}{
		{"test1",
			fields{logger.GetLogger(chainid), tbftImpl.msgbus, make(chan interface{})},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &ConsensusTBFTImpl{
				logger: tt.fields.log,
				msgbus: tt.fields.msgBus,
			}
			m.OnQuit()
		})
	}
}

// TestProcVote
// @Description: TestProcVote
// @param t
func TestProcVote(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	tbftImpl.RLock()
	defer tbftImpl.RUnlock()
	valSet := &validatorSet{
		logger:             tLogger,
		Validators:         nodes,
		validatorsHeight:   make(map[string]uint64),
		validatorsBeatTime: make(map[string]int64),
	}

	tbftImpl.validatorSet = valSet

	tbftImpl.metrics = newHeightMetrics(1)
	tbftImpl.heightRoundVoteSet = newHeightRoundVoteSet(
		cmLogger, 10, 1, tbftImpl.validatorSet)
	tbftImpl.Id = tbftImpl.validatorSet.Validators[3]
	tbftImpl.Height = 10
	tbftImpl.Round = 1

	// procPrevote
	blockHash := sha256.Sum256(nil)
	_, _ = rand.Read(blockHash[:])
	vote := NewVote(tbftpb.VoteType_VOTE_PREVOTE, tbftImpl.validatorSet.Validators[0], 10, 1, blockHash[:])
	_, _ = tbftImpl.heightRoundVoteSet.addVote(vote)
	vote = NewVote(tbftpb.VoteType_VOTE_PREVOTE, tbftImpl.validatorSet.Validators[1], 10, 1, blockHash[:])
	_, _ = tbftImpl.heightRoundVoteSet.addVote(vote)
	vote = NewVote(tbftpb.VoteType_VOTE_PREVOTE, tbftImpl.validatorSet.Validators[2], 10, 1, blockHash[:])
	_, _ = tbftImpl.heightRoundVoteSet.addVote(vote)
	tbftImpl.Step = tbftpb.Step_PRECOMMIT

	require.NotNil(t, tbftImpl.heightRoundVoteSet.getRoundVoteSet(1).Prevotes.Maj23)
	vote = NewVote(tbftpb.VoteType_VOTE_PREVOTE, tbftImpl.Id, 10, 1, blockHash[:])
	tbftImpl.procPrevote(vote)

	// procPrecomimit
	vote = NewVote(tbftpb.VoteType_VOTE_PRECOMMIT, tbftImpl.validatorSet.Validators[0], 10, 1, blockHash[:])
	_, _ = tbftImpl.heightRoundVoteSet.addVote(vote)
	vote = NewVote(tbftpb.VoteType_VOTE_PRECOMMIT, tbftImpl.validatorSet.Validators[1], 10, 1, blockHash[:])
	_, _ = tbftImpl.heightRoundVoteSet.addVote(vote)
	vote = NewVote(tbftpb.VoteType_VOTE_PRECOMMIT, tbftImpl.validatorSet.Validators[2], 10, 1, blockHash[:])
	_, _ = tbftImpl.heightRoundVoteSet.addVote(vote)
	tbftImpl.Step = tbftpb.Step_COMMIT

	require.NotNil(t, tbftImpl.heightRoundVoteSet.getRoundVoteSet(1).Precommits.Maj23)
	vote = NewVote(tbftpb.VoteType_VOTE_PRECOMMIT, tbftImpl.Id, 10, 1, blockHash[:])
	tbftImpl.procPrecommit(vote)
}

// TestProcLocalVote
// @Description: TestProcLocalVote
// @param t
func TestProcLocalVote(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	tbftImpl.RLock()
	defer tbftImpl.RUnlock()
	valSet := &validatorSet{
		logger:             tLogger,
		Validators:         nodes,
		validatorsHeight:   make(map[string]uint64),
		validatorsBeatTime: make(map[string]int64),
	}

	tbftImpl.validatorSet = valSet

	tbftImpl.metrics = newHeightMetrics(1)
	tbftImpl.heightRoundVoteSet = newHeightRoundVoteSet(
		cmLogger, 10, 1, tbftImpl.validatorSet)
	tbftImpl.Id = tbftImpl.validatorSet.Validators[3]
	tbftImpl.Height = 10
	tbftImpl.Round = 1

	blockHash := sha256.Sum256(nil)
	_, _ = rand.Read(blockHash[:])
	vote := NewVote(tbftpb.VoteType_VOTE_PRECOMMIT, tbftImpl.validatorSet.Validators[0], 10, 1, blockHash[:])
	_, _ = tbftImpl.heightRoundVoteSet.addVote(vote)
	vote = NewVote(tbftpb.VoteType_VOTE_PRECOMMIT, tbftImpl.validatorSet.Validators[1], 10, 1, blockHash[:])
	_, _ = tbftImpl.heightRoundVoteSet.addVote(vote)
	vote = NewVote(tbftpb.VoteType_VOTE_PRECOMMIT, tbftImpl.validatorSet.Validators[2], 10, 1, blockHash[:])
	_, _ = tbftImpl.heightRoundVoteSet.addVote(vote)
	tbftImpl.Step = tbftpb.Step_COMMIT

	vote = NewVote(tbftpb.VoteType_VOTE_PRECOMMIT, tbftImpl.validatorSet.Validators[0], 10, 1, blockHash[:])
	tbftImpl.procLocalVote(vote)

	vote = NewVote(tbftpb.VoteType_VOTE_PRECOMMIT, tbftImpl.Id, 10, 1, blockHash[:])
	tbftImpl.procLocalVote(vote)
	vote = NewVote(tbftpb.VoteType_VOTE_PRECOMMIT, tbftImpl.Id, 10, 1, blockHash[1:])
	tbftImpl.procLocalVote(vote)
}

func TestConsensusTBFTImpl_OnMessage(t *testing.T) {
	msg := &msgbus.Message{
		Topic:   msgbus.ProposedBlock,
		Payload: 1,
	}
	tbftImpl.Lock()
	defer tbftImpl.Unlock()
	// clear channels
	var done bool
	for !done {
		select {
		case <-tbftImpl.blockHeightC:
		case <-tbftImpl.proposedBlockC:
		case <-tbftImpl.verifyResultC:
		case <-tbftImpl.externalMsgC:
		case <-tbftImpl.internalMsgC:
		default:
			done = true
		}
	}

	// test ProposeBlock with wrong payload
	tbftImpl.Height = 5
	tbftImpl.OnMessage(msg)

	// test ProposeBlock with nil payload
	var proposal *consensuspb.ProposalBlock
	msg.Payload = proposal
	tbftImpl.OnMessage(msg)

	// test ProposeBlock with nil block header
	proposal = new(consensuspb.ProposalBlock)
	proposal.Block = new(commonpb.Block)
	msg.Payload = proposal
	tbftImpl.OnMessage(msg)

	// test ProposeBlock with behind block height
	proposal.Block.Header = &commonpb.BlockHeader{
		BlockHeight: 0,
	}
	msg.Payload = proposal
	tbftImpl.OnMessage(msg)
	require.Equal(t, 0, len(tbftImpl.proposedBlockC))

	// test VerifyResult with wrong payload
	msg = &msgbus.Message{
		Topic:   msgbus.VerifyResult,
		Payload: 1,
	}
	tbftImpl.OnMessage(msg)

	// test VerifyResult with nil VerifyResult
	var verifyResult *consensuspb.VerifyResult
	msg.Payload = verifyResult
	tbftImpl.OnMessage(msg)
	verifyResult = &consensuspb.VerifyResult{
		VerifiedBlock: &commonpb.Block{
			Header: &commonpb.BlockHeader{
				BlockHeight: 0,
			},
		},
	}
	// test VerifyResult with behind height
	msg.Payload = verifyResult
	tbftImpl.OnMessage(msg)
	require.Equal(t, 0, len(tbftImpl.verifyResultC))
}

func TestConsensusTBFTImpl_OnMessage_ReceiveProposal(t *testing.T) {
	tbftImpl.Lock()
	defer tbftImpl.Unlock()
	// clear channels
	var done bool
	for !done {
		select {
		case <-tbftImpl.blockHeightC:
		case <-tbftImpl.proposedBlockC:
		case <-tbftImpl.verifyResultC:
		case <-tbftImpl.externalMsgC:
		case <-tbftImpl.internalMsgC:
		default:
			done = true
		}
	}
	tbftImpl.Height = 5
	// test external consensus message with wrong payload
	msg := &msgbus.Message{
		Topic:   msgbus.RecvConsensusMsg,
		Payload: 1,
	}
	tbftImpl.OnMessage(msg)

	// test external Proposal with wrong payload
	netMsg := &netpb.NetMsg{
		Type:    netpb.NetMsg_CONSENSUS_MSG,
		Payload: []byte{222},
	}
	msg.Payload = netMsg
	tbftImpl.OnMessage(msg)

	// test external Proposal with wrong proposal
	tbftMsg := &tbftpb.TBFTMsg{
		Type: tbftpb.TBFTMsgType_MSG_PROPOSE,
		Msg:  []byte{222},
	}
	netMsg.Payload = mustMarshal(tbftMsg)
	msg.Payload = netMsg
	tbftImpl.OnMessage(msg)

	header := &commonpb.BlockHeader{
		BlockHeight: 0,
		TxCount:     1,
	}
	endorsement := &commonpb.EndorsementEntry{
		Signature: []byte{},
		Signer:    &accesscontrol.Member{},
	}
	// test external Proposal without Endorsement
	externalProposal := &tbftpb.Proposal{
		Block: &commonpb.Block{
			Header: header,
		},
	}
	tbftMsg.Msg = mustMarshal(externalProposal)
	netMsg.Payload = mustMarshal(tbftMsg)
	msg.Payload = netMsg
	tbftImpl.OnMessage(msg)

	// test external Proposal without block header
	externalProposal.Block.Header = nil
	externalProposal.Endorsement = endorsement
	tbftMsg.Msg = mustMarshal(externalProposal)
	netMsg.Payload = mustMarshal(tbftMsg)
	msg.Payload = netMsg
	tbftImpl.OnMessage(msg)

	// test external Proposal with wrong block height
	externalProposal.Block.Header = header
	tbftMsg.Msg = mustMarshal(externalProposal)
	netMsg.Payload = mustMarshal(tbftMsg)
	msg.Payload = netMsg
	tbftImpl.OnMessage(msg)
	require.Equal(t, 0, len(tbftImpl.externalMsgC))
}

func TestConsensusTBFTImpl_OnMessage_ReceiveVote(t *testing.T) {
	tbftImpl.Lock()
	defer tbftImpl.Unlock()
	// clear channels
	var done bool
	for !done {
		select {
		case <-tbftImpl.blockHeightC:
		case <-tbftImpl.proposedBlockC:
		case <-tbftImpl.verifyResultC:
		case <-tbftImpl.externalMsgC:
		case <-tbftImpl.internalMsgC:
		default:
			done = true
		}
	}
	tbftImpl.Height = 5

	// test external prevote with wrong prevote
	tbftMsg := &tbftpb.TBFTMsg{
		Type: tbftpb.TBFTMsgType_MSG_PREVOTE,
		Msg:  []byte{222},
	}
	netMsg := &netpb.NetMsg{
		Type:    netpb.NetMsg_CONSENSUS_MSG,
		Payload: mustMarshal(tbftMsg),
	}
	msg := &msgbus.Message{
		Topic:   msgbus.RecvConsensusMsg,
		Payload: netMsg,
	}
	tbftImpl.OnMessage(msg)

	// test external Proposal without Endorsement
	externalPrevote := &tbftpb.Vote{}
	tbftMsg.Msg = mustMarshal(externalPrevote)
	netMsg.Payload = mustMarshal(tbftMsg)
	msg.Payload = netMsg
	tbftImpl.OnMessage(msg)
	require.Equal(t, 0, len(tbftImpl.externalMsgC))

	// test external prevote with wrong precommit
	tbftMsg = &tbftpb.TBFTMsg{
		Type: tbftpb.TBFTMsgType_MSG_PRECOMMIT,
		Msg:  []byte{222},
	}
	netMsg.Payload = mustMarshal(tbftMsg)
	msg.Payload = netMsg
	tbftImpl.OnMessage(msg)

	// test external Proposal without Endorsement
	externalPrecommit := &tbftpb.Vote{}
	tbftMsg.Msg = mustMarshal(externalPrecommit)
	netMsg.Payload = mustMarshal(tbftMsg)
	msg.Payload = netMsg
	tbftImpl.OnMessage(msg)
	require.Equal(t, 0, len(tbftImpl.externalMsgC))
}

type fakeConsensusExtendHandler struct{}

func (f *fakeConsensusExtendHandler) CreateRWSet(preBlkHash []byte, proposedBlock *consensuspb.ProposalBlock) error {
	return fmt.Errorf("CreateRWSet failed")
}

func (f *fakeConsensusExtendHandler) VerifyConsensusArgs(
	block *commonpb.Block, blockTxRwSet map[string]*commonpb.TxRWSet) error {
	return fmt.Errorf("VerifyConsensusArgs failed")
}

func (f *fakeConsensusExtendHandler) GetValidators() ([]string, error) {
	return nil, nil
}

// newMockSigner
func newMockSigner(ctrl *gomock.Controller) protocol.SigningMember {
	signer := mock.NewMockSigningMember(ctrl)
	signer.EXPECT().Sign(gomock.Any(), gomock.Any()).Return([]byte("123"), nil).AnyTimes()
	//mock GetMember
	signer.EXPECT().GetMember().DoAndReturn(
		func() (*accesscontrol.Member, error) {
			return &accesscontrol.Member{
				OrgId:      validator1,
				MemberType: accesscontrol.MemberType_CERT,
				MemberInfo: []byte(validator1),
			}, nil
		}).AnyTimes()

	return signer
}

func TestConsensusTBFTImpl_handleProposedBlock(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	tmpTbftImpl := newTbftImpl()
	tmpTbftImpl.Height = 5
	tmpTbftImpl.Id = validator1
	tmpTbftImpl.validatorSet.blocksPerProposer = DefaultBlocksPerProposer
	block := &commonpb.Block{
		Header: &commonpb.BlockHeader{
			BlockHeight: 1,
			BlockHash:   nilHash,
		},
	}
	proposed := &proposedProposal{
		proposedBlock: NewProposalBlock(block, nil),
	}
	// wrong height
	tmpTbftImpl.handleProposedBlock(proposed)
	// wrong proposer
	proposed.proposedBlock.Block.Header.BlockHeight = 5
	tmpTbftImpl.Id = validator0
	tmpTbftImpl.handleProposedBlock(proposed)
	// wrong step
	tmpTbftImpl.Id = validator1
	tmpTbftImpl.Step = tbftpb.Step_COMMIT
	tmpTbftImpl.handleProposedBlock(proposed)
	// has extendHandler and createRwSet failed
	tmpTbftImpl.extendHandler = &fakeConsensusExtendHandler{}
	tmpTbftImpl.Step = tbftpb.Step_PROPOSE
	tmpTbftImpl.handleProposedBlock(proposed)
	// success
	myConfig := &configpb.ChainConfig{
		Crypto: &configpb.CryptoConfig{
			Hash: "SHA256",
		},
	}
	tmpTbftImpl.extendHandler = nil
	tmpTbftImpl.chainConf, _ = chainconf.NewChainConf()
	tmpTbftImpl.chainConf.SetChainConfig(myConfig)
	tmpTbftImpl.signer = newMockSigner(ctrl)
	tmpTbftImpl.ProposeOptimalTimer = time.NewTimer(1 * time.Second)
	tmpTbftImpl.metrics = newHeightMetrics(tmpTbftImpl.Height)
	tmpTbftImpl.internalMsgC = make(chan *ConsensusMsg, 10)
	tmpTbftImpl.handleProposedBlock(proposed)
	require.Equal(t, 1, len(tmpTbftImpl.internalMsgC))
}

func newTmpTbftImpl(ctrl *gomock.Controller, validators []string) *ConsensusTBFTImpl {
	tmpTbftImpl := newTbftImpl()
	tmpTbftImpl.Height = 5
	tmpTbftImpl.Round = 0
	tmpTbftImpl.Id = validator1
	tmpTbftImpl.Step = tbftpb.Step_PROPOSE
	tmpTbftImpl.chainConf, _ = chainconf.NewChainConf()
	tmpTbftImpl.ProposeOptimalTimer = time.NewTimer(5 * time.Second)
	tmpTbftImpl.metrics = newHeightMetrics(tmpTbftImpl.Height)
	tmpTbftImpl.internalMsgC = make(chan *ConsensusMsg, 10)
	tmpTbftImpl.externalMsgC = make(chan *ConsensusMsg, 10)
	myConfig := &configpb.ChainConfig{
		Crypto: &configpb.CryptoConfig{
			Hash: "SHA256",
		},
	}
	tmpTbftImpl.chainConf.SetChainConfig(myConfig)
	tmpTbftImpl.signer = newMockSigner(ctrl)
	tmpTbftImpl.validatorSet = &validatorSet{
		logger:             tLogger,
		Validators:         validators,
		validatorsHeight:   make(map[string]uint64),
		validatorsBeatTime: make(map[string]int64),
	}
	tmpTbftImpl.validatorSet.blocksPerProposer = DefaultBlocksPerProposer
	tmpTbftImpl.heightRoundVoteSet = newHeightRoundVoteSet(
		cmLogger, 5, 0, tmpTbftImpl.validatorSet)
	return tmpTbftImpl
}

func TestConsensusTBFTImpl_handleVerifyResult(t *testing.T) {
	rightHash, wrongHash := []byte("right_hash"), []byte("wrong_hash")
	validators := []string{validator0, validator1}
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	tmpTbftImpl := newTmpTbftImpl(ctrl, validators)
	// verifying proposal is nil
	result := &consensuspb.VerifyResult{
		VerifiedBlock: &commonpb.Block{
			Header: &commonpb.BlockHeader{
				BlockHeight: 5,
				BlockHash:   rightHash,
			},
		},
	}
	tmpTbftImpl.VerifingProposal = nil
	tmpTbftImpl.handleVerifyResult(result)
	// verify failed
	verifingProposal := &TBFTProposal{
		PbMsg: &tbftpb.Proposal{
			Height: 5,
			Round:  0,
			Block: &commonpb.Block{
				Header: &commonpb.BlockHeader{
					BlockHeight: 5,
					BlockHash:   rightHash,
				},
			},
		},
	}
	tmpTbftImpl.VerifingProposal = verifingProposal
	result.Code = consensuspb.VerifyResult_FAIL
	result.RwSetVerifyFailTxs = &consensuspb.RwSetVerifyFailTxs{
		BlockHeight: 5,
		TxIds:       []string{"tx1", "tx2"},
	}
	tmpTbftImpl.handleVerifyResult(result)
	// can commit
	vote := NewVote(tbftpb.VoteType_VOTE_PRECOMMIT, validators[0], 5, 0, rightHash)
	_, _ = tmpTbftImpl.heightRoundVoteSet.addVote(vote)
	vote = NewVote(tbftpb.VoteType_VOTE_PRECOMMIT, validators[1], 5, 0, rightHash)
	_, _ = tmpTbftImpl.heightRoundVoteSet.addVote(vote)
	result.Code = consensuspb.VerifyResult_SUCCESS
	tmpTbftImpl.VerifingProposal = verifingProposal
	result.RwSetVerifyFailTxs = nil
	tmpTbftImpl.handleVerifyResult(result)
	require.NotEmpty(t, tmpTbftImpl.VerifingProposal.PbMsg.Block.AdditionalData.ExtraData[TBFTAddtionalDataKey])
	// wrong hash
	tmpTbftImpl.Step = tbftpb.Step_PROPOSE
	result.VerifiedBlock.Header.BlockHash = wrongHash
	tmpTbftImpl.VerifingProposal = verifingProposal
	tmpTbftImpl.handleVerifyResult(result)
	// has extendHandler and VerifyConsensusArgs failed
	tmpTbftImpl.Step = tbftpb.Step_PROPOSE
	tmpTbftImpl.extendHandler = &fakeConsensusExtendHandler{}
	tmpTbftImpl.VerifingProposal = verifingProposal
	result.VerifiedBlock.Header.BlockHash = rightHash
	tmpTbftImpl.heightRoundVoteSet = newHeightRoundVoteSet(
		cmLogger, 5, 0, tmpTbftImpl.validatorSet)
	tmpTbftImpl.handleVerifyResult(result)
	// success
	tmpTbftImpl.Step = tbftpb.Step_PROPOSE
	tmpTbftImpl.extendHandler = nil
	tmpTbftImpl.heightRoundVoteSet = newHeightRoundVoteSet(
		cmLogger, 5, 0, tmpTbftImpl.validatorSet)
	tmpTbftImpl.VerifingProposal = verifingProposal
	tmpTbftImpl.handleVerifyResult(result)
	require.Equal(t, 2, len(tmpTbftImpl.internalMsgC))
}

func TestConsensusTBFTImpl_procPropose(t *testing.T) {
	rightHash, wrongHash := []byte("right_hash"), []byte("wrong_hash")
	validators := []string{validator0, validator1}
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	tmpTbftImpl := newTmpTbftImpl(ctrl, validators)
	tmpTbftImpl.consensusFutureMsgCache = newConsensusFutureMsgCache(
		tmpTbftImpl.logger, defaultConsensusFutureCacheSize, 0)
	tmpTbftImpl.blockVersion = 2040000
	mockAc := mock.NewMockAccessControlProvider(ctrl)
	mockAc.EXPECT().CreatePrincipal(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	mockAc.EXPECT().VerifyMsgPrincipal(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	tmpTbftImpl.ac = mockAc
	mockVerifier := mock.NewMockBlockVerifier(ctrl)
	mockVerifier.EXPECT().VerifyBlockWithRwSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	tmpTbftImpl.blockVerifier = mockVerifier
	// proposal is nil
	tmpTbftImpl.procPropose(nil)
	// future proposal
	proposal := &tbftpb.Proposal{
		Block: &commonpb.Block{
			Header: &commonpb.BlockHeader{
				BlockHeight: 5,
				BlockHash:   rightHash,
			},
		},
		Voter:  validator1,
		Height: 5,
		Round:  1,
	}
	tmpTbftImpl.procPropose(proposal)
	// wrong step
	proposal.Round = 0
	tmpTbftImpl.Step = tbftpb.Step_PREVOTE
	proposal.Block.Header.BlockHeight = 5
	tmpTbftImpl.procPropose(proposal)
	// wrong proposer
	proposal.Voter = validator0
	tmpTbftImpl.Step = tbftpb.Step_PROPOSE
	tmpTbftImpl.procPropose(proposal)
	// duplicate proposal
	implProposal := &TBFTProposal{
		PbMsg: &tbftpb.Proposal{
			Block: &commonpb.Block{
				Header: &commonpb.BlockHeader{
					BlockHeight: 5,
					BlockHash:   rightHash,
				},
			},
			Voter: validator1,
		},
	}
	tmpTbftImpl.Proposal = implProposal
	proposal.Voter = validator1
	tmpTbftImpl.procPropose(proposal)
	// wrong block hash
	proposal.Block.Header.BlockHash = wrongHash
	tmpTbftImpl.procPropose(proposal)
	// verifying
	tmpTbftImpl.VerifingProposal = implProposal
	tmpTbftImpl.Proposal = nil
	proposal.Block.Header.BlockHash = rightHash
	tmpTbftImpl.procPropose(proposal)
	// unequal verifying
	proposal.Block.Header.BlockHash = wrongHash
	tmpTbftImpl.procPropose(proposal)
	// with qc and rwsets
	tmpTbftImpl.VerifingProposal = nil
	proposal.Block.Header.BlockHash = rightHash
	proposal.Qc = []*tbftpb.Vote{
		{Type: tbftpb.VoteType_VOTE_PREVOTE, Voter: validator0, Height: 5, Hash: rightHash},
		{Type: tbftpb.VoteType_VOTE_PREVOTE, Voter: validator1, Height: 5, Hash: rightHash},
	}
	proposal.TxsRwSet = make(map[string]*commonpb.TxRWSet)
	tmpTbftImpl.procPropose(proposal)
	require.Equal(t, tmpTbftImpl.Step, tbftpb.Step_PREVOTE)
	// to verify
	proposal.Qc, proposal.TxsRwSet = nil, nil
	tmpTbftImpl.Step, tmpTbftImpl.Proposal = tbftpb.Step_PROPOSE, nil
	tmpTbftImpl.procPropose(proposal)
	require.NotNil(t, tmpTbftImpl.VerifingProposal)
	tmpTbftImpl.getFutureMsgFromCache(5, 1)
	require.Equal(t, 1, len(tmpTbftImpl.externalMsgC))
}

func TestConsensusTBFTImpl_saveWalEntry(t *testing.T) {
	rightHash := []byte("right_hash")
	validators := []string{validator0, validator1}
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	tmpTbftImpl := newTmpTbftImpl(ctrl, validators)
	var err error
	consensusConfig := &configpb.ConsensusConfig{
		ExtConfig: []*configpb.ConfigKeyValue{
			{Key: wal_service.WALWriteModeKey, Value: "0"},
		},
	}
	tmpTbftImpl.wal, tmpTbftImpl.walWriteMode, err = InitLWS(consensusConfig, tmpTbftImpl.chainID, tmpTbftImpl.Id)
	tmpTbftImpl.saveWalEntry(&tbftpb.Proposal{
		Block: &commonpb.Block{
			Header: &commonpb.BlockHeader{
				BlockHeight: 5,
				BlockHash:   rightHash,
			},
		},
		Voter: validator1,
		Qc: []*tbftpb.Vote{
			{Type: tbftpb.VoteType_VOTE_PREVOTE, Voter: validator0, Height: 5, Hash: rightHash},
			{Type: tbftpb.VoteType_VOTE_PREVOTE, Voter: validator1, Height: 5, Hash: rightHash},
		},
	})
	require.Nil(t, err)
	//tmpTbftImpl.replayWal()
	//require.Equal(t, tmpTbftImpl.Step, tbftpb.Step_PRECOMMIT)
}
