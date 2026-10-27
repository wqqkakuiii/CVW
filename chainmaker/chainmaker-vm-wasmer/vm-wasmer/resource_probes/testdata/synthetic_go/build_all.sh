#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
export GOROOT="/usr/local/go"
export PATH="$GOROOT/bin:$PATH"

for n in 1024 64 32 16 8; do
  dir="$ROOT/funcs-$n"
  echo "=== building funcs-$n (${n} wasmexport entries) ==="
  (
    cd "$dir"
    go mod tidy
    GOWORK=off ./build.sh
  )
  cp "$dir/funcs-$n-go.wasm" "$ROOT/funcs-$n-go.wasm"
  echo "  -> $ROOT/funcs-$n-go.wasm ($(stat -c%s "$ROOT/funcs-$n-go.wasm") bytes)"
done
