#!/usr/bin/env python3
"""Generate synthetic wasm (as WAT text) with a precise local function count.

Wasmer-go's NewModule path calls Wat2Wasm(), which accepts WAT text but
truncates binary wasm at NUL bytes, so we emit WAT files (*.wat).

Usage:
  python3 gen_synthetic_funcs_wasm.py --counts 64,32,16,8 -o ../testdata/synthetic
"""

from __future__ import annotations

import argparse
from pathlib import Path


def function_body(func_index: int) -> list[str]:
    seed = (func_index * 131 + 17) & 0x7FFF
    tweak = (func_index * 7 + 3) & 0xFF
    lines = [
        "    (local i32)",
        "    local.get 0",
        f"    i32.const {seed}",
        "    i32.add",
        "    local.set 1",
    ]
    # Straight-line ops inflate Cranelift IR; loops optimize to tiny IR.
    for step in range(80):
        k = (seed + step * 17 + func_index) & 0xFFFF
        lines.extend(
            [
                "    local.get 1",
                f"    i32.const {k}",
                "    i32.mul",
                f"    i32.const {tweak + step}",
                "    i32.add",
                "    local.set 1",
            ]
        )
    lines.extend(
        [
            "    local.get 1",
            f"    i32.const {tweak}",
            "    i32.add",
        ]
    )
    return lines


def build_wat(num_funcs: int) -> str:
    if num_funcs <= 0:
        raise ValueError("num_funcs must be > 0")

    lines = [
        "(module",
        "  (type (func (param i32) (result i32)))",
    ]
    for i in range(num_funcs):
        lines.append(f"  (func (type 0) (param i32) (result i32)")
        lines.extend(function_body(i))
        lines.append("  )")
    for i in range(num_funcs):
        lines.append(f'  (export "f{i}" (func {i}))')
    lines.append(")")
    return "\n".join(lines) + "\n"


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--counts", default="64,32,16,8")
    parser.add_argument("-o", "--out-dir", default="../testdata/synthetic")
    args = parser.parse_args()

    out_dir = Path(args.out_dir).resolve()
    out_dir.mkdir(parents=True, exist_ok=True)

    for n in [int(x.strip()) for x in args.counts.split(",") if x.strip()]:
        wat = build_wat(n)
        path = out_dir / f"funcs-{n}.wat"
        path.write_text(wat, encoding="ascii")
        print(f"wrote {path} ({len(wat)} bytes text, {n} functions)")


if __name__ == "__main__":
    main()
