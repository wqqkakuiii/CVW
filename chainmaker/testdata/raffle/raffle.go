/*
Copyright (C) BABEC. All rights reserved.
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/TKOTKCh/contract-sdk-go-wasm/sdk"
	"math/big"
	"strconv"
	"strings"
)

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
	ctx := sdk.NewSimContext()
	ctx.SuccessResult("Init contract success")
}

// 升级合约
//
//go:wasmexport upgrade
func Upgrade() {
	ctx := sdk.NewSimContext()
	ctx.SuccessResult("Upgrade contract success")
}

// 批量注册参与者
//
//go:wasmexport registerAll
func registerAll() {
	ctx := sdk.NewSimContext()

	// 获取参数
	peoplesStr, _ := ctx.ArgString("peoples")
	ctx.EmitEvent("peoples", peoplesStr)
	if len(peoplesStr) == 0 {
		ctx.ErrorResult("peoples param should not be empty")
	}

	// 解析数据
	var peoples Peoples
	err1 := json.Unmarshal([]byte(peoplesStr), &peoples)
	if err1 != nil {
		ctx.ErrorResult("invalid peoples")
	}

	// 校验数据
	for i := 0; i < len(peoples.Peoples); i++ {
		if people := peoples.Peoples[i]; len(people.Name) == 0 {
			errMsg := fmt.Sprintf("[registerAll] name should not be empty for number %d", i)
			ctx.ErrorResult(errMsg)
		}
	}

	// 存储数据
	if err := ctx.PutStateByte(peoplesKey, "", []byte(peoplesStr)); err != sdk.SUCCESS {
		ctx.ErrorResult("save peoples failed")
	}

	ctx.SuccessResult("ok")
}

//
//go:wasmexport raffle
func raffle() {
	ctx := sdk.NewSimContext()

	// 获取参数
	level, _ := ctx.ArgString("level")
	argTimestamp, _ := ctx.ArgString("timestamp")

	if len(level) == 0 {
		ctx.ErrorResult("level should not be empty!")
	}
	if len(argTimestamp) == 0 {
		ctx.ErrorResult("argTimestamp should not be empty!")
	}
	// 获取参与者数据
	peoplesData, err := ctx.GetStateByte(peoplesKey, "")
	if err != sdk.SUCCESS {
		ctx.ErrorResult(queryErrorMsg)
	}

	var peoples Peoples
	if err1 := json.Unmarshal(peoplesData, &peoples); err1 != nil {
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
		newPeoples.Peoples = append(newPeoples.Peoples, peoples.Peoples[num+1:]...)
	}

	// 保存新数据
	newPeoplesData, err1 := json.Marshal(newPeoples)
	if err1 != nil {
		errMsg := fmt.Sprintf("marshal new peoples data failed, %s", err1)
		ctx.ErrorResult(errMsg)
	}
	if err := ctx.PutStateByte(peoplesKey, "", newPeoplesData); err != sdk.SUCCESS {
		ctx.ErrorResult("put new peoples data failed")
	}

	ctx.SuccessResult(result)
}

//
//go:wasmexport registRaffle
func registRaffle() {
	ctx := sdk.NewSimContext()
	// 获取参数
	peoplesStr, _ := ctx.ArgString("peoples")
	ctx.EmitEvent("peoples", peoplesStr)
	if len(peoplesStr) == 0 {
		ctx.ErrorResult("peoples param should not be empty")
	}

	// 解析数据
	var registPeoples Peoples
	err1 := json.Unmarshal([]byte(peoplesStr), &registPeoples)
	if err1 != nil {
		ctx.ErrorResult("invalid peoples")
	}

	// 校验数据
	for i := 0; i < len(registPeoples.Peoples); i++ {
		if people := registPeoples.Peoples[i]; len(people.Name) == 0 {
			errMsg := fmt.Sprintf("[registerAll] name should not be empty for number %d", i)
			ctx.ErrorResult(errMsg)
		}
	}

	// 存储数据
	if err := ctx.PutStateByte(peoplesKey, "", []byte(peoplesStr)); err != sdk.SUCCESS {
		ctx.ErrorResult("save peoples failed")
	}

	// 获取参数
	level, _ := ctx.ArgString("level")
	argTimestamp, _ := ctx.ArgString("timestamp")

	if len(level) == 0 {
		ctx.ErrorResult("level should not be empty!")
	}
	if len(argTimestamp) == 0 {
		ctx.ErrorResult("argTimestamp should not be empty!")
	}
	// 获取参与者数据
	peoplesData, err := ctx.GetStateByte(peoplesKey, "")
	if err != sdk.SUCCESS {
		ctx.ErrorResult(queryErrorMsg)
	}

	var peoples Peoples
	if err1 := json.Unmarshal(peoplesData, &peoples); err1 != nil {
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
		newPeoples.Peoples = append(newPeoples.Peoples, peoples.Peoples[num+1:]...)
	}

	// 保存新数据
	newPeoplesData, err1 := json.Marshal(newPeoples)
	if err1 != nil {
		errMsg := fmt.Sprintf("marshal new peoples data failed, %s", err1)
		ctx.ErrorResult(errMsg)
	}
	if err := ctx.PutStateByte(peoplesKey, "", newPeoplesData); err != sdk.SUCCESS {
		ctx.ErrorResult("put new peoples data failed")
	}

	ctx.SuccessResult(result)
}

// 查询参与者
//
//go:wasmexport query
func query() {
	ctx := sdk.NewSimContext()

	data, err := ctx.GetStateByte(peoplesKey, "")
	if err != sdk.SUCCESS {
		ctx.ErrorResult(queryErrorMsg)
	}

	ctx.SuccessResult(string(data))
}

// BKDR哈希算法
func bkdrHash(input string) int {
	hash := 0
	seed := 131
	for _, c := range input {
		hash = hash*seed + int(c)
	}
	return hash & 0x7FFFFFFF // 保证正数
}

//
//go:wasmexport sprintf
func Sprintf() {
	s := fmt.Sprintf("1000")
	_ = s
	return
}

//
//go:wasmexport errorf
func Errorf() {
	s := fmt.Errorf("1000")
	_ = s
	return
}

//go:wasmexport jsonMarshal
func jsonMarshal() {

	var p tstruct
	json.Marshal(p)
}

//go:wasmexport jsonUnmarshal
func jsonUnmarshal() {
	jsonStr := `{}`
	var p tstruct
	json.Unmarshal([]byte(jsonStr), &p)
}

//go:wasmexport parseInt
func ParseInt() {
	strconv.ParseInt("1234567", 16, 32)
}

//go:wasmexport formatInt
func formatInt() {
	strconv.FormatInt(123456, 10)
}

//go:wasmexport normalCal
func normalCal() {
	result := 0
	for i := 0; i < 1000000; i++ {
		result += i
	}
}

//go:wasmexport hashCal
func hashCal() {

	hashInput := "ChainMaker Performance Test"
	var hashResult [32]byte
	for i := 0; i < 10000; i++ {
		hashResult = sha256.Sum256([]byte(hashInput))
	}
	_ = hashResult
}

//go:wasmexport bigNumCal
func bigNumCal() {
	a := big.NewInt(2)
	exp := big.NewInt(100000)
	mod := big.NewInt(1000000007)
	var result *big.Int

	result = new(big.Int).Exp(a, exp, mod)

	_ = result

}

//go:wasmexport stringsJoin
func stringsJoin() {
	result := strings.Join([]string{"a", "b"}, ",")

	_ = result

}

//
//go:wasmexport nolib
func nolib() {
	return
}

//go:wasmexport testgasWarmUp
func testgasWarmUp() {

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

}
