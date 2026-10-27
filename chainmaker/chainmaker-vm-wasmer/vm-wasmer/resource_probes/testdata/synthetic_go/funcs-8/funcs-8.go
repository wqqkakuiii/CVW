/*
Copyright (C) BABEC. All rights reserved.
SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"fmt"
	"strconv"

	"github.com/TKOTKCh/contract-sdk-go-wasm/sdk"
)

//go:wasmexport init_contract
func initContract() {
	ctx := sdk.NewSimContext()
	ctx.SuccessResult("init ok")
}

//go:wasmexport upgrade
func upgrade() {
	ctx := sdk.NewSimContext()
	ctx.SuccessResult("upgrade ok")
}

//go:wasmexport bizMethod0
func bizMethod0() {
	ctx := sdk.NewSimContext()
	key, err1 := ctx.ArgString("key")
	val, err2 := ctx.ArgString("value")
	if err1 != sdk.SUCCESS || err2 != sdk.SUCCESS {
		ctx.ErrorResult("missing key or value")
		return
	}
	ctx.PutState("biz0", key, val)
	ctx.EmitEvent("biz0_saved", key, val)
	ctx.SuccessResult(fmt.Sprintf("saved %s=%s", key, val))
}

//go:wasmexport bizMethod1
func bizMethod1() {
	ctx := sdk.NewSimContext()
	key, err := ctx.ArgString("key")
	if err != sdk.SUCCESS {
		ctx.ErrorResult("missing key")
		return
	}
	data, code := ctx.GetStateByte("biz1", key)
	if code != sdk.SUCCESS {
		ctx.ErrorResult("not found: " + key)
		return
	}
	ctx.SuccessResultByte(data)
}

//go:wasmexport bizMethod2
func bizMethod2() {
	ctx := sdk.NewSimContext()
	nStr, err := ctx.ArgString("n")
	if err != sdk.SUCCESS {
		ctx.ErrorResult("missing n")
		return
	}
	n, convErr := strconv.Atoi(nStr)
	if convErr != nil {
		ctx.ErrorResult(convErr.Error())
		return
	}
	sum := 0
	for i := 0; i < n; i++ {
		sum += (i + 2) % 97
	}
	ctx.EmitEvent("biz2_calc", nStr, strconv.Itoa(sum))
	ctx.SuccessResult(strconv.Itoa(sum))
}

//go:wasmexport bizMethod3
func bizMethod3() {
	ctx := sdk.NewSimContext()
	key, err1 := ctx.ArgString("key")
	val, err2 := ctx.ArgString("value")
	if err1 != sdk.SUCCESS || err2 != sdk.SUCCESS {
		ctx.ErrorResult("missing key or value")
		return
	}
	ctx.PutState("biz3", key, val)
	ctx.EmitEvent("biz3_saved", key, val)
	ctx.SuccessResult(fmt.Sprintf("saved %s=%s", key, val))
}

//go:wasmexport bizMethod4
func bizMethod4() {
	ctx := sdk.NewSimContext()
	key, err := ctx.ArgString("key")
	if err != sdk.SUCCESS {
		ctx.ErrorResult("missing key")
		return
	}
	data, code := ctx.GetStateByte("biz4", key)
	if code != sdk.SUCCESS {
		ctx.ErrorResult("not found: " + key)
		return
	}
	ctx.SuccessResultByte(data)
}

//go:wasmexport bizMethod5
func bizMethod5() {
	ctx := sdk.NewSimContext()
	nStr, err := ctx.ArgString("n")
	if err != sdk.SUCCESS {
		ctx.ErrorResult("missing n")
		return
	}
	n, convErr := strconv.Atoi(nStr)
	if convErr != nil {
		ctx.ErrorResult(convErr.Error())
		return
	}
	sum := 0
	for i := 0; i < n; i++ {
		sum += (i + 5) % 97
	}
	ctx.EmitEvent("biz5_calc", nStr, strconv.Itoa(sum))
	ctx.SuccessResult(strconv.Itoa(sum))
}

func main() {}
