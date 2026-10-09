#!/usr/bin/env python3
"""Copy the rendered rows of a reference database into a rewrite database.

The 0.0.1 rewrite is a fresh baseline and refuses to run against a database the
Django reference migrated (`table "users_user" already exists`), which is the
documented behaviour — see docs/RELEASING.md. Screenshot comparison still needs
both servers to render *the same rows*, so this tool takes the database the
rewrite created itself, replaces its seeded rows with the reference's, table by
table, and leaves the schema bookkeeping of each side alone.

Only tables present in both databases are touched, and the column lists are
verified to be identical first; a mismatch aborts instead of inserting into the
wrong columns. Django's own bookkeeping tables (auth_*, django_*) and the session
tables are skipped: sessions are re-established by the capture, and neither side
renders auth_*.

Usage
  python3 tools/diff/bridge_database.py --from <reference.sqlite3> --into <rewrite.sqlite3>
"""

from __future__ import annotations

import argparse
import sqlite3
import sys

SKIP = {
    # Django bookkeeping and machinery the rewrite implements differently.
    "django_migrations",
    "django_content_type",
    "django_admin_log",
    "django_session",
    "auth_group",
    "auth_group_permissions",
    "auth_permission",
    "users_user_groups",
    "users_user_user_permissions",
    # The rewrite's own bookkeeping.
    "schema_migrations",
    "app_session",
}


def tables(connection: sqlite3.Connection) -> set[str]:
    return {
        row[0]
        for row in connection.execute("SELECT name FROM sqlite_master WHERE type = 'table'")
    }


def columns(connection: sqlite3.Connection, table: str) -> list[str]:
    return [row[1] for row in connection.execute(f'PRAGMA table_info("{table}")')]


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--from", dest="source", required=True)
    parser.add_argument("--into", dest="target", required=True)
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()

    source = sqlite3.connect(f"file:{args.source}?mode=ro", uri=True)
    target = sqlite3.connect(args.target)
    target.execute("PRAGMA foreign_keys = OFF")

    shared = sorted((tables(source) & tables(target)) - SKIP)
    if not shared:
        raise SystemExit("no shared tables; are these the databases you meant?")

    copied: list[tuple[str, int]] = []
    for table in shared:
        source_columns = columns(source, table)
        target_columns = columns(target, table)
        if source_columns != target_columns:
            print(f"column mismatch in {table}:", file=sys.stderr)
            print(f"  reference: {source_columns}", file=sys.stderr)
            print(f"  rewrite  : {target_columns}", file=sys.stderr)
            return 1
        rows = list(source.execute(f'SELECT * FROM "{table}"'))
        if args.dry_run:
            copied.append((table, len(rows)))
            continue
        target.execute(f'DELETE FROM "{table}"')
        placeholders = ", ".join("?" * len(source_columns))
        target.executemany(f'INSERT INTO "{table}" VALUES ({placeholders})', rows)
        copied.append((table, len(rows)))
    if not args.dry_run:
        target.commit()

    skipped = sorted((tables(source) | tables(target)) - set(shared) - SKIP)
    print(f"bridged {len(copied)} tables from {args.source} into {args.target}")
    for table, count in copied:
        print(f"  {table:38} {count:5} rows")
    if skipped:
        print(f"  (not shared, left as is: {', '.join(skipped)})")
    if args.dry_run:
        print("dry run: nothing written")
    return 0


if __name__ == "__main__":
    sys.exit(main())
