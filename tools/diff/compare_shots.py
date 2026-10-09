#!/usr/bin/env python3
"""Pixel comparison of two screenshot directories (S4 / section 4 gate).

Both sides are captured from the *same* database file and the same media tree,
so any difference is something the server decided at render time. That is
deliberately strict: a layout, style, copy or route change shows up immediately.

Three kinds of difference are reported separately instead of being hidden:

  * sub-perceptual noise. A composited page can come back with a channel or two
    of difference across the whole surface (gradient dithering, layer
    promotion). `--pixel-tolerance` (default 8/255, well below anything visible)
    decides what counts as a differing pixel; the raw ratio is printed next to
    the tolerated one so a large uniform shift stays visible.
  * mechanically random regions, i.e. the login captcha image, which every
    request regenerates. They are excluded by `--ignore-region` and both ratios
    are printed.
  * accepted differences, passed as `--accepted PAGE=reason` and echoed in the
    output, so the exception lives in the tool's own record rather than in a
    report's prose.

Usage
  .venv-ref/bin/python tools/diff/compare_shots.py <ref-dir> <go-dir> \
      [--out <diff-dir>] [--threshold 0.0001] \
      [--ignore-region x,y,w,h ...] [--accepted page=reason ...]
"""

from __future__ import annotations

import argparse
import pathlib
import sys

from PIL import Image, ImageChops

# The captcha image is regenerated on every request, so the pixels under it can
# never match. The boxes are fixed by the templates; the regions come from the
# measured bounding boxes of the difference on each page, plus a 3px margin.
CAPTCHA_REGIONS: dict[str, tuple[int, int, int, int]] = {
    "login.png": (448, 466, 291, 63),
    "forgot-password.png": (448, 630, 291, 63),
    "account-security.png": (384, 610, 419, 63),
}


def load_pair(ref_path: pathlib.Path, go_path: pathlib.Path) -> tuple[Image.Image, Image.Image]:
    ref = Image.open(ref_path).convert("RGB")
    go = Image.open(go_path).convert("RGB")
    if ref.size != go.size:
        raise ValueError(f"size mismatch: ref={ref.size} go={go.size}")
    return ref, go


def difference_ratio(
    ref: Image.Image,
    go: Image.Image,
    tolerance: int,
    ignored: list[tuple[int, int, int, int]],
) -> tuple[float, float]:
    """Returns (raw differing ratio, ratio with tolerated pixels and ignored
    regions removed).

    The per-channel maximum is taken first: a pixel only counts when some
    channel moved by more than the tolerance, which a plain luminance
    conversion would under-report for saturated single-channel shifts.
    """
    diff = ImageChops.difference(ref, go)
    red, green, blue = diff.split()
    max_delta = ImageChops.lighter(ImageChops.lighter(red, green), blue)

    pixels = diff.size[0] * diff.size[1]
    raw = sum(max_delta.histogram()[1:])

    significant_mask = max_delta.point(lambda value: 255 if value > tolerance else 0)
    for region in ignored:
        x, y, width, height = region
        significant_mask.paste(0, (x, y, x + width, y + height))
    # The mask holds 0 or 255 per pixel, so the count is the top histogram bucket.
    significant = significant_mask.histogram()[255]

    return raw / pixels, significant / pixels


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("ref_dir")
    parser.add_argument("go_dir")
    parser.add_argument("--out", default="")
    parser.add_argument(
        "--threshold",
        type=float,
        default=0.0001,
        help="allowed ratio of differing pixels per page (default 0.0001 = 0.01%%)",
    )
    parser.add_argument(
        "--pixel-tolerance",
        type=int,
        default=8,
        help="per-channel difference below which a pixel is not counted (default 8/255)",
    )
    parser.add_argument(
        "--ignore-region",
        action="append",
        default=[],
        metavar="X,Y,W,H",
        help="exclude a region from every page (repeatable)",
    )
    parser.add_argument(
        "--no-captcha-mask",
        action="store_true",
        help="compare the captcha boxes too, instead of the built-in masks",
    )
    parser.add_argument(
        "--accepted",
        action="append",
        default=[],
        metavar="PAGE=REASON",
        help="record a page whose difference is accepted, with its reason (repeatable)",
    )
    args = parser.parse_args()

    ref_dir, go_dir = pathlib.Path(args.ref_dir), pathlib.Path(args.go_dir)
    diff_dir = pathlib.Path(args.out) if args.out else None
    if diff_dir:
        diff_dir.mkdir(parents=True, exist_ok=True)

    extra = [tuple(int(part) for part in item.split(",")) for item in args.ignore_region]
    accepted: dict[str, str] = {}
    for item in args.accepted:
        name, _, reason = item.partition("=")
        accepted[name] = reason

    pages = sorted(path.name for path in ref_dir.glob("*.png"))
    if not pages:
        raise SystemExit(f"no screenshots in {ref_dir}")
    missing = [name for name in pages if not (go_dir / name).exists()]
    if missing:
        raise SystemExit(f"missing on the rewrite side: {missing}")

    failures: list[str] = []
    print(f"pixel tolerance: {args.pixel_tolerance}/255; threshold: {args.threshold:.4%}")
    print(f"{'page':22} {'size':10} {'raw':>10} {'compared':>10}  verdict")
    for name in pages:
        ref, go = load_pair(ref_dir / name, go_dir / name)
        regions = list(extra)
        if not args.no_captcha_mask and name in CAPTCHA_REGIONS:
            regions.append(CAPTCHA_REGIONS[name])
        raw, compared = difference_ratio(ref, go, args.pixel_tolerance, regions)
        if name in accepted:
            verdict = f"ACCEPTED ({accepted[name]})"
        elif compared <= args.threshold:
            verdict = "OK"
        else:
            verdict = "FAIL"
            failures.append(f"{name}: {compared:.4%} of pixels differ")
        print(
            f"{name:22} {ref.size[0]}x{ref.size[1]:<5} {raw:9.4%} {compared:9.4%}  {verdict}"
        )
        if diff_dir and compared > 0:
            ImageChops.difference(ref, go).save(diff_dir / name)

    if failures:
        print(f"\nFAIL: {len(failures)} page(s) differ beyond {args.threshold:.4%}")
        for failure in failures:
            print(f"  - {failure}")
        return 1
    print(f"\nOK: {len(pages)} pages rendered identically "
          f"({len(accepted)} accepted difference(s) recorded)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
