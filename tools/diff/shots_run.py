#!/usr/bin/env python3
"""Boot one backend against a prepared database and hold it for screenshotting.

`run.py` drives the API corpus and tears its server down at the end. This tool
does the opposite: it serves a database that a corpus run already produced, so a
browser can capture pages from it, and it stays up until it is interrupted.

The same database file is normally copied once per target so both sides render
byte-identical rows; the screenshots then differ only through what the server
computes at render time (relative timestamps, random captchas, CSRF tokens).

Usage
  python3 tools/diff/shots_run.py --target python --db <path> --port 8013 \
      --out .reftmp/shots/python

Writes <out>/backend.log and <out>/ready (once healthy), then blocks on SIGINT
or SIGTERM.
"""

from __future__ import annotations

import argparse
import os
import pathlib
import signal
import sys
import time

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

from run import (  # noqa: E402  (path setup must run first)
    BACKEND_PORT,
    COMMON_ENV,
    GO_BINARY,
    REF_PYTHON,
    free_port,
    spawn_backend,
    stop_backend,
    wait_for_http,
)


def install_signal_handlers() -> None:
    """Turn SIGTERM into the same unwind as SIGINT.

    Without this, `kill <pid>` leaves the backend orphaned: it is started in its
    own session, so it survives, keeps the port and the *next* server started for
    a comparison silently serves the port while never being reached.
    """

    def handler(signum, frame):  # noqa: ANN001, ARG001
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, handler)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--target", required=True, choices=("python", "go"))
    parser.add_argument("--db", required=True, help="database file to serve")
    parser.add_argument("--port", type=int, default=BACKEND_PORT + 2)
    parser.add_argument("--out", required=True)
    args = parser.parse_args()

    install_signal_handlers()
    # Refuse to start when the port is taken: otherwise the health check below
    # succeeds against whatever is already listening.
    free_port(args.port)

    out_dir = pathlib.Path(args.out).resolve()
    out_dir.mkdir(parents=True, exist_ok=True)
    ready = out_dir / "ready"
    if ready.exists():
        ready.unlink()

    db_path = pathlib.Path(args.db).resolve()
    if not db_path.exists():
        raise SystemExit(f"missing database {db_path}")

    origin = f"http://127.0.0.1:{args.port}"
    env = {
        **os.environ,
        **COMMON_ENV,
        "PUBLIC_ORIGIN": origin,
        "APP_PORT": str(args.port),
        "DATABASE_URL": f"sqlite:///{db_path}",
        "MOCK_PROVIDER_ENABLED": "1",
        "EMAIL_VERIFICATION_ENABLED": "0",
        "LOGIN_CAPTCHA_ENABLED": "1",
    }
    env.pop("MEDIA_ROOT", None)

    if args.target == "python":
        command = [
            str(REF_PYTHON),
            "manage.py",
            "runserver",
            f"127.0.0.1:{args.port}",
            "--noreload",
        ]
        env["DJANGO_SETTINGS_MODULE"] = "config.settings.dev"
    else:
        if not GO_BINARY.exists():
            raise SystemExit(f"missing {GO_BINARY}; build it first")
        command = [str(GO_BINARY), "--listen", f"127.0.0.1:{args.port}"]
        # Same dev flag as the reference's config.settings.dev, so the security
        # headers and cookie attributes the pages trigger match.
        env["DEBUG"] = "1"
        env["GENERATION_WORKER_POLL_SECONDS"] = "1"

    log_handle = (out_dir / "backend.log").open("wb")
    backend = spawn_backend(command, env, log_handle)
    try:
        wait_for_http(f"{origin}/healthz")
        if backend.poll() is not None:
            print(
                f"backend exited with {backend.returncode} during startup; "
                f"see {out_dir / 'backend.log'}",
                file=sys.stderr,
            )
            return 1
        ready.write_text(f"{origin}\n")
        print(f"ready: {origin} ({args.target}, db={db_path})", flush=True)
        while True:
            if backend.poll() is not None:
                print(f"backend exited with {backend.returncode}", file=sys.stderr)
                return 1
            time.sleep(0.5)
    except KeyboardInterrupt:
        return 0
    finally:
        stop_backend(backend)
        log_handle.close()


if __name__ == "__main__":
    sys.exit(main())
