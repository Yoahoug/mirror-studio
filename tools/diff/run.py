"""End-to-end differential runner: boots a backend, replays the corpus, records.

    python tools/diff/run.py --target python --out .reftmp/run-ref
    python tools/diff/run.py --target go --out .reftmp/run-go

Both targets get a pristine database, a pristine media directory, the same
fixture rows and the same fake upstream, so the two output directories can be
compared directly by tools/diff/compare.py.
"""

from __future__ import annotations

import argparse
import os
import shutil
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
DIFF = ROOT / "tools" / "diff"
REF_PYTHON = ROOT / ".venv-ref" / "bin" / "python"
GO_BINARY = ROOT / "bin" / "mirror-studio"

UPSTREAM_PORT = 8099
BACKEND_PORT = 8011

# Values shared by both backends. These are part of the frozen environment
# contract: the differential run must not depend on anything outside this map.
COMMON_ENV = {
    "SECRET_KEY": "diff-secret-key-for-parity-testing",
    "PUBLIC_ORIGIN": f"http://127.0.0.1:{BACKEND_PORT}",
    "TIME_ZONE": "Asia/Shanghai",
    "LANGUAGE_CODE": "zh-hans",
    "REGISTER_ENABLED": "1",
    "LOGIN_CAPTCHA_ENABLED": "1",
    "DEFAULT_DAILY_FREE_QUOTA": "20",
    "IMAGE_RETENTION_DAYS": "30",
    "IMAGE_HISTORY_SOFT_DELETE_RETENTION_DAYS": "7",
    "IMAGE_PREVIEW_MAX_DIMENSION": "768",
    "DAILY_USER_TASK_LIMIT": "50",
    "MAX_PENDING_TASKS": "3",
    "RISK_LOGIN_FAIL_LIMIT": "2",
    "RISK_LOGIN_LOCK_MINUTES": "15",
    "RISK_TASK_WINDOW_SECONDS": "60",
    "RISK_TASK_LIMIT_PER_WINDOW": "6",
    "TRUST_X_FORWARDED_FOR": "1",
    "USE_X_FORWARDED_HOST": "1",
    "RESUMABLE_UPLOAD_CHUNK_SIZE": "64",
    "RESUMABLE_UPLOAD_SESSION_TTL_SECONDS": "86400",
    "MAX_REFERENCE_IMAGE_BYTES": "10485760",
    "MAX_REMOTE_IMAGE_BYTES": "26214400",
    "REMOTE_IMAGE_ALLOWED_HOSTS": "s3.siliconflow.cn",
    "NOVELVIZ_MAX_EPUB_UPLOAD_BYTES": "83886080",
    "NOVELVIZ_EVENT_LOG_LIMIT": "120",
    "NOVELVIZ_MAX_SCENES": "10",
    "NOVELVIZ_IMAGE_CONCURRENCY": "3",
    "NOVELVIZ_IMAGE_MAX_RETRIES": "2",
    "NOVELVIZ_IMAGE_TIMEOUT_SECONDS": "60",
    "NOVELVIZ_TEXT_MAX_RETRIES": "2",
    "NOVELVIZ_TEXT_CONNECT_TIMEOUT_SECONDS": "10",
    "NOVELVIZ_TEXT_READ_TIMEOUT_SECONDS": "60",
    "NOVELVIZ_SSE_POLL_SECONDS": "1",
    "NOVELVIZ_STALL_REQUEUE_SECONDS": "60",
    "FIELD_CRYPT_KEY": "diff-field-crypt-key",
    "CONSOLE_API_RATE_LIMIT": "600",
    "PROVIDER_NAME": "默认图片渠道",
    "PROVIDER_VENDOR": "OpenAI-Compatible",
    "PROVIDER_VENDOR_CODE": "openai",
    "PROVIDER_BASE_URL": f"http://127.0.0.1:{UPSTREAM_PORT}/v1",
    "PROVIDER_API_PATH": "/images/generations",
    "PROVIDER_MODEL_NAME": "diff-image-model",
    "PROVIDER_API_KEY": "diff-image-key",
    "PROVIDER_TIMEOUT_SECONDS": "60",
    "PROVIDER_MAX_RETRIES": "2",
    "DEFAULT_CHANNEL_CODE": "default",
    "LLM_CHANNEL_CODE": "default-llm",
    "LLM_PROVIDER_NAME": "默认 LLM 渠道",
    "LLM_PROVIDER_VENDOR": "OpenAI-Compatible",
    "LLM_BASE_URL": f"http://127.0.0.1:{UPSTREAM_PORT}/v1",
    "LLM_API_PATH": "/chat/completions",
    "LLM_MODEL_NAME": "diff-llm-model",
    "LLM_API_KEY": "diff-llm-key",
    "LLM_TIMEOUT_SECONDS": "60",
    "LLM_MAX_RETRIES": "2",
    "LLM_PRIORITY": "1",
    "NOVELVIZ_API_BASE_URL": f"http://127.0.0.1:{UPSTREAM_PORT}/v1",
    "NOVELVIZ_TEXT_MODEL": "diff-llm-model",
    "NOVELVIZ_TEXT_API_PATH": "/chat/completions",
    "NOVELVIZ_TEXT_API_KEY": "diff-llm-key",
    "NOVELVIZ_IMAGE_MODEL": "diff-image-model",
    "NOVELVIZ_IMAGE_API_PATH": "/images/generations",
    "NOVELVIZ_IMAGE_API_KEY": "diff-image-key",
    "NOVELVIZ_IMAGE_SIZE": "1024x1024",
    "NOVELVIZ_IMAGE_QUALITY": "high",
    "NOVELVIZ_BOOK_DIR": str(ROOT / "novel"),
    "ADMIN_USERNAME": "admin",
    "ADMIN_PASSWORD": "DiffAdmin123!@#",
    "ADMIN_EMAIL": "admin@example.com",
    # Seeded into core_siteconfig on first boot; the DB row then wins over the
    # environment, exactly as in the reference implementation.
    "EMAIL_VERIFICATION_ENABLED": "0",
    "EMAIL_HOST": "",
    "EMAIL_HOST_USER": "",
    "EMAIL_HOST_PASSWORD": "",
    "DEFAULT_FROM_EMAIL": "noreply@example.com",
    "SITE_NAME": "绘镜 Studio",
}


def free_port(port: int) -> None:
    """Fail loudly instead of silently reusing a stale listener."""
    with socket.socket() as probe:
        if probe.connect_ex(("127.0.0.1", port)) == 0:
            raise SystemExit(f"port {port} is already in use; stop the stale process first")


def wait_for_http(url: str, timeout: float = 120.0) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=3) as response:
                if response.status < 500:
                    return
        except (urllib.error.URLError, OSError):
            time.sleep(0.3)
    raise SystemExit(f"backend did not become ready: {url}")


def write_fixture_epub(path: Path) -> None:
    sys.path.insert(0, str(DIFF))
    from harness import epub_bytes

    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(epub_bytes())


def start_upstream(log: Path) -> subprocess.Popen:
    free_port(UPSTREAM_PORT)
    # The server's own stdout must not share the recorder file, which is JSONL.
    with (log.parent / "upstream-server.log").open("wb") as handle:
        process = subprocess.Popen(
            [sys.executable, str(DIFF / "fake_upstream.py"), "--port", str(UPSTREAM_PORT), "--log", str(log)],
            cwd=str(DIFF),
            stdout=handle,
            stderr=subprocess.STDOUT,
        )
    wait_for_http(f"http://127.0.0.1:{UPSTREAM_PORT}/result.png")
    return process


def prepare_database(target: str, db_path: Path) -> None:
    """Build a pristine database. Go migrates and seeds itself on startup."""
    db_path.parent.mkdir(parents=True, exist_ok=True)
    if db_path.exists():
        db_path.unlink()
    if target == "go":
        return
    env = {
        **os.environ,
        **COMMON_ENV,
        "DJANGO_SETTINGS_MODULE": "config.settings.dev",
        "DATABASE_URL": f"sqlite:///{db_path}",
    }
    for step in (
        [str(REF_PYTHON), "manage.py", "migrate", "--noinput"],
        [str(REF_PYTHON), "scripts/seed_siteconfig.py"],
        [str(REF_PYTHON), "scripts/seed_demo_data.py"],
    ):
        subprocess.run(step, cwd=str(ROOT), env=env, check=True, stdout=subprocess.DEVNULL)


def apply_fixtures(db_path: Path) -> None:
    """Fixture rows go in once the schema exists, whichever stack created it."""
    subprocess.run(
        [sys.executable, str(DIFF / "fixtures.py"), "--db", str(db_path)],
        check=True,
        stdout=subprocess.DEVNULL,
    )


def reset_media() -> None:
    for path in (ROOT / "media", ROOT / "novel"):
        if path.exists():
            shutil.rmtree(path)
    (ROOT / "media").mkdir(parents=True, exist_ok=True)
    (ROOT / "novel").mkdir(parents=True, exist_ok=True)


def set_site_config(db_path: Path, key: str, value: str) -> None:
    """Flip a seeded site config between profiles.

    core_siteconfig shadows the environment once a row exists, so the email
    profile has to change the stored value rather than the process environment.
    """
    import sqlite3

    connection = sqlite3.connect(db_path)
    connection.execute(
        "UPDATE core_siteconfig SET value_encrypted = ? WHERE key = ?", (value, key)
    )
    connection.commit()
    connection.close()


def spawn_backend(command: list[str], env: dict, log_handle) -> subprocess.Popen:
    return subprocess.Popen(
        command,
        cwd=str(ROOT),
        env=env,
        stdout=log_handle,
        stderr=subprocess.STDOUT,
        start_new_session=True,
    )


def stop_backend(backend: subprocess.Popen) -> None:
    backend.terminate()
    try:
        backend.wait(timeout=20)
    except subprocess.TimeoutExpired:
        backend.kill()


# Every profile gets a database; profiles that must observe each other's rows
# share one on purpose (the email profile inspects accounts the main profile
# registered).
PROFILE_DB = {
    "main": "app-primary.sqlite3",
    "email": "app-primary.sqlite3",
    "upstream": "app-upstream.sqlite3",
}

# Per-profile environment overrides. MOCK_PROVIDER_ENABLED decides whether the
# generator talks to the mock adapter or to the recorded fake upstream.
PROFILE_ENV = {
    "main": {"MOCK_PROVIDER_ENABLED": "1", "EMAIL_VERIFICATION_ENABLED": "0"},
    "email": {"MOCK_PROVIDER_ENABLED": "1", "EMAIL_VERIFICATION_ENABLED": "1"},
    "upstream": {"MOCK_PROVIDER_ENABLED": "0", "EMAIL_VERIFICATION_ENABLED": "0"},
}


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--target", required=True, choices=("python", "go"))
    parser.add_argument("--out", required=True)
    parser.add_argument("--profiles", default="main,email,upstream")
    parser.add_argument("--keep", action="store_true")
    args = parser.parse_args()

    if args.target == "go" and not GO_BINARY.exists():
        raise SystemExit(f"missing {GO_BINARY}; build it first")

    profiles = [item.strip() for item in args.profiles.split(",") if item.strip()]
    out_dir = Path(args.out).resolve()
    if out_dir.exists():
        shutil.rmtree(out_dir)
    out_dir.mkdir(parents=True)

    databases = {name: (out_dir / PROFILE_DB[name]).resolve() for name in profiles}
    for db_path in sorted(set(databases.values())):
        prepare_database(args.target, db_path)
    reset_media()
    write_fixture_epub(ROOT / "novel" / "diff-fixture.epub")

    upstream = start_upstream(out_dir / "upstream.jsonl")
    if args.target == "python":
        command = [str(REF_PYTHON), "manage.py", "runserver", f"127.0.0.1:{BACKEND_PORT}", "--noreload"]
        base = {
            **COMMON_ENV,
            "DJANGO_SETTINGS_MODULE": "config.settings.dev",
            "GENERATION_TASKS_RUN_INLINE": "1",
            "NOVELVIZ_RUN_INLINE": "1",
            "NOVELVIZ_USE_SUBPROCESS": "0",
        }
    else:
        command = [str(GO_BINARY), "--listen", f"127.0.0.1:{BACKEND_PORT}"]
        # The reference target runs config.settings.dev, so the Go target runs
        # with the same dev flag: debug asset tags, non-Secure cookies and no
        # HSTS, exactly like the reference.
        base = {
            **COMMON_ENV,
            "APP_PORT": str(BACKEND_PORT),
            "DEBUG": "1",
            "GENERATION_WORKER_POLL_SECONDS": "1",
            "GENERATION_WORKER_CONCURRENCY": "2",
        }

    log_handle = (out_dir / "backend.log").open("wb")
    backend: subprocess.Popen | None = None
    prepared: set[Path] = set()
    status = 0
    try:
        for profile in profiles:
            db_path = databases[profile]
            env = {**os.environ, **base, **PROFILE_ENV[profile], "DATABASE_URL": f"sqlite:///{db_path}"}
            if "MEDIA_ROOT" in env:
                # The reference keeps MEDIA_ROOT under the repository; media files
                # are not part of the compared contract.
                env.pop("MEDIA_ROOT")
            if backend is not None:
                stop_backend(backend)
            else:
                free_port(BACKEND_PORT)
            backend = spawn_backend(command, env, log_handle)
            wait_for_http(f"http://127.0.0.1:{BACKEND_PORT}/healthz")
            # Fixtures and seeded config need the schema, which the Go service and
            # the reference both create before they start serving.
            if db_path not in prepared:
                apply_fixtures(db_path)
                prepared.add(db_path)
            set_site_config(
                db_path, "email.verification_enabled", PROFILE_ENV[profile]["EMAIL_VERIFICATION_ENABLED"]
            )
            result = subprocess.run(
                [
                    sys.executable,
                    str(DIFF / "replay.py"),
                    "--base-url",
                    f"http://127.0.0.1:{BACKEND_PORT}",
                    "--db",
                    str(db_path),
                    "--out",
                    str(out_dir / profile),
                    "--profile",
                    profile,
                ],
                cwd=str(ROOT),
            )
            if result.returncode != 0:
                status = result.returncode
                break
    finally:
        if (out_dir / "upstream.jsonl").exists():
            shutil.copyfile(out_dir / "upstream.jsonl", out_dir / "upstream-final.jsonl")
        if backend is not None:
            stop_backend(backend)
        upstream.terminate()
        log_handle.close()
        if not args.keep:
            shutil.rmtree(ROOT / "media", ignore_errors=True)
            shutil.rmtree(ROOT / "novel", ignore_errors=True)
    return status


if __name__ == "__main__":
    sys.exit(main())
