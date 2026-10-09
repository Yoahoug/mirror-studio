#!/usr/bin/env python3
"""Upstream wire-recording comparison.

`compare.py` replays the API corpus and diffs the traces and SQLite snapshots.
This tool covers the other half of section 10: the requests the backend itself
sends upstream, recorded by the fake provider (`fake_upstream.py`).

Two classes of difference are expected and are *verified as such* here rather
than masked:

1. Encoding of a re-encoded reference image. The reference implementation saves
   through Pillow with `optimize=True, progressive=True`; Go's stdlib
   `image/jpeg` and `image/png` offer no progressive or Huffman-optimised
   encoding, so the byte length of the lossy re-encode cannot be reproduced.
   The contract that matters — part name, content type, filename extension,
   decoded pixel dimensions, and both payloads staying under the byte cap — is
   asserted explicitly, and the observed lengths are printed so the gap stays
   visible.

2. Ordering between requests that both implementations issue concurrently. The
   scene fan-out in novel generation issues its image requests in parallel and
   neither implementation defines an order, so a maximal run of same-(method,
   path) calls is compared as a set. Anything else is a failure.

Run: python3 tools/diff/compare_upstream.py .reftmp/run-ref .reftmp/run-go
"""

from __future__ import annotations

import json
import pathlib
import sys
from collections import Counter

IMAGE_PART_TYPES = ("image/jpeg", "image/png", "image/webp")


def load(run_dir: pathlib.Path, name: str = "upstream-final.jsonl") -> list[dict]:
    path = run_dir / name
    if not path.exists():
        raise SystemExit(f"missing recording: {path}")
    return [json.loads(line) for line in path.read_text().splitlines() if line.strip()]


def canonical(record: dict) -> dict:
    """Stable form of one recorded upstream call. Image part lengths are
    replaced by a placeholder; everything else is compared verbatim."""
    body = record.get("body")
    if isinstance(body, dict) and "__multipart__" in body:
        parts = []
        for part in body["__multipart__"]:
            part = dict(part)
            if part.get("content_type") in IMAGE_PART_TYPES:
                part["length"] = "<image-bytes>"
            parts.append(part)
        body = {"__multipart__": parts}
    return {
        "method": record.get("method"),
        "path": record.get("path"),
        "authorization": record.get("authorization"),
        "content_type": record.get("content_type"),
        "body": body,
    }


def image_parts(record: dict) -> list[dict]:
    body = record.get("body")
    if not isinstance(body, dict):
        return []
    return [
        part
        for part in body.get("__multipart__", [])
        if part.get("content_type") in IMAGE_PART_TYPES
    ]


def key(record: dict) -> tuple[str, str]:
    return (record.get("method") or "", record.get("path") or "")


def sort_runs(records: list[dict]) -> list[dict]:
    """Sort each maximal run of consecutive same-(method, path) calls. Such a
    run is a set of concurrent requests; the order inside it is scheduler
    dependent in both implementations."""
    ordered: list[dict] = []
    index = 0
    while index < len(records):
        end = index + 1
        while end < len(records) and key(records[end]) == key(records[index]):
            end += 1
        run = records[index:end]
        if len(run) > 1:
            run = sorted(run, key=lambda item: json.dumps(item, sort_keys=True, ensure_ascii=False))
        ordered.extend(run)
        index = end
    return ordered


def describe_image_lengths(label: str, records: list[dict]) -> None:
    for position, record in enumerate(records):
        for part in image_parts(record):
            print(
                f"  {label} call {position:2} {record.get('method')} {record.get('path')}: "
                f"{part.get('filename')} {part.get('content_type')} "
                f"{part.get('width')}x{part.get('height')} {part.get('length')} bytes"
            )


def compare_image_contracts(ref: list[dict], go: list[dict], failures: list[str]) -> None:
    """Every Re-encoded image part must agree on everything except byte length."""
    ref_parts = [(key(r), p) for r in ref for p in image_parts(r)]
    go_parts = [(key(r), p) for r in go for p in image_parts(r)]
    if len(ref_parts) != len(go_parts):
        failures.append(f"image part count differs: ref={len(ref_parts)} go={len(go_parts)}")
        return
    for (ref_key, ref_part), (go_key, go_part) in zip(ref_parts, go_parts):
        if ref_key != go_key:
            failures.append(f"image part on a different call: ref={ref_key} go={go_key}")
            continue
        for field in ("name", "filename", "content_type", "width", "height"):
            if ref_part.get(field) != go_part.get(field):
                failures.append(
                    f"{ref_key[1]} image part {field}: "
                    f"ref={ref_part.get(field)!r} go={go_part.get(field)!r}"
                )
        if ref_part.get("length") == go_part.get("length"):
            continue
        # Length may differ (see the module docstring); the payload must still be
        # a compressed image under the byte cap on both sides. The cap is the
        # only size the contract fixes, and the recordings are far below it, so
        # this is a sanity check, not an equality check.
        cap = 4 * 1024 * 1024
        for label, part in (("ref", ref_part), ("go", go_part)):
            if not isinstance(part.get("length"), int) or part["length"] > cap:
                failures.append(f"{label} image part exceeds the {cap} byte cap: {part.get('length')}")


def main() -> int:
    if len(sys.argv) != 3:
        raise SystemExit(__doc__)
    ref_dir, go_dir = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2])
    ref, go = load(ref_dir), load(go_dir)

    print(f"ref: {len(ref)} upstream calls from {ref_dir}")
    print(f"go : {len(go)} upstream calls from {go_dir}")
    if len(ref) != len(go):
        print("FAIL: call counts differ")
        return 1

    failures: list[str] = []
    ref_canon = [canonical(record) for record in ref]
    go_canon = [canonical(record) for record in go]

    missing = Counter(map(repr, ref_canon)) - Counter(map(repr, go_canon))
    extra = Counter(map(repr, go_canon)) - Counter(map(repr, ref_canon))
    if missing:
        failures.append(f"{sum(missing.values())} reference call(s) never issued: "
                        + "; ".join(item[:200] for item in missing))
    if extra:
        failures.append(f"{sum(extra.values())} unexpected call(s) issued: "
                        + "; ".join(item[:200] for item in extra))

    ordered_ref, ordered_go = sort_runs(ref_canon), sort_runs(go_canon)
    if ordered_ref != ordered_go:
        for index, (want, got) in enumerate(zip(ordered_ref, ordered_go)):
            if want != got:
                failures.append(
                    f"call {index} differs beyond concurrency reordering:\n"
                    f"    ref: {json.dumps(want, ensure_ascii=False, sort_keys=True)[:400]}\n"
                    f"    go : {json.dumps(got, ensure_ascii=False, sort_keys=True)[:400]}"
                )
    elif ref_canon != go_canon:
        changed = [i for i, (a, b) in enumerate(zip(ref_canon, go_canon)) if a != b]
        print(f"note: calls {changed} are a concurrency interleaving "
              f"(same method+path, identical payload set); both implementations "
              f"issue these in parallel with no defined order")

    compare_image_contracts(ref, go, failures)

    print("image parts as sent upstream:")
    describe_image_lengths("ref", ref)
    describe_image_lengths("go ", go)

    if failures:
        print(f"\nFAIL: {len(failures)} difference(s)")
        for failure in failures:
            print(f"  - {failure}")
        return 1
    print("\nOK: same upstream calls with the same payloads")
    return 0


if __name__ == "__main__":
    sys.exit(main())
