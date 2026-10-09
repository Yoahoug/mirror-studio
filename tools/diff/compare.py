"""Compares two replay output directories and reports the first differences.

Exit code 0 means the traces and snapshots are identical. Development asset.
"""

from __future__ import annotations

import argparse
import difflib
import json
import sys
from pathlib import Path


def load(path: Path):
    return json.loads(path.read_text(encoding="utf-8"))


def compare_traces(left: list, right: list, limit: int) -> list[str]:
    problems: list[str] = []
    if len(left) != len(right):
        problems.append(f"step count differs: reference={len(left)} candidate={len(right)}")
    for index, (a, b) in enumerate(zip(left, right)):
        if a == b:
            continue
        label = a.get("label") or b.get("label") or "?"
        request = a.get("request") or b.get("request")
        problems.append(f"[{label}] step {index} {request} differs")
        left_text = json.dumps(a, ensure_ascii=False, indent=1, sort_keys=True).splitlines()
        right_text = json.dumps(b, ensure_ascii=False, indent=1, sort_keys=True).splitlines()
        diff = list(difflib.unified_diff(left_text, right_text, "reference", "candidate", lineterm="", n=2))
        problems.extend(diff[:80])
        if len(problems) > limit:
            problems.append("... truncated")
            break
    return problems


def compare_snapshots(left: dict, right: dict, limit: int) -> list[str]:
    problems: list[str] = []
    for table in sorted(set(left) | set(right)):
        a = left.get(table)
        b = right.get(table)
        if a == b:
            continue
        problems.append(f"table {table} differs")
        left_text = json.dumps(a, ensure_ascii=False, indent=1, sort_keys=True).splitlines()
        right_text = json.dumps(b, ensure_ascii=False, indent=1, sort_keys=True).splitlines()
        diff = list(difflib.unified_diff(left_text, right_text, "reference", "candidate", lineterm="", n=2))
        problems.extend(diff[:120])
        if len(problems) > limit:
            problems.append("... truncated")
            break
    return problems


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("reference")
    parser.add_argument("candidate")
    parser.add_argument("--limit", type=int, default=400)
    parser.add_argument("--only", choices=("all", "trace", "snapshot"), default="all")
    args = parser.parse_args()

    reference = Path(args.reference)
    candidate = Path(args.candidate)
    problems: list[str] = []

    if args.only in ("all", "trace"):
        problems += compare_traces(
            load(reference / "trace.json")["trace"],
            load(candidate / "trace.json")["trace"],
            args.limit,
        )
    if args.only in ("all", "snapshot") and not problems:
        problems += compare_snapshots(
            load(reference / "snapshot.json"),
            load(candidate / "snapshot.json"),
            args.limit,
        )

    if problems:
        print("\n".join(problems))
        print(f"\nFAIL: {len(problems)} difference entries")
        return 1
    print("OK: traces and snapshots are identical")
    return 0


if __name__ == "__main__":
    sys.exit(main())
