"""Seeds the deterministic fixture rows the differential corpus depends on.

Inserts rows directly with plain SQL: they are test setup, not application
behaviour, and both stacks share the same application table/column names so the
same statement works for either database.

Usage:
    python tools/diff/fixtures.py --db .reftmp/ref.sqlite3
"""

from __future__ import annotations

import argparse
import sqlite3
from pathlib import Path

NOW = "2026-01-01 00:00:00.000000"

SENSITIVE_WORDS = (
    ("禁止词A", "contains", 1, "差分固定夹具"),
    ("exactblock", "exact", 1, "差分固定夹具"),
    ("停用词", "contains", 0, "差分固定夹具"),
)

IP_RULES = (
    ("203.0.113.9", 1, "差分固定夹具", None),
    ("203.0.113.10", 1, "差分固定夹具", "2020-01-01 00:00:00.000000"),
)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--db", required=True)
    args = parser.parse_args()
    path = Path(args.db)
    if not path.exists():
        raise SystemExit(f"database not found: {path}")

    connection = sqlite3.connect(path)
    for keyword, mode, active, remark in SENSITIVE_WORDS:
        connection.execute(
            "INSERT OR IGNORE INTO risk_sensitivewordrule "
            "(created_at, updated_at, keyword, match_mode, is_active, remark) "
            "VALUES (?, ?, ?, ?, ?, ?)",
            (NOW, NOW, keyword, mode, active, remark),
        )
    for ip, blocked, reason, expires in IP_RULES:
        connection.execute(
            "INSERT OR IGNORE INTO risk_ipaccessrule "
            "(created_at, updated_at, ip_address, is_blocked, reason, expires_at) "
            "VALUES (?, ?, ?, ?, ?, ?)",
            (NOW, NOW, ip, blocked, reason, expires),
        )
    connection.commit()
    connection.close()
    print(f"fixtures applied to {path}")


if __name__ == "__main__":
    main()
