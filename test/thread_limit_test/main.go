// thread_limit_test.go
// 测试操作系统线程数上限的工具
// 用法: go run thread_limit_test.go
package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

func getThreadCount() int {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "Threads:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				n, _ := strconv.Atoi(parts[1])
				return n
			}
		}
	}
	return -1
}

func main() {
	// 创建日志文件（带时间戳）
	logFileName := fmt.Sprintf("thread_limit_%s.log", time.Now().Format("20060102_150405"))
	logFile, err := os.Create(logFileName)
	if err != nil {
		fmt.Printf("无法创建日志文件: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()

	// 同时输出到终端和日志文件
	mw := io.MultiWriter(os.Stdout, logFile)
	log.SetOutput(mw)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	log.Println("=== 线程上限测试工具 ===")
	log.Printf("Go 版本: %s\n", runtime.Version())
	log.Printf("GOMAXPROCS: %d\n", runtime.GOMAXPROCS(0))

	// 显示当前 ulimit
	cmd := exec.Command("sh", "-c", "ulimit -u")
	out, _ := cmd.Output()
	log.Printf("当前 ulimit -u (软限制): %s", string(out))

	cmd = exec.Command("sh", "-c", "ulimit -Hu")
	out, _ = cmd.Output()
	log.Printf("当前 ulimit -Hu (硬限制): %s", string(out))

	log.Println("\n开始创建 OS 线程（每个 goroutine 锁定一个 OS 线程）...")
	log.Println("按 Ctrl+C 停止测试\n")

	var count int32
	start := time.Now()

	// 记录最终上限（程序崩溃或手动停止时）
	defer func() {
		finalThreads := getThreadCount()
		log.Printf("\n=== 测试结束 ===")
		log.Printf("最终创建的 goroutine 数: %d", count)
		log.Printf("最终线程数: %d", finalThreads)
		log.Printf("本次测试的线程上限 ≈ %d", finalThreads)
		log.Printf("日志已保存到: %s", logFileName)
	}()

	for {
		go func() {
			runtime.LockOSThread()
			atomic.AddInt32(&count, 1)
			// 保持线程存活
			select {}
		}()

		if count%100 == 0 {
			threads := getThreadCount()
			log.Printf("已创建 goroutine: %d, 当前线程数: %d, 耗时: %v",
				count, threads, time.Since(start).Round(time.Second))
		}

		// 进一步放慢创建速度，避免短时间内打满系统线程限制
		// 每创建 10 个 goroutine 就 sleep 100ms（约 100 线程/秒）
		if count%10 == 0 {
			time.Sleep(100 * time.Millisecond)
		}
	}
}
