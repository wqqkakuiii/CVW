#!/bin/bash
set -euo pipefail
export GOROOT="/usr/local/go"
export PATH="$GOROOT/bin:$PATH"
echo "Building funcs-1024-go.wasm with $(go version)"
GOWORK=off GOOS=wasip1 GOARCH=wasm go build -o "funcs-1024-go.wasm"
