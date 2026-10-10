#!/usr/bin/env python3
"""发布脚本 —— VERSION 的唯一写入者。

    python3 scripts/release.py 0.0.2
    python3 scripts/release.py 0.0.2 --dry-run
    python3 scripts/release.py 0.0.2 --no-tag

设计约束（docs/RELEASING.md 有完整说明）：

* VERSION 只能由本脚本修改，禁止手工编辑。
* CHANGELOG.md 必须已经包含目标版本的 `## [X.Y.Z]` 段，否则拒绝发布 ——
  应用内的「更新说明」由服务端解析 CHANGELOG.md 生成，所以先写日志再发版。
* `[Unreleased]` 段在发布时改写为目标版本段，并补上发布日期。
* 打 annotated tag `vX.Y.Z`，tag 消息就是该版本的更新说明。
* 只用标准库，不引入任何依赖。
"""

from __future__ import annotations

import argparse
import datetime as dt
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
VERSION_FILE = ROOT / "VERSION"
CHANGELOG_FILE = ROOT / "CHANGELOG.md"

SEMVER_RE = re.compile(r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$")
# 日期只认 YYYY-MM-DD（与 Go 端 config/changelog.go 的解析同一形状）：用 \S+ 时
# 标题里没有日期的情况下，`\s*` 会跨过空行把正文的第一条 `- xxx` 当成日期，
# 于是「已有版本段只补日期」的分支永远进不去。
SECTION_RE = re.compile(
    r"^## \[(?P<version>[^\]]+)\](?:[^\S\n]*-[^\S\n]*(?P<date>\d{4}-\d{2}-\d{2}))?[^\S\n]*$",
    re.MULTILINE,
)
UNRELEASED_RE = re.compile(r"^## \[Unreleased\][^\n]*\n", re.MULTILINE)


class ReleaseError(SystemExit):
    def __init__(self, message: str) -> None:
        super().__init__(f"发布中止：{message}")


def run(command: list[str], *, check: bool = True, capture: bool = False) -> str:
    result = subprocess.run(
        command, cwd=str(ROOT), check=False, text=True, capture_output=capture
    )
    if check and result.returncode != 0:
        detail = (result.stderr or result.stdout or "").strip()
        raise ReleaseError(f"命令失败：{' '.join(command)}\n{detail}")
    return (result.stdout or "").strip()


def read_version() -> str:
    if not VERSION_FILE.exists():
        raise ReleaseError("缺少 VERSION 文件")
    version = VERSION_FILE.read_text(encoding="utf-8").strip()
    if not SEMVER_RE.match(version):
        raise ReleaseError(f"VERSION 内容不是合法 semver：{version!r}")
    return version


def require_clean_tree() -> None:
    if run(["git", "status", "--porcelain"], capture=True):
        raise ReleaseError("工作区有未提交改动，先提交再发布")


def require_branch() -> str:
    branch = run(["git", "rev-parse", "--abbrev-ref", "HEAD"], capture=True)
    if branch == "HEAD":
        raise ReleaseError("处于 detached HEAD，无法发布")
    return branch


def section_body(changelog: str, version: str) -> str:
    match = None
    for candidate in SECTION_RE.finditer(changelog):
        if candidate.group("version") == version:
            match = candidate
            break
    if match is None:
        raise ReleaseError(f"CHANGELOG.md 里缺少 `## [{version}]` 段，先补写更新日志")
    start = match.end()
    end = len(changelog)
    for following in SECTION_RE.finditer(changelog, match.end()):
        end = following.start()
        break
    body = changelog[start:end].strip()
    if not body:
        raise ReleaseError(f"CHANGELOG.md 的 `## [{version}]` 段是空的")
    return body


def stamp_changelog(changelog: str, version: str, today: str) -> tuple[str, bool]:
    """把 [Unreleased] 段改写成目标版本段；若已存在目标版本段则只补日期。

    目标版本段已存在时**不再**改写 [Unreleased]：否则同一版本会出现两个
    `## [x.y.z]` 段，服务端解析与 tag 消息都取第一个，人工维护的那段被静默忽略。
    """
    for candidate in SECTION_RE.finditer(changelog):
        if candidate.group("version") != version:
            continue
        if candidate.group("date"):
            return changelog, False
        return changelog.replace(candidate.group(0), f"## [{version}] - {today}", 1), True
    if UNRELEASED_RE.search(changelog):
        stamped = UNRELEASED_RE.sub(f"## [{version}] - {today}\n", changelog, count=1)
        return stamped, True
    return changelog, False


def main() -> int:
    parser = argparse.ArgumentParser(description="发布 mirror-studio 新版本")
    parser.add_argument("version", help="目标版本号，例如 0.0.2")
    parser.add_argument("--dry-run", action="store_true", help="只校验和打印，不写文件不打 tag")
    parser.add_argument("--no-tag", action="store_true", help="不创建 git tag")
    parser.add_argument("--no-push", action="store_true", help="不推送 tag")
    args = parser.parse_args()

    target = args.version.strip().lstrip("v")
    if not SEMVER_RE.match(target):
        raise ReleaseError(f"目标版本号不是合法 semver：{args.version!r}")

    current = read_version()
    if current == target:
        raise ReleaseError(f"VERSION 已经是 {target}")
    if not args.dry_run:
        require_clean_tree()
    branch = require_branch()

    changelog = CHANGELOG_FILE.read_text(encoding="utf-8")
    today = dt.date.today().isoformat()
    updated, stamped = stamp_changelog(changelog, target, today)
    notes = section_body(updated, target)

    print(f"当前版本      {current}")
    print(f"目标版本      {target}")
    print(f"分支          {branch}")
    print(f"CHANGELOG 段  {'已补日期' if stamped else '已存在'}")
    print("更新说明：")
    for line in notes.splitlines():
        print(f"  {line}")

    if args.dry_run:
        print("\n--dry-run：未写入任何文件")
        return 0

    # 显式 newline="\n"：Windows 上默认会把整份文件改写成 CRLF。CHANGELOG 是
    # 服务端解析的数据源，VERSION 还会被 Dockerfile 用 $(cat VERSION) 拼进
    # -ldflags —— 多出来的 \r 会跟着版本号进二进制。
    CHANGELOG_FILE.write_text(updated, encoding="utf-8", newline="\n")
    VERSION_FILE.write_text(f"{target}\n", encoding="utf-8", newline="\n")

    run(["git", "add", "VERSION", "CHANGELOG.md"])
    run(["git", "commit", "-m", f"chore(release): {target}"])
    print(f"已提交 chore(release): {target}")

    if not args.no_tag:
        run(["git", "tag", "-a", f"v{target}", "-m", f"mirror-studio {target}\n\n{notes}"])
        print(f"已创建 tag v{target}")
        if not args.no_push:
            run(["git", "push", "origin", branch])
            run(["git", "push", "origin", f"v{target}"])
            print(f"已推送 {branch} 与 v{target}")

    print(f"\n部署：git checkout v{target} && docker compose up -d --build")
    return 0


if __name__ == "__main__":
    sys.exit(main())
