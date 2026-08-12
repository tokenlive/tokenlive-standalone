#!/usr/bin/env python3
import argparse
import pathlib
import re


def replace_one(text: str, pattern: str, replacement: str, label: str) -> str:
    updated, count = re.subn(pattern, replacement, text, count=1)
    if count != 1:
        raise SystemExit(f"failed to update {label}: expected 1 match, got {count}")
    return updated


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--formula", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--arm64-url", required=True)
    parser.add_argument("--arm64-sha256", required=True)
    parser.add_argument("--amd64-url", required=True)
    parser.add_argument("--amd64-sha256", required=True)
    args = parser.parse_args()

    path = pathlib.Path(args.formula)
    text = path.read_text()
    text = replace_one(
        text,
        r'version\s+"[^"]+"',
        f'version "{args.version}"',
        "version",
    )
    text = replace_one(
        text,
        r'(if\s+Hardware::CPU\.intel\?\s*\n\s*url\s+)"[^"]+"(\s*\n\s*sha256\s+)"[^"]+"',
        rf'\1"{args.amd64_url}"\2"{args.amd64_sha256}"',
        "amd64 release",
    )
    text = replace_one(
        text,
        r'(else\s*\n\s*url\s+)"[^"]+"(\s*\n\s*sha256\s+)"[^"]+"',
        rf'\1"{args.arm64_url}"\2"{args.arm64_sha256}"',
        "arm64 release",
    )
    path.write_text(text)


if __name__ == "__main__":
    main()
