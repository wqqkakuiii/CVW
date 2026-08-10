/*
Copyright (C) BABEC. All rights reserved.
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"CVW/registry"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/TKOTKCh/contract-sdk-go-wasm/sdk"
	"math/big"
	"strconv"
	"strings"
)

var GAS = &registry.
	Gas{}

//go:wasmexport SetGas
func SetGas(
	amount uint64) {
	registry.SetGas(amount)
}
//go:wasmexport GetGas
func GetGas() uint64 {
	return registry.GetGas()
}

const (
	peoplesKey    = "peoples"
	queryErrorMsg = "get peoples data failed"
)

type tstruct struct {
}

// People 抽奖参与者信息
type People struct {
	Num  int    `json:"num"`
	Name string `json:"name"`
}

// Peoples 参与者集合
type Peoples struct {
	Peoples []*People `json:"peoples"`
}

// 安装合约
//
//go:wasmexport init_contract
func InitContract() {
	__cvwGasSave := registry.GetGas()
	defer registry.SetGas(__cvwGasSave)
	registry.
		ConsumeGas(5)

	ctx := sdk.NewSimContext()
	ctx.SuccessResult("Init contract success")
}

// 升级合约
//
//go:wasmexport upgrade
func Upgrade() {
	registry.
		ConsumeGas(5)

	ctx := sdk.NewSimContext()
	ctx.SuccessResult("Upgrade contract success")
}

// 批量注册参与者
//
//go:wasmexport registerAll
func registerAll() {
	registry.
		ConsumeGas(28)

	ctx := sdk.NewSimContext()

	// 获取参数
	peoplesStr, _ := ctx.ArgString("peoples")
	ctx.EmitEvent("peoples", peoplesStr)
	if len(peoplesStr) == 0 {
		registry.
			ConsumeGas(8)

		ctx.ErrorResult("peoples param should not be empty")
	}

	// 解析数据
	var peoples Peoples
	err1 := json.Unmarshal([]byte(peoplesStr), &peoples)
	if err1 != nil {
		registry.
			ConsumeGas(6)

		ctx.ErrorResult("invalid peoples")
	}

	// 校验数据
	for i := 0; i < len(peoples.Peoples); i++ {
		registry.
			ConsumeGas(14)

		if people := peoples.Peoples[i]; len(people.Name) == 0 {
			registry.
				ConsumeGas(31)

			errMsg := fmt.Sprintf("[registerAll] name should not be empty for number %d", i)
			ctx.ErrorResult(errMsg)
		}
	}

	// 存储数据
	if err := ctx.PutStateByte(peoplesKey, "", []byte(peoplesStr)); err != sdk.SUCCESS {
		registry.
			ConsumeGas(10)

		ctx.ErrorResult("save peoples failed")
	}

	ctx.SuccessResult("ok")
}

//
//go:wasmexport raffle
func raffle() {
	registry.
		ConsumeGas(114)

	ctx := sdk.NewSimContext()

	// 获取参数
	level, _ := ctx.ArgString("level")
	argTimestamp, _ := ctx.ArgString("timestamp")

	if len(level) == 0 {
		registry.
			ConsumeGas(8)

		ctx.ErrorResult("level should not be empty!")
	}
	if len(argTimestamp) == 0 {
		registry.
			ConsumeGas(8)

		ctx.ErrorResult("argTimestamp should not be empty!")
	}
	// 获取参与者数据
	peoplesData, err := ctx.GetStateByte(peoplesKey, "")
	if err != sdk.SUCCESS {
		registry.
			ConsumeGas(6)

		ctx.ErrorResult(queryErrorMsg)
	}

	var peoples Peoples
	if err1 := json.Unmarshal(peoplesData, &peoples); err1 != nil {
		registry.
			ConsumeGas(19)

		errMsg := fmt.Sprintf("unmarshal peoples data failed, %s", err1)

		ctx.ErrorResult(errMsg)
	}

	// 计算中奖位置
	hashVal := bkdrHash(argTimestamp)
	num := hashVal % len(peoples.Peoples)
	ctx.Log(fmt.Sprintf("raffle position: %d", num))

	// 获取中奖者
	resultPeople := peoples.Peoples[num]
	result := fmt.Sprintf("num: %d, name: %s, level: %s", resultPeople.Num, resultPeople.Name, level)

	// 更新参与者列表
	var newPeoples Peoples
	newPeoples.Peoples = append(newPeoples.Peoples, peoples.Peoples[0:num]...)
	if num+1 < len(peoples.Peoples) {
		registry.
			ConsumeGas(29)

		newPeoples.Peoples = append(newPeoples.Peoples, peoples.Peoples[num+1:]...)
	}

	// 保存新数据
	newPeoplesData, err1 := json.Marshal(newPeoples)
	if err1 != nil {
		registry.
			ConsumeGas(17)

		errMsg := fmt.Sprintf("marshal new peoples data failed, %s", err1)
		ctx.ErrorResult(errMsg)
	}
	if err := ctx.PutStateByte(peoplesKey, "", newPeoplesData); err != sdk.SUCCESS {
		registry.
			ConsumeGas(8)

		ctx.ErrorResult("put new peoples data failed")
	}

	ctx.SuccessResult(result)
}

//
//go:wasmexport registRaffle
func registRaffle() {
	registry.
		ConsumeGas(137)

	ctx := sdk.NewSimContext()
	// 获取参数
	peoplesStr, _ := ctx.ArgString("peoples")
	ctx.EmitEvent("peoples", peoplesStr)
	if len(peoplesStr) == 0 {
		registry.
			ConsumeGas(8)

		ctx.ErrorResult("peoples param should not be empty")
	}

	// 解析数据
	var registPeoples Peoples
	err1 := json.Unmarshal([]byte(peoplesStr), &registPeoples)
	if err1 != nil {
		registry.
			ConsumeGas(6)

		ctx.ErrorResult("invalid peoples")
	}

	// 校验数据
	for i := 0; i < len(registPeoples.Peoples); i++ {
		registry.
			ConsumeGas(14)

		if people := registPeoples.Peoples[i]; len(people.Name) == 0 {
			registry.
				ConsumeGas(31)

			errMsg := fmt.Sprintf("[registerAll] name should not be empty for number %d", i)
			ctx.ErrorResult(errMsg)
		}
	}

	// 存储数据
	if err := ctx.PutStateByte(peoplesKey, "", []byte(peoplesStr)); err != sdk.SUCCESS {
		registry.
			ConsumeGas(10)

		ctx.ErrorResult("save peoples failed")
	}

	// 获取参数
	level, _ := ctx.ArgString("level")
	argTimestamp, _ := ctx.ArgString("timestamp")

	if len(level) == 0 {
		registry.
			ConsumeGas(8)

		ctx.ErrorResult("level should not be empty!")
	}
	if len(argTimestamp) == 0 {
		registry.
			ConsumeGas(8)

		ctx.ErrorResult("argTimestamp should not be empty!")
	}
	// 获取参与者数据
	peoplesData, err := ctx.GetStateByte(peoplesKey, "")
	if err != sdk.SUCCESS {
		registry.
			ConsumeGas(6)

		ctx.ErrorResult(queryErrorMsg)
	}

	var peoples Peoples
	if err1 := json.Unmarshal(peoplesData, &peoples); err1 != nil {
		registry.
			ConsumeGas(19)

		errMsg := fmt.Sprintf("unmarshal peoples data failed, %s", err1)

		ctx.ErrorResult(errMsg)
	}

	// 计算中奖位置
	hashVal := bkdrHash(argTimestamp)
	num := hashVal % len(peoples.Peoples)
	ctx.Log(fmt.Sprintf("raffle position: %d", num))

	// 获取中奖者
	resultPeople := peoples.Peoples[num]
	result := fmt.Sprintf("num: %d, name: %s, level: %s", resultPeople.Num, resultPeople.Name, level)

	// 更新参与者列表
	var newPeoples Peoples
	newPeoples.Peoples = append(newPeoples.Peoples, peoples.Peoples[0:num]...)
	if num+1 < len(peoples.Peoples) {
		registry.
			ConsumeGas(29)

		newPeoples.Peoples = append(newPeoples.Peoples, peoples.Peoples[num+1:]...)
	}

	// 保存新数据
	newPeoplesData, err1 := json.Marshal(newPeoples)
	if err1 != nil {
		registry.
			ConsumeGas(17)

		errMsg := fmt.Sprintf("marshal new peoples data failed, %s", err1)
		ctx.ErrorResult(errMsg)
	}
	if err := ctx.PutStateByte(peoplesKey, "", newPeoplesData); err != sdk.SUCCESS {
		registry.
			ConsumeGas(8)

		ctx.ErrorResult("put new peoples data failed")
	}

	ctx.SuccessResult(result)
}

// 查询参与者
//
//go:wasmexport query
func query() {
	registry.
		ConsumeGas(13)

	ctx := sdk.NewSimContext()

	data, err := ctx.GetStateByte(peoplesKey, "")
	if err != sdk.SUCCESS {
		registry.
			ConsumeGas(6)

		ctx.ErrorResult(queryErrorMsg)
	}

	ctx.SuccessResult(string(data))
}

// BKDR哈希算法
func bkdrHash(input string) int {
	registry.
		ConsumeGas(10)

	hash := 0
	seed := 131
	for _, c := range input {
		registry.
			ConsumeGas(12)

		hash = hash*seed + int(c)
	}
	return hash & 0x7FFFFFFF // 保证正数
}

//
//go:wasmexport sprintf
func Sprintf() {
	registry.
		ConsumeGas(3)

	s := fmt.Sprintf("1000")
	_ = s
	return
}

//
//go:wasmexport errorf
func Errorf() {
	registry.
		ConsumeGas(3)

	s := fmt.Errorf("1000")
	_ = s
	return
}

//go:wasmexport jsonMarshal
func jsonMarshal() {
	registry.
		ConsumeGas(5)

	var p tstruct
	json.Marshal(p)
}

//go:wasmexport jsonUnmarshal
func jsonUnmarshal() {
	registry.
		ConsumeGas(9)

	jsonStr := `{}`
	var p tstruct
	json.Unmarshal([]byte(jsonStr), &p)
}

//go:wasmexport parseInt
func ParseInt() {
	registry.
		ConsumeGas(3)

	strconv.ParseInt("1234567", 16, 32)
}

//go:wasmexport formatInt
func formatInt() {
	registry.
		ConsumeGas(3)

	strconv.FormatInt(123456, 10)
}

//go:wasmexport normalCal
func normalCal() {
	registry.
		ConsumeGas(2)

	result := 0
	for i := 0; i < 1000000; i++ {
		registry.
			ConsumeGas(10)

		result += i
	}
}

//go:wasmexport hashCal
func hashCal() {

	hashInput := "ChainMaker Performance Test"
	var hashResult [32]byte
	for i := 0; i < 10000; i++ {
		registry.
			ConsumeGas(12)

		hashResult = sha256.Sum256([]byte(hashInput))
	}
	_ = hashResult
}

//go:wasmexport bigNumCal
func bigNumCal() {
	registry.
		ConsumeGas(11)

	a := big.NewInt(2)
	exp := big.NewInt(100000)
	mod := big.NewInt(1000000007)
	var result *big.Int

	result = new(big.Int).Exp(a, exp, mod)

	_ = result

}

//go:wasmexport stringsJoin
func stringsJoin() {
	registry.
		ConsumeGas(13)

	result := strings.Join([]string{"a", "b"}, ",")

	_ = result

}

//
//go:wasmexport nolib
func nolib() {
	registry.
		ConsumeGas(1)

	return
}

//go:wasmexport testgasWarmUp
func testgasWarmUp() {
	registry.
		ConsumeGas(19)

	hashCal()
	normalCal()
	ParseInt()
	jsonUnmarshal()
	jsonMarshal()
	Sprintf()
	Errorf()
	bigNumCal()
	stringsJoin()
}
func main() {
	registry.
		Register("gas", GAS)
	registry.
		SetGas(9223372036854775807)

}
