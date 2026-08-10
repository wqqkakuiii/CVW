#!/usr/bin/env python3
import argparse
import shutil
import subprocess
import sys
from pathlib import Path


CONTRACT_DIRS = [
    "compute",
    "erc721",
    "exchange",
    "fact",
    "identity",
    "itinerary",
    "raffle",
    "bigInput",
]

DEFAULT_TARGET = "/home/projects/CVW/chainmaker/chainmaker-vm-wasmer/vm-wasmer/v2@v2.4.0/testdata"


def collect_wasm_files(base_dir: Path):
    files = []
    missing_dirs = []
    for contract in CONTRACT_DIRS:
        contract_dir = base_dir / contract
        if not contract_dir.is_dir():
            missing_dirs.append(str(contract_dir))
            continue
        files.extend(sorted(contract_dir.glob("*.wasm")))
    return files, missing_dirs


def build_contract_wasm(base_dir: Path, contract: str):
    contract_dir = base_dir / contract
    if not contract_dir.is_dir():
        return False, f"contract directory not found: {contract_dir}"

    build_script = contract_dir / "build.sh"
    if not build_script.is_file():
        return False, f"build script not found: {build_script}"

    cmd = ["bash", "build.sh", contract, "go"]
    print(f"building {contract}: {' '.join(cmd)} (cwd={contract_dir})")
    proc = subprocess.run(cmd, cwd=str(contract_dir), text=True)
    if proc.returncode != 0:
        return False, f"build failed for {contract}, exit_code={proc.returncode}"
    return True, ""


def main():
    parser = argparse.ArgumentParser(
        description="Copy wasm files from testdata-instrument contract folders to vm-wasmer testdata-instrument."
    )
    parser.add_argument(
        "--target-dir",
        default=DEFAULT_TARGET,
        help=f"Destination directory (default: {DEFAULT_TARGET})",
    )
    parser.add_argument(
        "--no-overwrite",
        action="store_true",
        help="Skip files that already exist in destination.",
    )
    args = parser.parse_args()

    base_dir = Path(__file__).resolve().parent
    target_dir = Path(args.target_dir).resolve()
    target_dir.mkdir(parents=True, exist_ok=True)

    # 先编译 7 个合约目录中的 wasm（bash build.sh <contract> go）
    for contract in CONTRACT_DIRS:
        ok, err = build_contract_wasm(base_dir, contract)
        if not ok:
            print(err)
            return 1

    wasm_files, missing_dirs = collect_wasm_files(base_dir)
    if missing_dirs:
        print("Warning: some contract directories are missing:")
        for d in missing_dirs:
            print(f"  - {d}")

    if not wasm_files:
        print("No wasm files found, nothing to copy.")
        return 1

    copied = 0
    skipped = 0
    for src in wasm_files:
        dst = target_dir / src.name
        if args.no_overwrite and dst.exists():
            skipped += 1
            print(f"skip   {src} -> {dst} (exists)")
            continue
        shutil.copy2(src, dst)
        copied += 1
        print(f"copied {src} -> {dst}")

    print("")
    print(f"Done. copied={copied}, skipped={skipped}, total_found={len(wasm_files)}")
    print(f"target_dir={target_dir}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
