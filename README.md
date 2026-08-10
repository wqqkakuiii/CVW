# CVW

## 插桩 → 压测

### 1. 插桩合约源码

```bash
cd /home/projects/CVW
# 单包
go run ./instrumenter \
  -input ./chainmaker/testdata/compute \
  -output ./chainmaker/testdata-instrument/compute \
  -gas-zero-blacklist ./instrumenter/gas_zero_blacklist.example.txt

# 或批量（透传黑名单）
go run ./instrumenter-go \
  -input <源码根> \
  -out-dir <输出根> \
  -gas-zero-blacklist ./instrumenter/gas_zero_blacklist.example.txt
```

- 源码：`chainmaker/testdata/<合约>`
- 插桩产物：`chainmaker/testdata-instrument/<合约>`（含 `ConsumeGas` / `SetGas` / `GetGas`）
- 黑名单按 `package.FuncName`；`main.InitContract` 已在 example 中

### 2. 编译 wasm

```bash
cd /home/projects/CVW/chainmaker/testdata-instrument/<合约>
./build.sh <合约名> go
# 产物: <合约名>-go.wasm（Go 1.24.1 wasip1）
```

### 3a. 本地 vmwasmer（单测）

```bash
# 编译并拷到 VM testdata
python3 chainmaker/testdata-instrument/copy_wasm_to_vm_testdata_instrument.py

cd chainmaker/cmd/run_testinvoke
go run . -name compute -method normalCal -type go -times 1000
# bigInput: chainmaker/cmd/run_testbiginput
```

### 3b. 集群压测

```bash
# 拷贝 wasm
cp chainmaker/testdata-instrument/<合约>/<合约>-go.wasm \
  /home/projects/chainmaker-test-toolkit-master/chainmaker-performance-test/contract/claim_demo/

# 改压测配置
# chainmaker-performance-test/config/const_config.yml
#   contract_name / file_name / contract_method / contract_type: go / runtime_type: WASMER

cd /home/projects/chainmaker-test-toolkit-master/chainmaker-performance-test/example
go run test.go
```

压测读 `claim_demo/<file_name|-contract_name>-go.wasm`；日志与出块报告在 `log/experimentlog/`。
