import os
import sys
import time
from urllib.parse import urlparse

import psycopg


def main() -> int:
    database_url = os.getenv("DATABASE_URL")
    if not database_url:
        return 0

    parsed = urlparse(database_url)
    if parsed.scheme not in {"postgres", "postgresql"}:
        return 0

    attempts = 30
    for _ in range(attempts):
        try:
            psycopg.connect(
                dbname=parsed.path.lstrip("/"),
                user=parsed.username,
                password=parsed.password,
                host=parsed.hostname,
                port=parsed.port or 5432,
                connect_timeout=2,
            ).close()
            return 0
        except psycopg.OperationalError:
            time.sleep(2)
    print("数据库连接失败", file=sys.stderr)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
