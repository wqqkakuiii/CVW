#!/usr/bin/env python3
import argparse
import base64
import csv
import glob
import json
import os
import subprocess
import sys
import time
from datetime import datetime


def b64_decode_best_effort(value: str) -> str:
    try:
        return base64.b64decode(value).decode("utf-8")
    except Exception:
        return value


def run_cmd_json(cmd, workdir: str):
    proc = subprocess.run(cmd, capture_output=True, text=True, cwd=workdir)
    raw = ((proc.stdout or "") + (proc.stderr or "")).strip()
    try:
        data = json.loads(raw) if raw else {}
    except Exception:
        return {
            "ok": False,
            "error": f"JSON parse failed: {raw}",
            "data": {},
            "raw": raw,
        }
    top_code = data.get("code", 0)
    cr = data.get("contract_result", {}) if isinstance(data.get("contract_result", {}), dict) else {}
    cr_code = cr.get("code", 0)
    message = data.get("message", "") or cr.get("message", "")
    ok = top_code in (0, None) and cr_code in (0, None)
    return {
        "ok": ok,
        "error": "" if ok else message,
        "data": data,
        "raw": raw,
    }


def run_cmc(cmc_bin: str, sdk_conf: str, contract: str, method: str, mode: str, params: dict, workdir: str):
    cmd = [
        cmc_bin,
        "client",
        "contract",
        "user",
        mode,
        f"--contract-name={contract}",
        f"--method={method}",
        f"--sdk-conf-path={sdk_conf}",
        f"--params={json.dumps(params, ensure_ascii=False)}",
    ]
    if mode == "invoke":
        cmd.append("--sync-result=true")

    proc = subprocess.run(cmd, capture_output=True, text=True, cwd=workdir)
    raw = (proc.stdout or "") + (proc.stderr or "")
    raw = raw.strip()
    try:
        data = json.loads(raw)
    except Exception:
        return {
            "ok": False,
            "tx_id": "",
            "gas_used": None,
            "result_decoded": "",
            "error": f"JSON parse failed: {raw}",
        }

    top_code = data.get("code", 0)
    cr = data.get("contract_result", {}) if isinstance(data.get("contract_result", {}), dict) else {}
    cr_code = cr.get("code", 0)
    tx_id = data.get("tx_id", "")
    gas_used = cr.get("gas_used", None)
    result_raw = cr.get("result", "")
    if isinstance(result_raw, (dict, list)):
        result_text = json.dumps(result_raw, ensure_ascii=False)
    else:
        result_text = str(result_raw)
    message = data.get("message", "") or cr.get("message", "")

    ok = True
    if top_code not in (0, None):
        ok = False
    if cr_code not in (0, None):
        ok = False
    if gas_used is None:
        ok = False

    return {
        "ok": ok,
        "tx_id": tx_id,
        "gas_used": gas_used,
        "result_decoded": b64_decode_best_effort(result_text),
        "error": "" if ok else message,
    }


def find_local_wasm(contracts_root: str, contract: str):
    contract_dir = os.path.join(contracts_root, contract)
    if not os.path.isdir(contract_dir):
        return "", f"contract directory not found: {contract_dir}"

    preferred = [
        os.path.join(contract_dir, f"{contract}-go.wasm"),
        os.path.join(contract_dir, f"{contract}-tinygo.wasm"),
    ]
    for p in preferred:
        if os.path.isfile(p):
            return p, ""

    candidates = sorted(glob.glob(os.path.join(contract_dir, "*.wasm")))
    if not candidates:
        return "", f"no wasm found in: {contract_dir}"
    return candidates[0], ""


def _contains_any(text: str, keywords):
    lower_text = (text or "").lower()
    return any(k.lower() in lower_text for k in keywords)


def _bump_contract_version(base_version: str):
    # Use timestamp suffix to avoid "contract version exist" on upgrade.
    ts = int(time.time())
    parts = [p for p in str(base_version).split(".") if p != ""]
    if not parts:
        return f"1.0.{ts}"
    if len(parts) == 1:
        return f"{parts[0]}.0.{ts}"
    if len(parts) == 2:
        return f"{parts[0]}.{parts[1]}.{ts}"
    return ".".join(parts[:-1] + [str(ts)])


def deploy_contract_create_or_upgrade(
    cmc_bin: str,
    workdir: str,
    sdk_conf: str,
    contract: str,
    wasm_path: str,
    runtime_type: str,
    contract_version: str,
    admin_key_file_paths: str,
    admin_crt_file_paths: str,
):
    common = [
        cmc_bin,
        "client",
        "contract",
        "user",
        "",
        f"--contract-name={contract}",
        f"--runtime-type={runtime_type}",
        f"--byte-code-path={wasm_path}",
        f"--version={contract_version}",
        f"--sdk-conf-path={sdk_conf}",
        f"--admin-key-file-paths={admin_key_file_paths}",
        f"--admin-crt-file-paths={admin_crt_file_paths}",
        "--sync-result=true",
        "--params={}",
    ]

    create_cmd = common.copy()
    create_cmd[4] = "create"
    create_ret = run_cmd_json(create_cmd, workdir)
    if create_ret["ok"]:
        return True, "created", f"version={contract_version}"

    upgrade_cmd = common.copy()
    upgrade_cmd[4] = "upgrade"
    upgrade_ret = run_cmd_json(upgrade_cmd, workdir)
    if upgrade_ret["ok"]:
        note = create_ret["error"] or "create failed, then upgrade succeeded"
        return True, "upgraded", f"{note}, version={contract_version}"

    create_err = create_ret["error"] or create_ret["raw"]
    upgrade_err = upgrade_ret["error"] or upgrade_ret["raw"]

    # create reports "contract exist" and upgrade reports "contract version exist":
    # bump version and retry upgrade once.
    if _contains_any(create_err, ["contract exist"]) and _contains_any(upgrade_err, ["contract version exist"]):
        bumped_version = _bump_contract_version(contract_version)
        bumped_upgrade_cmd = common.copy()
        bumped_upgrade_cmd[4] = "upgrade"
        bumped_upgrade_cmd[8] = f"--version={bumped_version}"
        bumped_upgrade_ret = run_cmd_json(bumped_upgrade_cmd, workdir)
        if bumped_upgrade_ret["ok"]:
            return (
                True,
                "upgraded",
                f"create failed(contract exist), upgrade retried with version={bumped_version}",
            )
        bumped_err = bumped_upgrade_ret["error"] or bumped_upgrade_ret["raw"]
        err = (
            f"create failed: {create_err}; "
            f"upgrade failed: {upgrade_err}; "
            f"upgrade with bumped version failed: {bumped_err}"
        )
        return False, "failed", err

    err = (
        f"create failed: {create_err}; "
        f"upgrade failed: {upgrade_err}"
    )
    return False, "failed", err


def build_cases(i: int, addr_a: str, addr_b: str, base_token_mint: int, base_token_buynow: int, base_ts: int, run_tag: str):
    token_mint = base_token_mint + i
    token_buy = base_token_buynow + i
    phone = str(13800000000 + i)
    ts = str(base_ts + i)

    return [
        ("compute", "bigNumCal", "invoke", {}),
        ("compute", "hashCal", "invoke", {}),
        ("compute", "normalCal", "invoke", {}),
        ("erc721", "mint", "invoke", {"to": addr_a, "tokenId": str(token_mint), "metadata": f"bench-{i}"}),
        ("erc721", "setApprovalForAll2", "invoke", {"approvalFrom": addr_a}),
        ("erc721", "transferFrom", "invoke", {"from": addr_a, "to": addr_b, "tokenId": str(token_mint)}),
        ("exchange", "buyNow", "invoke", {"tokenId": str(token_buy), "from": addr_a, "to": addr_b, "metadata": f"trade-{i}"}),
        ("fact", "saveAndFindByFileHash", "invoke", {"file_hash": f"hash_{run_tag}_{i}", "file_name": f"file_{i}", "time": ts}),
        ("identity", "addWriteList", "invoke", {"address": f"{addr_a},{addr_b}"}),
        ("identity", "isApprovedUser", "get", {"address": addr_a}),
        ("raffle", "registRaffle", "invoke", {
            "peoples": json.dumps({"peoples": [{"num": 1, "name": "alice"}, {"num": 2, "name": "bob"}]}, ensure_ascii=False),
            "level": "1",
            "timestamp": ts,
        }),
        ("itinerary", "saveAndQueryHistory", "invoke", {
            "phone": phone,
            "itinerary": json.dumps({"ip": "1.1.1.1", "country": "CN", "city": "Shenzhen", "region": "Guangdong"}, ensure_ascii=False),
        }),
    ]


def main():
    parser = argparse.ArgumentParser(description="Run 12 contract functions and record gas min/max per function.")
    parser.add_argument("-n", "--repeat", type=int, default=1000, help="Repeat count for each function (default: 1000)")
    parser.add_argument("--cmc-bin", default="/home/projects/chainmaker-go/tools/cmc/cmc", help="Path to cmc binary")
    parser.add_argument("--cmc-workdir", default="", help="Working directory for cmc (default: dirname of --cmc-bin)")
    parser.add_argument("--sdk-conf", default="/home/projects/chainmaker-go/tools/cmc/testdata/sdk_config.yml", help="Path to sdk_config.yml")
    parser.add_argument(
        "--deploy-local-contracts",
        action="store_true",
        help="Deploy local wasm contracts before benchmark (create, fallback to upgrade)",
    )
    parser.add_argument(
        "--contracts-root",
        default=os.path.dirname(os.path.abspath(__file__)),
        help="Contracts root directory; each contract should be under <root>/<contract>/*.wasm",
    )
    parser.add_argument("--runtime-type", default="WASMER", help="Runtime type for contract create/upgrade (default: WASMER)")
    parser.add_argument("--contract-version", default="1.0", help="Contract version used for create/upgrade (default: 1.0)")
    parser.add_argument(
        "--admin-key-file-paths",
        default="./testdata/crypto-config/wx-org1.chainmaker.org/user/admin1/admin1.sign.key,"
                "./testdata/crypto-config/wx-org2.chainmaker.org/user/admin1/admin1.sign.key,"
                "./testdata/crypto-config/wx-org3.chainmaker.org/user/admin1/admin1.sign.key",
        help="Comma-separated admin sign key paths (override if your environment differs)",
    )
    parser.add_argument(
        "--admin-crt-file-paths",
        default="./testdata/crypto-config/wx-org1.chainmaker.org/user/admin1/admin1.sign.crt,"
                "./testdata/crypto-config/wx-org2.chainmaker.org/user/admin1/admin1.sign.crt,"
                "./testdata/crypto-config/wx-org3.chainmaker.org/user/admin1/admin1.sign.crt",
        help="Comma-separated admin sign cert paths (override if your environment differs)",
    )
    parser.add_argument("--addr-a", default="0x1111111111111111111111111111111111111111", help="Address A used in tests")
    parser.add_argument("--addr-b", default="0x2222222222222222222222222222222222222222", help="Address B used in tests")
    parser.add_argument("--base-token-mint", type=int, default=1000000, help="Base token id for erc721 mint/transfer")
    parser.add_argument("--base-token-buynow", type=int, default=2000000, help="Base token id for exchange buyNow")
    parser.add_argument("--base-ts", type=int, default=1712500000, help="Base timestamp")
    parser.add_argument("--log-dir", default="", help="Output log directory")
    parser.add_argument(
        "--outlier-threshold",
        type=float,
        default=0.20,
        help="Mark as fail if (maxGas - gasUsed) / maxGas > threshold (default: 0.20)",
    )
    parser.add_argument(
        "--only-function",
        default="",
        help="Only test one function, format: contract.method, e.g. compute.hashCal",
    )
    args = parser.parse_args()

    if args.repeat <= 0:
        print("repeat must be > 0")
        sys.exit(1)
    if args.outlier_threshold < 0 or args.outlier_threshold >= 1:
        print("outlier-threshold must be in [0, 1)")
        sys.exit(1)
    if not os.path.isfile(args.sdk_conf):
        print(f"sdk config not found: {args.sdk_conf}")
        sys.exit(1)
    if not os.path.isfile(args.cmc_bin):
        print(f"cmc not found: {args.cmc_bin}")
        sys.exit(1)
    cmc_workdir = args.cmc_workdir or os.path.dirname(os.path.abspath(args.cmc_bin))
    if not os.path.isdir(cmc_workdir):
        print(f"cmc workdir not found: {cmc_workdir}")
        sys.exit(1)
    if args.deploy_local_contracts:
        if not os.path.isdir(args.contracts_root):
            print(f"contracts root not found: {args.contracts_root}")
            sys.exit(1)
        if not args.admin_key_file_paths.strip():
            print("admin-key-file-paths is required when --deploy-local-contracts is set")
            sys.exit(1)
        if not args.admin_crt_file_paths.strip():
            print("admin-crt-file-paths is required when --deploy-local-contracts is set")
            sys.exit(1)

    run_ts = datetime.now().strftime("%Y%m%d_%H%M%S")
    log_dir = args.log_dir or os.path.join(os.getcwd(), f"benchmark_logs_{run_ts}")
    os.makedirs(log_dir, exist_ok=True)
    details_csv = os.path.join(log_dir, "details.csv")
    summary_txt = os.path.join(log_dir, "summary.txt")

    gas_min = {}
    gas_max = {}
    ok_cnt = {}
    fail_cnt = {}
    records = []

    start = time.time()
    # 先确定函数顺序，然后按“单函数连续执行 repeat 次”的方式跑。
    ordered_cases = build_cases(
        i=1,
        addr_a=args.addr_a,
        addr_b=args.addr_b,
        base_token_mint=args.base_token_mint,
        base_token_buynow=args.base_token_buynow,
        base_ts=args.base_ts,
        run_tag=run_ts,
    )
    if args.only_function:
        target = args.only_function.strip()
        filtered = []
        for c, m, mode, p in ordered_cases:
            if f"{c}.{m}" == target:
                filtered.append((c, m, mode, p))
        if not filtered:
            available = ", ".join([f"{c}.{m}" for c, m, _, _ in ordered_cases])
            print(f"only-function not found: {target}")
            print(f"available: {available}")
            sys.exit(1)
        ordered_cases = filtered

    if args.deploy_local_contracts:
        unique_contracts = []
        for c, _, _, _ in ordered_cases:
            if c not in unique_contracts:
                unique_contracts.append(c)
        print(f"Deploying local contracts from: {args.contracts_root}")
        for c in unique_contracts:
            wasm_path, err = find_local_wasm(args.contracts_root, c)
            if err:
                print(f"[deploy] {c}: {err}")
                sys.exit(1)
            ok, action, msg = deploy_contract_create_or_upgrade(
                cmc_bin=args.cmc_bin,
                workdir=cmc_workdir,
                sdk_conf=args.sdk_conf,
                contract=c,
                wasm_path=wasm_path,
                runtime_type=args.runtime_type,
                contract_version=args.contract_version,
                admin_key_file_paths=args.admin_key_file_paths,
                admin_crt_file_paths=args.admin_crt_file_paths,
            )
            if not ok:
                print(f"[deploy] {c}: failed, wasm={wasm_path}")
                print(f"[deploy] error: {msg}")
                sys.exit(1)
            extra = f", note={msg}" if msg else ""
            print(f"[deploy] {c}: {action}, wasm={wasm_path}{extra}")

    total_case_count = len(ordered_cases)
    for case_idx, (contract, method, mode, _) in enumerate(ordered_cases, start=1):
        print(f"Running function {case_idx}/{total_case_count}: {contract}.{method}, repeat={args.repeat}")
        for i in range(1, args.repeat + 1):
            round_cases = build_cases(
                i=i,
                addr_a=args.addr_a,
                addr_b=args.addr_b,
                base_token_mint=args.base_token_mint,
                base_token_buynow=args.base_token_buynow,
                base_ts=args.base_ts,
                run_tag=run_ts,
            )
            _, _, _, params = round_cases[case_idx - 1]
            ret = run_cmc(args.cmc_bin, args.sdk_conf, contract, method, mode, params, cmc_workdir)
            records.append(
                {
                    "contract": contract,
                    "method": method,
                    "round": i,
                    "tx_id": ret["tx_id"],
                    "gas_used": ret["gas_used"],
                    "result_decoded": (ret["result_decoded"] or "").replace("\n", "\\n"),
                    "error": (ret["error"] or "").replace("\n", "\\n"),
                    "call_ok": bool(ret["ok"]),
                }
            )
            if i % 20 == 0:
                print(f"Progress [{contract}.{method}]: {i}/{args.repeat}")

    elapsed = time.time() - start

    # First pass: find max gas per function from successful calls.
    max_gas_per_metric = {}
    for rec in records:
        if not rec["call_ok"] or not isinstance(rec["gas_used"], int):
            continue
        metric_key = f"{rec['contract']}.{rec['method']}"
        prev = max_gas_per_metric.get(metric_key)
        if prev is None or rec["gas_used"] > prev:
            max_gas_per_metric[metric_key] = rec["gas_used"]

    # Second pass: apply 20% rule and accumulate stats.
    with open(details_csv, "w", newline="", encoding="utf-8") as f:
        writer = csv.writer(f)
        writer.writerow(
            [
                "contract",
                "method",
                "round",
                "ok",
                "tx_id",
                "gas_used",
                "result_decoded",
                "error",
                "is_outlier",
                "deviation_ratio",
                "max_gas_used_ref",
            ]
        )

        for rec in records:
            metric_key = f"{rec['contract']}.{rec['method']}"
            is_outlier = 0
            deviation_ratio = ""
            final_ok = 0

            if rec["call_ok"] and isinstance(rec["gas_used"], int):
                max_ref = max_gas_per_metric.get(metric_key)
                if max_ref and max_ref > 0:
                    deviation = (max_ref - rec["gas_used"]) / max_ref
                    deviation_ratio = f"{deviation:.6f}"
                    if deviation > args.outlier_threshold:
                        is_outlier = 1
                        rec["error"] = (
                            rec["error"] + "; " if rec["error"] else ""
                        ) + f"gas outlier: deviation={deviation:.4f} > threshold={args.outlier_threshold:.4f}"
                    else:
                        final_ok = 1
                else:
                    final_ok = 1
            else:
                max_ref = max_gas_per_metric.get(metric_key, "")

            if final_ok:
                ok_cnt[metric_key] = ok_cnt.get(metric_key, 0) + 1
                gas_used = rec["gas_used"]
                if metric_key not in gas_min or gas_used < gas_min[metric_key]:
                    gas_min[metric_key] = gas_used
                if metric_key not in gas_max or gas_used > gas_max[metric_key]:
                    gas_max[metric_key] = gas_used
            else:
                fail_cnt[metric_key] = fail_cnt.get(metric_key, 0) + 1

            writer.writerow(
                [
                    rec["contract"],
                    rec["method"],
                    rec["round"],
                    final_ok,
                    rec["tx_id"],
                    rec["gas_used"],
                    rec["result_decoded"],
                    rec["error"],
                    is_outlier,
                    deviation_ratio,
                    max_ref,
                ]
            )

    method_keys = []
    for c, m, _, _ in ordered_cases:
        method_keys.append(f"{c}.{m}")
    lines = []
    lines.append("Benchmark summary")
    lines.append(f"repeat={args.repeat}")
    lines.append(f"outlier_threshold={args.outlier_threshold:.4f}")
    lines.append(f"elapsed_sec={elapsed:.2f}")
    lines.append(f"log_dir={log_dir}")
    lines.append("")
    lines.append("Per-contract-per-function gasUsed (min/max) and pass/fail:")
    for c in method_keys:
        min_v = gas_min.get(c, None)
        max_v = gas_max.get(c, None)
        spread_ratio = "N/A"
        if isinstance(min_v, int) and isinstance(max_v, int) and max_v > 0:
            spread_ratio = f"{(max_v - min_v) / max_v:.6f}"
        lines.append(
            f"{c}: gas_min={gas_min.get(c, 'N/A')}, gas_max={gas_max.get(c, 'N/A')}, "
            f"spread_ratio={spread_ratio}, ok={ok_cnt.get(c, 0)}, fail={fail_cnt.get(c, 0)}"
        )

    with open(summary_txt, "w", encoding="utf-8") as f:
        f.write("\n".join(lines) + "\n")

    print("\n".join(lines))
    print("")
    print(f"Details CSV: {details_csv}")
    print(f"Summary: {summary_txt}")


if __name__ == "__main__":
    main()
