/*
Copyright (C) BABEC. All rights reserved.
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0
*/

package utils

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"chainmaker.org/chainmaker/pb-go/v2/config"
	"chainmaker.org/chainmaker/pb-go/v2/consensus"
	"chainmaker.org/chainmaker/pb-go/v2/syscontract"
	"github.com/stretchr/testify/assert"

	"github.com/mr-tron/base58/base58"
	"github.com/stretchr/testify/require"
)

func TestERC20Config_load(t *testing.T) {
	/**
	- key: erc20.total
	  value: 1000000
	- key: erc20.owner
	  value: 5pQfwDwtyA
	- key: erc20.decimals
	  value: 18
	- key: erc20.account:<addr1>
	  value: 800000
	- key: erc20.account:<addr2>
	  value: 200000
	*/
	var (
		//stakeHash1 = sha256.Sum256([]byte("stake1"))
		//stakeAddr1 = base58.Encode(stakeHash1[:])
		//
		//stakeHash2 = sha256.Sum256([]byte("stake2"))
		//stakeAddr2 = base58.Encode(stakeHash2[:])
		//
		//stakeHash3 = sha256.Sum256([]byte("stake3"))
		//stakeAddr3 = base58.Encode(stakeHash3[:])

		contractAddr = getContractAddress()

		hash  = sha256.Sum256([]byte("owner"))
		owner = base58.Encode(hash[:])
	)

	var tests = []*config.ConfigKeyValue{
		{
			Key:   keyERC20Total,
			Value: "1000000",
		},
		{
			Key:   keyERC20Owner,
			Value: owner,
		},
		{
			Key:   keyERC20Decimals,
			Value: "18",
		},
		{
			Key:   keyERC20Acc + owner,
			Value: "800000",
		},
		{
			Key:   keyERC20Acc + syscontract.SystemContract_DPOS_STAKE.String(),
			Value: "200000",
		},
	}
	erc20Config, err := loadERC20Config(tests)
	require.Nil(t, err)
	require.NotNil(t, erc20Config)
	require.Equal(t, "1000000", erc20Config.total.String())
	require.Equal(t, owner, erc20Config.owner)
	require.Equal(t, "18", erc20Config.decimals.String())
	ownerToken := erc20Config.loadToken(owner)
	require.Equal(t, "800000", ownerToken.String())
	contractAddrToken := erc20Config.loadToken(contractAddr)
	require.Equal(t, "200000", contractAddrToken.String())
	err = erc20Config.legal()
	require.Nil(t, err)
	txWrites := erc20Config.toTxWrites()
	require.Equal(t, 5, len(txWrites))
}
func TestGenConfigTxRWSet(t *testing.T) {
	table := []struct {
		Version string
		Pass    bool
	}{
		{"v2.1.0", false},
		{"v2.2.0", true}}
	for _, row := range table {
		t.Run(row.Version, func(tt *testing.T) {

			func(version string, contain bool) {
				chainConfig := &config.ChainConfig{ChainId: "chain1", Version: version, Consensus: &config.ConsensusConfig{Type: consensus.ConsensusType_SOLO}}
				rwset, err := genConfigTxRWSet(chainConfig, defaultTimestamp)
				assert.Nil(t, err)
				str := ""
				for _, write := range rwset.TxWrites {
					str += fmt.Sprintf("[%s]\t%s\t%x\n", write.ContractName, write.Key, write.Value)
				}
				t.Log(str, contain)
				if contain {
					assert.Contains(t, str, "[CONTRACT_MANAGE]\tContract:T\t")
				} else {
					assert.NotContains(t, str, "[CONTRACT_MANAGE]\tContract:T\t")
				}
			}(row.Version, row.Pass)

		})
	}

}
func TestCreateGenesis(t *testing.T) {

	chainConfig := &config.ChainConfig{ChainId: "chain1", Version: "v2.1.0", Crypto: &config.CryptoConfig{Hash: "SM3"}, Consensus: &config.ConsensusConfig{Type: consensus.ConsensusType_SOLO}}
	genesis, rwset, err := CreateGenesis(chainConfig, "")
	t.Log(genesis)
	for i, rw := range rwset {
		t.Logf("%d", i)
		for _, w := range rw.TxWrites {
			t.Logf("key:%s,value:%s", w.Key, w.Value)
		}
	}
	assert.Nil(t, err)
	assert.True(t, IsConfBlock(genesis))

}
func TestGetBlockHeaderVersion(t *testing.T) {
	tt := map[string]uint32{
		"v2.2.0":       2201,
		"v2.3.0_alpha": 2300,
		"v2.3.0":       2301,
		"v2.2.2":       2220,
		"v2.0.0":       20,
		"v2.2.0_alpha": 220,
		"v2.3.1":       2030100,
		"v2.3.1.7":     2030107,
		"v2.4.0_alpha": 2040000,
		"v2.4.0":       2040001,
		"2030100":      2030100,
	}
	for v, result := range tt {
		intV := GetBlockVersion(v)
		assert.EqualValues(t, result, intV, v)
	}
}

func TestGetUnixTime(t *testing.T) {
	var tests = []struct {
		input       string
		expectedOut int64
		errExpected bool
	}{
		{"2020-11-30T01:01:01+08:00", 1606669261, false},
		{"2024-10-30T00:00:00+00:00", 1730246400, false},
		{"2024-10-30T00:00:00+08:00", 1730217600, false},
		{"2024-10-30T00:00:00Z08:00", 0, true},
		{"2024-10-30T00:00:00", 0, true},
		{"2024-10-30 00:00:00 08:00", 0, true},
		{"2024-10-30", 0, true},
		{"2024-10-30 00:00:00", 0, true},
		{"not valid time", 0, true},
		{"", 0, true},
	}

	for _, testCase := range tests {
		t.Logf("Test Case: %#v\n", testCase)

		actualOutput, actualErr := rfc3339ToUninTime(testCase.input)

		if testCase.errExpected && actualErr == nil {
			t.Fatalf("\texpected error but did not receive one")
		} else if !testCase.errExpected && actualErr != nil {
			t.Fatalf("\tdid not expect error but received one: %v", actualErr)
		}

		require.EqualValues(t, testCase.expectedOut, actualOutput)
	}
}
