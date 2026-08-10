#!/bin/bash

contractName=$1
buildOption=$2
targetARCH=$3
crypto=""

if [ "$(uname)" == "Linux" ]; then
  crypto="-tags crypto"
fi

if [[ ! -n $contractName ]]; then
  echo "contractName is empty. use as: ./build.sh contractName."
  exit 1
fi

if [[ -z $buildOption ]]; then
  buildOption="tinygo"
fi

if [[ $buildOption == "tinygo" ]]; then
  export GOROOT="/usr/local/go"
  export PATH="$GOROOT/bin:$PATH"
  echo "Using TinyGo to compile..."
  tinygo build -no-debug -opt=s -o "$contractName-tinygo.wasm" -target=wasip1
else
  export GOROOT="/usr/local/go-version/go1.24.1"
  export PATH="$GOROOT/bin:$PATH"
  echo "Using Go to compile..."
  GOOS=wasip1 GOARCH=wasm go build -o "$contractName-go.wasm"
fi
