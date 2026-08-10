# 线程上限测试工具

用于测试当前操作系统环境（尤其是 WSL2）能创建的最大 OS 线程数。

## 编译与运行

```bash
cd /home/projects/CVW/test/thread_limit_test
go run .
```

或者编译后运行：

```bash
go build -o thread_limit_test .
./thread_limit_test
```

## 日志输出

程序会自动创建一个带时间戳的日志文件，例如：

```
thread_limit_20260602_190405.log
```

**所有输出会同时显示在终端和日志文件中**，方便事后分析。

日志文件会记录：
- 测试开始时的环境信息（Go 版本、ulimit 限制等）
- 每 100 个 goroutine 的线程数变化
- 测试结束时的最终上限（通过 defer 记录）

## 输出示例

```
=== 线程上限测试工具 ===
Go 版本: go1.24.0
GOMAXPROCS: 16
当前 ulimit -u (软限制): 16384
当前 ulimit -Hu (硬限制): 16384

开始创建 OS 线程（每个 goroutine 锁定一个 OS 线程）...
按 Ctrl+C 停止测试

已创建 goroutine: 100, 当前线程数: 123, 耗时: 1s
已创建 goroutine: 200, 当前线程数: 223, 耗时: 2s
...
```

## 注意事项

- 每个 goroutine 会调用 `runtime.LockOSThread()`，确保绑定到独立的 OS 线程。
- 测试会持续创建线程，直到触发 `EAGAIN`（资源暂时不可用）或手动停止。
- 建议在测试前先用 `ulimit -u` 查看当前限制。
- 按 `Ctrl+C` 可安全中断测试。
- 日志文件会在程序结束（包括 panic）时自动记录最终的上限值。

## 典型 WSL2 限制

- 默认 `ulimit -u` 通常是 16384
- 实际可创建的线程数可能更低（取决于 Windows 版本和 WSL2 配置）
- 如果需要更高限制，请修改 `~/.wslconfig` 或 `/etc/security/limits.conf`
