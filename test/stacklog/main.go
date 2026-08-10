package main

import (
	"runtime"
	"syscall"
)

func main() {
	write([]byte("从 main 进入调用链，在深层用 runtime.Stack 打印当前 goroutine 栈\n\n"))
	level1()
}

func level1() {
	level2()
}

func level2() {
	level3()
}

func level3() {
	printStack([]byte("level3 内"))
}

func printStack(where []byte) {
	write([]byte("---------- "))
	write(where)
	write([]byte(" ----------\n"))
	buf := make([]byte, 1024)
	for {
		n := runtime.Stack(buf, false)
		if n < len(buf) {
			write(buf[:n])
			break
		}
		buf = make([]byte, len(buf)*2)
	}
	write([]byte("\n---------- 结束 ----------\n"))
}

func write(p []byte) {
	for len(p) > 0 {
		n, err := syscall.Write(syscall.Stdout, p)
		if err != nil || n == 0 {
			return
		}
		p = p[n:]
	}
}
