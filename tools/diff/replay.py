"""Scripted differential corpus.

Runs the same scripted user journey against any backend that implements the
0.0.1 contract and writes a normalized trace plus a SQLite snapshot. Run it once
against the Django reference and once against the Go rewrite on a fresh database
and compare the two output directories.

Usage:
    python tools/diff/replay.py --base-url http://127.0.0.1:8010 \
        --db .reftmp/ref.sqlite3 --out .reftmp/trace-ref --profile main
"""

from __future__ import annotations

import argparse
import json
import sys
import time
from contextlib import contextmanager
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from harness import (  # noqa: E402
    Client,
    Normalizer,
    epub_bytes,
    png_bytes,
    png_data_url,
    snapshot_database,
)

PASSWORD = "DiffPass123!@#"
NEW_PASSWORD = "DiffPass456!@#"
FORWARDED_IP = "198.51.100.7"
# Must match ADMIN_USERNAME / ADMIN_PASSWORD in tools/diff/run.py.
ADMIN_USERNAME = "admin"
ADMIN_PASSWORD = "DiffAdmin123!@#"
TERMINAL_GENERATION = {"succeeded", "failed", "canceled"}
TERMINAL_NOVEL = {"succeeded", "failed", "canceled"}


class Corpus:
    def __init__(self, base_url: str, profile: str) -> None:
        self.normalizer = Normalizer()
        self.client = Client(base_url, self.normalizer)
        self.profile = profile
        self.artifacts: dict[str, object] = {}

    # ── helpers ──

    def as_ip(self, ip: str) -> dict:
        """Route a request through the trusted X-Forwarded-For header."""
        return {"X-Forwarded-For": ip}

    def captcha_code(self) -> str:
        response = self.client.get("/api/auth/captcha", record=False)
        return response.json()["data"]["captcha_code"]

    def login(self, username: str, password: str, ip: str = FORWARDED_IP) -> int:
        response = self.client.post(
            "/api/auth/login",
            json_body={
                "username": username,
                "password": password,
                "captcha_code": self.captcha_code(),
            },
            headers=self.as_ip(ip),
            record=False,
        )
        return response.status

    def settle(self, path: str, *, terminal: set[str], timeout: float = 90.0, interval: float = 0.25) -> dict:
        """Poll an asynchronous resource until it reaches a terminal state.

        Generation and novel-visualisation work is executed by a worker in the
        target architecture and inline in the reference; polling makes the
        recorded steady state identical for both.
        """
        deadline = time.monotonic() + timeout
        data: dict = {}
        while time.monotonic() < deadline:
            response = self.client.get(path, record=False)
            if response.status != 200:
                return data
            data = response.json()["data"]
            if data.get("status") in terminal:
                return data
            time.sleep(interval)
        return data

    def record_terminal(self, data: dict) -> None:
        """Replace the last recorded envelope body with the settled resource.

        The create endpoints answer with whatever state the task happens to be in
        at the moment of the write (immediately terminal when work runs inline,
        still queued when a worker picks it up). The settled payload is the part
        the frontend actually consumes, so it is what the trace records.
        """
        if not self.client.trace:
            return
        body = self.client.trace[-1]["body"]
        if isinstance(body, dict) and "data" in body:
            body["data"] = self.normalizer.value(data)

    def generation_create(self, body: dict) -> dict:
        response = self.client.post("/api/generate/tasks", json_body=body)
        if response.status != 200:
            return response.json()
        payload = response.json()["data"]
        settled = self.settle(
            f"/api/generate/tasks/{payload['task_no']}", terminal=TERMINAL_GENERATION
        )
        self.record_terminal(settled or payload)
        return response.json()

    # ── phases ──

    def phase_public(self) -> None:
        with with_label(self.client, "public"):
            for path in ("/", "/healthz", "/workspace", "/pricing", "/login", "/register", "/forgot-password"):
                self.client.get(path)
            self.client.get("/admin")
            self.client.get("/admin/")
            self.client.get("/console")
            self.client.get("/console/")
            self.client.get("/static/css/main.css")
            self.client.get("/static/favicon.svg")
            self.client.get("/static/workspace/assets/workspace.js?v=x")
            self.client.get("/static/console/assets/console.js")
            self.client.get("/missing-page")

    def phase_auth(self) -> None:
        with with_label(self.client, "auth"):
            self.client.get("/api/auth/csrf")
            # A state-changing POST without the CSRF header must be refused by the
            # framework before the view runs.
            self.client.post(
                "/api/auth/login",
                json_body={"username": "csrf-probe", "password": "x", "captcha_code": "ZZZZ"},
                csrf=False,
            )
            self.client.patch("/api/account/settings", json_body={"nickname": "x"}, csrf=False)
            self.client.get("/api/account/summary")
            self.client.post("/api/auth/register/check", json_body={"username": "diff-user"})
            self.client.post("/api/auth/register/check", json_body={"email": "diff@example.com"})
            self.client.post("/api/auth/register/check", json_body={})
            self.client.post(
                "/api/auth/register",
                json_body={
                    "username": "taken",
                    "email": "taken@example.com",
                    "email_verification_code": "",
                    "password": PASSWORD,
                    "confirm_password": PASSWORD,
                },
            )
            self.client.post("/api/auth/register", json_body={"username": "taken", "email": "other@example.com"})
            self.client.post("/api/auth/register/check", json_body={"username": "taken", "email": "taken@example.com"})
            self.client.post(
                "/api/auth/register",
                json_body={
                    "username": "diff-user",
                    "email": "diff@example.com",
                    "email_verification_code": "",
                    "password": PASSWORD,
                    "confirm_password": PASSWORD,
                },
            )
            # Duplicate registration must be rejected.
            self.client.post(
                "/api/auth/register",
                json_body={
                    "username": "diff-user",
                    "email": "diff@example.com",
                    "email_verification_code": "",
                    "password": PASSWORD,
                    "confirm_password": PASSWORD,
                },
            )
            # Mismatched confirmation and a weak password are rejected by the
            # shared validators.
            self.client.post(
                "/api/auth/register",
                json_body={
                    "username": "diff-weak",
                    "email": "weak@example.com",
                    "email_verification_code": "",
                    "password": PASSWORD,
                    "confirm_password": "different",
                },
            )
            self.client.post(
                "/api/auth/register",
                json_body={
                    "username": "diff-weak",
                    "email": "weak@example.com",
                    "email_verification_code": "",
                    "password": "12345678",
                    "confirm_password": "12345678",
                },
            )
            self.client.get("/api/account/summary")
            self.client.get("/api/account/settings")
            if self.profile == "email":
                self.phase_email_codes()
                return
            # Wrong captcha leaves an anonymous session.
            self.client.post(
                "/api/auth/login",
                json_body={"username": "diff-user", "password": PASSWORD, "captcha_code": "ZZZZ"},
                headers=self.as_ip(FORWARDED_IP),
            )
            self.artifacts["login_status"] = self.login("diff-user", PASSWORD)
            self.client.get("/api/account/summary")
            self.client.get("/api/account/settings")
            self.client.patch(
                "/api/account/settings",
                json_body={
                    "nickname": "差异测试账号",
                    "email": "diff@example.com",
                    "avatar": "https://cdn.example.com/avatar.png",
                    "remark": "差分验证",
                },
            )
            self.client.patch("/api/account/settings", json_body={"nickname": ""})
            self.client.patch("/api/account/settings", json_body={"nickname": "差异测试账号", "email": "taken@example.com", "avatar": "not-a-url", "remark": ""})
            self.client.post(
                "/api/account/password",
                json_body={"current_password": "wrong", "new_password": NEW_PASSWORD, "confirm_password": NEW_PASSWORD},
            )
            self.client.post(
                "/api/account/password",
                json_body={"current_password": PASSWORD, "new_password": NEW_PASSWORD, "confirm_password": "mismatch"},
            )
            self.client.post(
                "/api/account/password",
                json_body={"current_password": PASSWORD, "new_password": "12345678", "confirm_password": "12345678"},
            )
            self.client.post(
                "/api/account/password",
                json_body={"current_password": PASSWORD, "new_password": NEW_PASSWORD, "confirm_password": NEW_PASSWORD},
            )
            # Session survives the password change, then re-login with the new one.
            self.client.get("/api/account/settings")
            self.client.post("/api/auth/logout")
            self.client.get("/api/account/summary")
            self.artifacts["relogin_status"] = self.login("diff-user", NEW_PASSWORD)

    def phase_email_codes(self) -> None:
        # SMTP is intentionally unconfigured in the diff environment, so the send
        # endpoint fails deterministically while still arming the rate limits.
        self.client.post(
            "/api/auth/register/email-code",
            json_body={"email": "code@example.com", "username": "code-user"},
        )
        self.client.post(
            "/api/auth/register/email-code",
            json_body={"email": "code@example.com", "username": "code-user"},
        )
        self.client.post(
            "/api/auth/register/email-code",
            json_body={"email": "code@example.com", "username": "diff-user"},
        )
        self.client.post("/api/auth/register/email-code", json_body={"email": "code@example.com"})
        self.client.post(
            "/api/auth/register",
            json_body={
                "username": "code-user",
                "email": "code@example.com",
                "email_verification_code": "000000",
                "password": PASSWORD,
                "confirm_password": PASSWORD,
            },
        )
        self.client.post(
            "/api/auth/register",
            json_body={
                "username": "code-user2",
                "email": "code2@example.com",
                "email_verification_code": "000000",
                "password": PASSWORD,
                "confirm_password": PASSWORD,
            },
        )

    def phase_generator(self) -> None:
        with with_label(self.client, "generator"):
            self.client.get("/api/chat/models")
            self.client.post("/api/chat", json_body={"prompt": ""})
            self.client.post("/api/chat", json_body={"prompt": "你好", "history": [{"role": "model", "parts": [{"text": "历史"}]}]})
            self.client.get("/api/generate/topics")
            self.client.post("/api/generate/topics", json_body={"title": "差分主题"})
            reference = self.client.post(
                "/api/generate/reference-images",
                json_body={"reference_image_name": "fruit.png", "reference_image_data_url": png_data_url()},
            )
            self.artifacts["reference_upload"] = reference.json()["data"]
            self.client.post("/api/generate/reference-images", json_body={"reference_image_data_url": ""})
            self.generation_create(
                {
                    "prompt": "一只橘猫在咖啡馆看报纸",
                    "style_code": "cinematic",
                    "size_code": "1024x1024",
                    "quality_code": "auto",
                    "client_request_id": "diff-req-1",
                    "wait_seconds": 10,
                }
            )
            # Same client_request_id returns the existing task untouched.
            self.generation_create(
                {
                    "prompt": "一只橘猫在咖啡馆看报纸",
                    "client_request_id": "diff-req-1",
                }
            )
            for body in (
                {"prompt": "尺寸", "size_code": "1537x864"},
                {"prompt": "尺寸", "size_code": "2048x512"},
                {"prompt": "尺寸", "size_code": "640x640"},
                {"prompt": "尺寸", "size_code": "2160x1216"},
                {"prompt": "质量", "quality_code": "ultra"},
                {"prompt": "风格", "style_code": "nope"},
                {"prompt": "模式", "generation_mode": "nope"},
                {"prompt": "图生图缺少参考图", "generation_mode": "image_to_image"},
                {"prompt": "太长" + "x" * 4001},
            ):
                self.client.post("/api/generate/tasks", json_body=body)
            self.generation_create(
                {
                    "prompt": "2K 自定义比例海报",
                    "size_code": "2048x1152",
                    "quality_code": "high",
                    "wait_seconds": 10,
                }
            )
            self.generation_create(
                {
                    "prompt": "最小尺寸与低质量",
                    "size_code": "1024x1024",
                    "quality_code": "low",
                    "wait_seconds": 10,
                }
            )
            self.generation_create(
                {
                    "prompt": "参考原图重绘成电商海报",
                    "generation_mode": "image_to_image",
                    "reference_image_name": "fruit.png",
                    "reference_image_data_url": png_data_url(),
                    "wait_seconds": 10,
                }
            )
            self.generation_create(
                {
                    "prompt": "使用上传凭证重绘",
                    "generation_mode": "image_to_image",
                    "reference_image_name": "fruit.png",
                    "reference_upload_token": self.artifacts["reference_upload"]["reference_upload_token"],
                    "wait_seconds": 10,
                }
            )
            self.client.post(
                "/api/generate/tasks",
                json_body={
                    "prompt": "伪造的凭证",
                    "generation_mode": "image_to_image",
                    "reference_upload_token": "bogus-token",
                },
            )
            # Risk: sensitive keyword and prompt length guard.
            self.client.post("/api/generate/tasks", json_body={"prompt": "包含禁止词A的内容"})
            self.client.post("/api/generate/tasks", json_body={"prompt": "   "})
            self.client.get("/api/generate/tasks?page=1&page_size=10")
            self.client.get("/api/generate/tasks?q=橘猫")
            self.client.get("/api/generate/tasks?page_size=999")
            listing = self.client.get("/api/generate/tasks?page=1&page_size=5")
            first = listing.json()["data"]["items"][0]
            self.client.get(f"/api/generate/tasks/{first['task_no']}")
            self.client.get("/api/generate/tasks/NOSUCHTASK")
            self.client.post(f"/api/generate/tasks/{first['task_no']}/cancel")
            self.artifacts["deleted_task_no"] = listing.json()["data"]["items"][-1]["task_no"]

    def phase_risk(self) -> None:
        with with_label(self.client, "risk"):
            # Blocked IP from the fixture row.
            self.client.get("/healthz", headers=self.as_ip("203.0.113.9"))
            self.client.get("/api/account/settings", headers=self.as_ip("203.0.113.9"))
            # Failed login counter and lock (RISK_LOGIN_FAIL_LIMIT=2 in the diff env).
            for _ in range(3):
                self.client.post(
                    "/api/auth/login",
                    json_body={"username": "diff-user", "password": "wrong-password", "captcha_code": self.captcha_code()},
                    headers=self.as_ip("198.51.100.8"),
                )
            self.client.post(
                "/api/auth/login",
                json_body={"username": "diff-user", "password": NEW_PASSWORD, "captcha_code": self.captcha_code()},
                headers=self.as_ip("198.51.100.8"),
            )
            # Task frequency limit: the generator phase already consumed the
            # window, so the first probe passes and the second is refused. The
            # probes go through generation_create so the accepted one is recorded
            # in its settled state, like every other create in this corpus.
            for _ in range(2):
                self.generation_create({"prompt": "频率限制探测"})

    def phase_media(self) -> None:
        with with_label(self.client, "media"):
            self.client.get("/api/images")
            self.client.get("/api/images?status=available")
            self.client.get("/api/images?keyword=橘猫")
            self.client.get("/api/images/1")
            self.client.get("/api/images/999999")
            download = self.client.post("/api/images/1/download-url")
            self.artifacts["download_url"] = download.json()["data"]["url"]
            self.artifacts["preview_url"] = download.json()["data"]["preview_url"]
            self.client.get(self.artifacts["download_url"], csrf=False)
            self.client.get(self.artifacts["preview_url"], csrf=False)
            self.client.get("/downloads/bogus-token/", csrf=False)
            self.upload_reference_session()
            self.upload_epub_session()
            self.client.get("/api/uploads/sessions/nonexistent")
            self.client.delete("/api/uploads/sessions/nonexistent")
            self.client.post("/api/uploads/sessions", json_body={})
            self.client.post(
                "/api/uploads/sessions",
                json_body={"kind": "nope", "filename": "x.bin", "total_size": 10},
            )
            self.client.post(
                "/api/uploads/sessions",
                json_body={"kind": "generation_reference_image", "filename": "x.png", "total_size": 0},
            )
            self.client.post(
                "/api/uploads/sessions",
                json_body={"kind": "generation_reference_image", "filename": "x.png", "total_size": 999_999_999},
            )

    def _upload(self, *, kind: str, filename: str, content_type: str, content: bytes, client_upload_id: str) -> dict:
        created = self.client.post(
            "/api/uploads/sessions",
            json_body={
                "kind": kind,
                "filename": filename,
                "content_type": content_type,
                "total_size": len(content),
                "client_upload_id": client_upload_id,
            },
        )
        session = created.json()["data"]
        self.client.get(f"/api/uploads/sessions/{session['upload_id']}")
        # Chunk size is 64 bytes in the diff environment so small fixtures split.
        for index in range(session["total_chunks"]):
            start = index * session["chunk_size"]
            chunk = content[start : start + session["chunk_size"]]
            self.client.put(
                f"/api/uploads/sessions/{session['upload_id']}/chunks/{index}",
                data=chunk,
                headers={"Content-Type": "application/octet-stream"},
            )
        # Re-uploading a chunk is idempotent.
        if session["total_chunks"]:
            self.client.put(
                f"/api/uploads/sessions/{session['upload_id']}/chunks/0",
                data=content[: session["chunk_size"]],
                headers={"Content-Type": "application/octet-stream"},
            )
            self.client.put(
                f"/api/uploads/sessions/{session['upload_id']}/chunks/999",
                data=b"x",
                headers={"Content-Type": "application/octet-stream"},
            )
        completed = self.client.post(f"/api/uploads/sessions/{session['upload_id']}/complete")
        self.client.post(f"/api/uploads/sessions/{session['upload_id']}/complete")
        return completed.json()["data"]

    def upload_reference_session(self) -> None:
        content = png_bytes(width=32, height=32)
        session = self._upload(
            kind="generation_reference_image",
            filename="ref.png",
            content_type="image/png",
            content=content,
            client_upload_id="diff-ref-1",
        )
        self.artifacts["ref_session"] = session
        # Resuming an existing session returns the same upload id.
        resumed = self.client.post(
            "/api/uploads/sessions",
            json_body={
                "kind": "generation_reference_image",
                "filename": "ref.png",
                "content_type": "image/png",
                "total_size": len(content),
                "client_upload_id": "diff-ref-1",
            },
        )
        self.artifacts["ref_resume"] = resumed.json()["data"]

    def upload_epub_session(self) -> None:
        content = epub_bytes()
        session = self._upload(
            kind="novel_epub",
            filename="uploaded-story.epub",
            content_type="application/epub+zip",
            content=content,
            client_upload_id="diff-epub-1",
        )
        self.artifacts["epub_session"] = session
        # Missing-chunk completion must fail without consuming the session.
        partial = self.client.post(
            "/api/uploads/sessions",
            json_body={
                "kind": "novel_epub",
                "filename": "incomplete.epub",
                "content_type": "application/epub+zip",
                "total_size": len(content),
                "client_upload_id": "diff-epub-2",
            },
        ).json()["data"]
        self.client.post(f"/api/uploads/sessions/{partial['upload_id']}/complete")
        self.client.delete(f"/api/uploads/sessions/{partial['upload_id']}")

    def phase_novel(self) -> None:
        with with_label(self.client, "novel"):
            self.client.get("/api/novelviz/books")
            self.client.get("/api/novelviz/tasks")
            self.client.post("/api/novelviz/tasks", json_body={})
            self.client.post("/api/novelviz/tasks", json_body={"book_filename": "missing.epub"})
            self.client.post(
                "/api/novelviz/tasks",
                json_body={"book_filename": "diff-fixture.epub", "scene_mode": "targeted", "target_plot": "短"},
            )
            self.client.post(
                "/api/novelviz/tasks",
                json_body={"book_filename": "../etc/passwd"},
            )
            created = self.client.post(
                "/api/novelviz/tasks",
                json_body={"book_filename": "diff-fixture.epub", "scene_mode": "auto"},
            )
            task_id = created.json()["data"]["id"]
            settled = self.settle(f"/api/novelviz/tasks/{task_id}", terminal=TERMINAL_NOVEL)
            self.record_terminal(settled or created.json()["data"])
            self.artifacts["novel_task"] = settled
            detail = self.client.get(f"/api/novelviz/tasks/{task_id}")
            self.artifacts["novel_detail"] = detail.json()["data"]
            self.client.get(f"/api/novelviz/tasks/{task_id}/events?last_event_id=0")
            self.client.get(f"/api/novelviz/tasks/{task_id}/events?last_event_id=999")
            self.client.get("/api/novelviz/tasks/999999")
            self.client.get("/api/novelviz/tasks/999999/events")
            scene_images = detail.json()["data"]["images"]
            if scene_images:
                image_id = scene_images[0]["id"]
                self.client.get(f"/api/novelviz/tasks/{task_id}/images/{image_id}.png", csrf=False)
                self.client.get(f"/api/novelviz/tasks/{task_id}/images/{image_id}.png?preview=1", csrf=False)
                self.client.get(f"/api/novelviz/tasks/{task_id}/images/{image_id}.png?download=1", csrf=False)
            self.client.post(f"/api/novelviz/tasks/{task_id}/retry")
            self.client.post("/api/novelviz/tasks/999999/retry")
            uploaded_id = self.artifacts["epub_session"]["result"]["uploaded_book_id"]
            uploaded_task = self.client.post(
                "/api/novelviz/tasks",
                json_body={
                    "book_source": "uploaded",
                    "book_filename": "uploaded-story.epub",
                    "uploaded_book_id": uploaded_id,
                    "scene_mode": "targeted",
                    "target_plot": "主角第一次进入星港",
                },
            )
            uploaded_task_id = uploaded_task.json()["data"]["id"]
            uploaded_settled = self.settle(f"/api/novelviz/tasks/{uploaded_task_id}", terminal=TERMINAL_NOVEL)
            # record_terminal rewrites the last recorded step, so it has to run
            # before the detail GET below.
            self.record_terminal(uploaded_settled or uploaded_task.json()["data"])
            detail = self.client.get(f"/api/novelviz/tasks/{uploaded_task_id}")
            self.artifacts["uploaded_novel_task"] = uploaded_settled
            self.client.delete(f"/api/novelviz/tasks/{uploaded_task_id}")
            self.client.get("/api/novelviz/tasks")

    def phase_console(self) -> None:
        with with_label(self.client, "console"):
            # A normal account must be refused before the operator signs in.
            self.client.get("/console/api/dashboard/")
            self.client.get("/console/api/users/")
            self.client.post("/api/auth/logout")
            self.client.post(
                "/api/auth/login",
                json_body={
                    "username": ADMIN_USERNAME,
                    "password": ADMIN_PASSWORD,
                    "captcha_code": self.captcha_code(),
                },
                headers=self.as_ip(FORWARDED_IP),
            )
            self.client.get("/console/")
            # Discover the corpus user's primary key instead of assuming it.
            users = self.client.get("/console/api/users/?per_page=100", record=False).json()["data"]["items"]
            user_id = next(item["id"] for item in users if item["username"] == "diff-user")
            self.artifacts["console_user_id"] = user_id
            self.client.get("/console/api/dashboard/")
            self.client.get("/console/api/users/")
            self.client.get("/console/api/users/?keyword=diff&per_page=5")
            self.client.get("/console/api/users/?status=active&role=user")
            self.client.get("/console/api/users/1/")
            self.client.get("/console/api/users/999999/")
            self.client.get(f"/console/api/users/{user_id}/")
            self.client.post(
                f"/console/api/users/{user_id}/quota/",
                json_body={"daily_quota": 9, "available_quota": 7, "remark": "差分调整"},
            )
            self.client.post(f"/console/api/users/{user_id}/quota/", json_body={})
            self.client.post(f"/console/api/users/{user_id}/quota/", json_body={"available_quota": -5})
            self.client.post(f"/console/api/users/{user_id}/quota/", json_body={"daily_quota": -1})
            self.client.get("/console/api/tasks/")
            self.client.get("/console/api/tasks/?status=failed&keyword=diff")
            self.client.get("/console/api/config/")
            self.client.get("/console/api/config/?category=risk")
            self.client.get("/console/api/config/risk.login_fail_limit/")
            self.client.put("/console/api/config/risk.login_fail_limit/", json_body={"value": "4"})
            self.client.get("/console/api/config/risk.login_fail_limit/")
            self.client.put("/console/api/config/", json_body={})
            self.client.put("/console/api/config/missing.key/", json_body={"value": "1"})
            self.client.get("/console/api/config/missing.key/")
            self.client.put(
                "/console/api/config/batch/",
                json_body={"items": [{"key": "site.brand_tagline", "value": "差分标语"}, {"key": "risk.task_limit_per_window", "value": "3"}]},
            )
            self.client.put("/console/api/config/batch/", json_body={"items": []})
            self.client.get("/console/api/image-channels/")
            created = self.client.post(
                "/console/api/image-channels/",
                json_body={
                    "name": "差分渠道",
                    "channel_code": "diff-channel",
                    "channel_type": "image",
                    "provider_name": "Diff",
                    "base_url": "https://images.example.com/v1",
                    "api_path": "/images/generations",
                    "model_name": "diff-model",
                    "timeout_seconds": 42,
                    "max_retries": 2,
                    "priority": 5,
                    "api_key": "diff-secret-key",
                },
            )
            created_data = created.json()["data"]
            self.artifacts["channel_id"] = created_data["id"]
            self.client.get(f"/console/api/image-channels/{created_data['id']}/")
            self.client.get("/console/api/image-channels/999999/")
            self.client.put(
                f"/console/api/image-channels/{created_data['id']}/",
                json_body={"name": "差分渠道改名", "is_active": False, "max_retries": 4, "api_key": "rotated-key"},
            )
            self.client.post(
                "/console/api/image-channels/",
                json_body={
                    "name": "重复",
                    "channel_code": "diff-channel",
                    "base_url": "https://x.example.com",
                    "model_name": "m",
                },
            )
            self.client.post("/console/api/image-channels/", json_body={"name": "缺字段"})
            self.client.delete(f"/console/api/image-channels/{created_data['id']}/")
            self.client.delete("/console/api/image-channels/1/")

    def phase_ssr_authenticated(self) -> None:
        with with_label(self.client, "ssr"):
            self.client.get("/account")
            self.client.get("/account/security")
            self.client.get("/account/history")
            self.client.get("/account/history/GTESTMISSING")
            self.client.get("/workspace")
            self.client.get("/console/")
            # Anonymous view of the same pages must redirect to the workspace login.
            self.client.post("/api/auth/logout")
            self.client.get("/account")
            self.client.get("/account/security")
            self.client.get("/account/history")
            self.client.get("/console/")
            self.client.get("/console/api/dashboard/")

    def phase_settle(self) -> None:
        """Wait for every background task to reach a terminal state.

        The snapshot is compared at the end of the corpus, so no task may still be
        queued or running when it is taken. Unrecorded so it adds no trace noise.
        """
        deadline = time.monotonic() + 120
        while time.monotonic() < deadline:
            pending = False
            for page in (1, 2):
                listing = self.client.get(
                    f"/api/generate/tasks?page={page}&page_size=48", record=False
                ).json()["data"]
                if any(item["status"] in ("queued", "running") for item in listing["items"]):
                    pending = True
                if not listing["pagination"]["has_next"]:
                    break
            for item in self.client.get("/api/novelviz/tasks", record=False).json()["data"]:
                if item["status"] in ("queued", "running"):
                    pending = True
            if not pending:
                return
            time.sleep(0.25)

    def run(self) -> dict:
        phases = [
            self.phase_public,
            self.phase_auth,
            self.phase_generator,
            self.phase_risk,
            self.phase_media,
            self.phase_novel,
        ]
        if self.profile == "main":
            # Settling runs while the corpus account is still signed in and before
            # the console switches the session over to the operator.
            phases.append(self.phase_settle)
        phases += [self.phase_console, self.phase_ssr_authenticated]
        for phase in phases:
            if self.profile == "email" and phase not in (self.phase_public, self.phase_auth):
                continue
            if self.profile == "upstream" and phase not in (
                self.phase_public,
                self.phase_auth,
                self.phase_generator,
            ):
                continue
            phase()
        return {"trace": self.client.trace, "artifacts": self.normalizer.value(self.artifacts)}


@contextmanager
def with_label(client: Client, tag: str):
    client.label(tag)
    try:
        yield
    finally:
        if client.label_stack:
            client.label_stack.pop()


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--db", required=True)
    parser.add_argument("--out", required=True)
    parser.add_argument("--profile", default="main", choices=("main", "email", "upstream"))
    args = parser.parse_args()

    out_dir = Path(args.out)
    out_dir.mkdir(parents=True, exist_ok=True)
    corpus = Corpus(args.base_url, args.profile)
    if args.profile == "email":
        # Registration over HTTP is only exercised with verification disabled, so
        # this profile covers the code endpoints on their own.
        corpus.phase_public()
        with with_label(corpus.client, "email"):
            corpus.phase_email_codes()
        result = {
            "trace": corpus.client.trace,
            "artifacts": corpus.normalizer.value(corpus.artifacts),
        }
    else:
        result = corpus.run()

    (out_dir / "trace.json").write_text(
        json.dumps(result, ensure_ascii=False, indent=1, sort_keys=True), encoding="utf-8"
    )
    snapshot = snapshot_database(Path(args.db), corpus.normalizer)
    (out_dir / "snapshot.json").write_text(
        json.dumps(snapshot, ensure_ascii=False, indent=1, sort_keys=True), encoding="utf-8"
    )
    print(f"recorded {len(result['trace'])} steps -> {out_dir}")


if __name__ == "__main__":
    main()
