# bigInput 合约

读取大参数并返回总字节长度。

| 方法 | 说明 |
|------|------|
| `inputSize` | 单参数 `data`（受 EasyCodec 单值 1MB 限制） |
| `inputSizeMulti` | 分片参数 `part_count` + `data0`..`data{N-1}`（每片 ≤1MB） |
| `sleep5` | 仅 sleep 5 秒 |
| `ioHeavy` | `count` 次 PutState+GetState；可选 `count`、`payload_size` |

## 编译

```sh
cd /home/projects/CVW/chainmaker/testdata/bigInput
./build.sh bigInput go
# 产物: bigInput-go.wasm
```

## cmc 示例（单参数）

```sh
./cmc client contract user invoke \
  --contract-name=bigInput \
  --method=inputSize \
  --sdk-conf-path=./sdk_config.yml \
  --params="{\"data\":\"hello\"}" \
  --sync-result=true \
  --result-to-string=true
```

## cmc 示例（分片，总约 2MB）

```sh
# part_count=2，data0/data1 各 1MB（示例用 Python 生成）
python3 - <<'PY'
import json, subprocess
mb = 1024 * 1024
params = {"part_count": "2", "data0": "A"*mb, "data1": "B"*mb}
subprocess.run([
  "./cmc", "client", "contract", "user", "invoke",
  "--contract-name=bigInput", "--method=inputSizeMulti",
  "--sdk-conf-path=./sdk_config.yml",
  f"--params={json.dumps(params)}",
  "--sync-result=true", "--result-to-string=true",
], check=False)
PY
```

## vmwasmer 压测

```sh
cd /home/projects/CVW/chainmaker/cmd/run_testbiginput
go run . -build -multi          # 默认阶梯至 64MB
go run . -multi -size=67108864  # 单点 64MB 总 data
```
