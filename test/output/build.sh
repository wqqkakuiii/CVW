#!/bin/bash

# 使用 Go 自带的编译器将 formatTest.go 编译成 formatTest-go.wasm

echo "使用 Go 编译器编译 formatTest.go 为 WebAssembly..."

# 使用 Go 自带的编译器编译为 WASM
GOOS=wasip1 GOARCH=wasm go build -o formatTest-go.wasm formatTest.go

if [ $? -eq 0 ]; then
    echo "编译成功！输出文件: formatTest-go.wasm"
else
    echo "编译失败！"
    read -p "按 Enter 键退出..."
    exit 1
fi
