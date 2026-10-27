#!/usr/bin/env python3
"""Generate Go Chainmaker contracts with a precise wasmexport entry count.

Each contract includes init_contract + upgrade + (N-2) business methods that
mirror production style: read args, KV state, events, SuccessResult/ErrorResult.

Usage:
  python3 gen_synthetic_go_contract.py --counts 64,32,16,8 -o ../testdata/synthetic_go
"""

from __future__ import annotations

import argparse
from pathlib import Path

GO_MOD = """module {module}

go 1.16

require github.com/TKOTKCh/contract-sdk-go-wasm v0.1.3
"""

BUILD_SH = """#!/bin/bash
set -euo pipefail
export GOROOT="/usr/local/go"
export PATH="$GOROOT/bin:$PATH"
echo "Building {name}-go.wasm with $(go version)"
GOWORK=off GOOS=wasip1 GOARCH=wasm go build -o "{name}-go.wasm"
"""

HEADER = """/*
Copyright (C) BABEC. All rights reserved.
SPDX-License-Identifier: Apache-2.0
*/

package main

import (
\t"fmt"
\t"strconv"

\t"github.com/TKOTKCh/contract-sdk-go-wasm/sdk"
)
"""

INIT_UPGRADE = """
//go:wasmexport init_contract
func initContract() {
\tctx := sdk.NewSimContext()
\tctx.SuccessResult("init ok")
}

//go:wasmexport upgrade
func upgrade() {
\tctx := sdk.NewSimContext()
\tctx.SuccessResult("upgrade ok")
}
"""


def business_method(index: int) -> str:
    kind = index % 3
    name = f"bizMethod{index}"
    if kind == 0:
        return f"""
//go:wasmexport {name}
func {name}() {{
\tctx := sdk.NewSimContext()
\tkey, err1 := ctx.ArgString("key")
\tval, err2 := ctx.ArgString("value")
\tif err1 != sdk.SUCCESS || err2 != sdk.SUCCESS {{
\t\tctx.ErrorResult("missing key or value")
\t\treturn
\t}}
\tctx.PutState("biz{index}", key, val)
\tctx.EmitEvent("biz{index}_saved", key, val)
\tctx.SuccessResult(fmt.Sprintf("saved %s=%s", key, val))
}}
"""
    if kind == 1:
        return f"""
//go:wasmexport {name}
func {name}() {{
\tctx := sdk.NewSimContext()
\tkey, err := ctx.ArgString("key")
\tif err != sdk.SUCCESS {{
\t\tctx.ErrorResult("missing key")
\t\treturn
\t}}
\tdata, code := ctx.GetStateByte("biz{index}", key)
\tif code != sdk.SUCCESS {{
\t\tctx.ErrorResult("not found: " + key)
\t\treturn
\t}}
\tctx.SuccessResultByte(data)
}}
"""
    return f"""
//go:wasmexport {name}
func {name}() {{
\tctx := sdk.NewSimContext()
\tnStr, err := ctx.ArgString("n")
\tif err != sdk.SUCCESS {{
\t\tctx.ErrorResult("missing n")
\t\treturn
\t}}
\tn, convErr := strconv.Atoi(nStr)
\tif convErr != nil {{
\t\tctx.ErrorResult(convErr.Error())
\t\treturn
\t}}
\tsum := 0
\tfor i := 0; i < n; i++ {{
\t\tsum += (i + {index}) % 97
\t}}
\tctx.EmitEvent("biz{index}_calc", nStr, strconv.Itoa(sum))
\tctx.SuccessResult(strconv.Itoa(sum))
}}
"""


def generate_contract(export_count: int) -> str:
    if export_count < 2:
        raise ValueError("export_count must be >= 2 (init + upgrade)")
    parts = [HEADER, INIT_UPGRADE]
    for i in range(export_count - 2):
        parts.append(business_method(i))
    parts.append("\nfunc main() {}\n")
    return "".join(parts)


def write_contract(out_dir: Path, export_count: int) -> Path:
    name = f"funcs-{export_count}"
    contract_dir = out_dir / name
    contract_dir.mkdir(parents=True, exist_ok=True)

    go_file = contract_dir / f"{name}.go"
    go_file.write_text(generate_contract(export_count), encoding="utf-8")

    module = name.replace("-", "_")
    (contract_dir / "go.mod").write_text(GO_MOD.format(module=module), encoding="utf-8")

    build_sh = contract_dir / "build.sh"
    build_sh.write_text(BUILD_SH.format(name=name), encoding="utf-8")
    build_sh.chmod(0o755)

    return contract_dir


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--counts", default="64,32,16,8")
    parser.add_argument("-o", "--out-dir", default="../testdata/synthetic_go")
    args = parser.parse_args()

    out_dir = Path(args.out_dir).resolve()
    counts = [int(x.strip()) for x in args.counts.split(",") if x.strip()]
    for n in counts:
        d = write_contract(out_dir, n)
        biz = n - 2
        print(f"wrote {d} ({n} wasmexport entries: init + upgrade + {biz} business)")


if __name__ == "__main__":
    main()
