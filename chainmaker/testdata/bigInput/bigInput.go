/*
Copyright (C) BABEC. All rights reserved.
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/TKOTKCh/contract-sdk-go-wasm/sdk"
)

const (
	paramData         = "data"
	paramPartCount    = "part_count"
	paramCount        = "count"
	paramPayloadSize  = "payload_size"
	maxMultiParts     = 128
	defaultIOCount    = 100
	defaultPayloadSz  = 256
	maxIOCount        = 10000
	maxIOPayloadBytes = 4096
)

//go:wasmexport init_contract
func initContract() {
	ctx := sdk.NewSimContext()
	ctx.SuccessResult("Init contract success")
}

//go:wasmexport upgrade
func upgrade() {
	ctx := sdk.NewSimContext()
	ctx.SuccessResult("Upgrade contract success")
}

// inputSize reads a string parameter "data" (may be very large) and returns its byte length.
//
//go:wasmexport inputSize
func inputSize() {
	ctx := sdk.NewSimContext()

	data, err := ctx.ArgString(paramData)
	if err != sdk.SUCCESS {
		ctx.ErrorResult("missing or invalid param: data")
		return
	}

	size := len(data)
	ctx.SuccessResult(fmt.Sprintf("%d", size))
}

// inputSizeMulti reads sharded params data0..data{N-1} (each ≤1MB per EasyCodec) and returns total byte length.
// Required: part_count (decimal string). Optional keys: data0, data1, ...
//
//go:wasmexport inputSizeMulti
func inputSizeMulti() {
	ctx := sdk.NewSimContext()

	countStr, err := ctx.ArgString(paramPartCount)
	if err != sdk.SUCCESS {
		ctx.ErrorResult("missing or invalid param: part_count")
		return
	}
	count, convErr := strconv.Atoi(countStr)
	if convErr != nil || count <= 0 || count > maxMultiParts {
		ctx.ErrorResult("invalid part_count")
		return
	}

	total := 0
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("data%d", i)
		part, err := ctx.ArgString(key)
		if err != sdk.SUCCESS {
			ctx.ErrorResult(fmt.Sprintf("missing or invalid param: %s", key))
			return
		}
		total += len(part)
	}
	ctx.SuccessResult(fmt.Sprintf("%d", total))
}

// sleep5 does nothing except sleep 5 seconds, then returns ok.
//
//go:wasmexport sleep5
func sleep5() {
	ctx := sdk.NewSimContext()
	time.Sleep(5 * time.Second)
	ctx.SuccessResult("ok")
}

// ioHeavy performs count times PutState+GetState (chain state I/O via host syscall).
// Optional params: count (default 100, max 10000), payload_size (default 256, max 4096).
//
//go:wasmexport ioHeavy
func ioHeavy() {
	ctx := sdk.NewSimContext()

	count := defaultIOCount
	if s, err := ctx.ArgString(paramCount); err == sdk.SUCCESS && s != "" {
		n, convErr := strconv.Atoi(s)
		if convErr != nil || n <= 0 || n > maxIOCount {
			ctx.ErrorResult("invalid param: count")
			return
		}
		count = n
	}

	payloadSize := defaultPayloadSz
	if s, err := ctx.ArgString(paramPayloadSize); err == sdk.SUCCESS && s != "" {
		n, convErr := strconv.Atoi(s)
		if convErr != nil || n <= 0 || n > maxIOPayloadBytes {
			ctx.ErrorResult("invalid param: payload_size")
			return
		}
		payloadSize = n
	}

	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = 'X'
	}

	txId, _ := ctx.GetTxId()
	done := 0
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("io_%s_%d", txId, i)
		if code := ctx.PutStateByte(key, "v", payload); code != sdk.SUCCESS {
			ctx.ErrorResult(fmt.Sprintf("PutState failed at %d", i))
			return
		}
		got, code := ctx.GetStateByte(key, "v")
		if code != sdk.SUCCESS || len(got) != payloadSize {
			ctx.ErrorResult(fmt.Sprintf("GetState failed at %d", i))
			return
		}
		done++
	}
	ctx.SuccessResult(strconv.Itoa(done))
}

func main() {
	sdk.NewSimContext().Args()
}
