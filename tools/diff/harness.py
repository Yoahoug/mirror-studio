"""Shared differential-verification harness (development asset, never shipped).

Replays a scripted corpus against a backend over HTTP and records a
normalized trace: every HTTP response plus a snapshot of the SQLite tables the
corpus touched. Two traces produced from a fresh database are expected to be
byte-identical; volatile values (timestamps, random ids, signatures) are
replaced by stable placeholders so the comparison stays meaningful.

Standard library only, so it runs with any Python 3.11+ interpreter.
"""

from __future__ import annotations

import base64
import hashlib
import http.cookiejar
import json
import re
import sqlite3
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

RECORDED_HEADERS = (
    "content-type",
    "location",
    "cache-control",
    "content-disposition",
    "x-content-type-options",
    "x-frame-options",
    "x-robots-tag",
    "pragma",
    "x-accel-buffering",
    "allow",
)

ISO_RE = re.compile(
    r"\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?"
)
TASK_NO_RE = re.compile(r"[GR]\d{14}[0-9A-F]{8}")
LEDGER_NO_RE = re.compile(r"L\d{14}[0-9A-F]{8}")
HEX32_RE = re.compile(r"\b[0-9a-f]{32}\b")
HEX16_RE = re.compile(r"\b[0-9a-f]{16}\b")
HEX64_RE = re.compile(r"\b[0-9a-f]{64}\b")
DOWNLOAD_TOKEN_RE = re.compile(r"/downloads/[^/?#]+")
MOCK_REQUEST_ID_RE = re.compile(r"mock-[A-Za-z0-9:._-]+")
REFERENCE_TOKEN_RE = re.compile(r"\b[A-Za-z0-9_\-=]{6,}:1:[A-Za-z0-9_\-=:]+")
SVG_DATA_URI_RE = re.compile(r"data:image/svg\+xml;base64,[A-Za-z0-9+/=]+")
DATA_CAPTCHA_ATTR_RE = re.compile(r'data-captcha="[^"]*"')
DATA_CSRF_ATTR_RE = re.compile(r'data-csrf="[^"]*"')
# The in-app release notes payload is version metadata sourced from CHANGELOG.md;
# the markup around it is compared, the payload itself is not.
RELEASE_DATA_RE = re.compile(
    r'<script id="workspace-release-data" type="application/json">.*?</script>', re.S
)
# The stored reference-image object key embeds a per-run task number
# (users/<uid>/tasks/R<timestamp><random>/reference.png), not implementation output.
REFERENCE_OBJECT_KEY_RE = re.compile(r"/tasks/R\d{8,}/")
# Tail of a timestamped signed value: ":1<base62 timestamp>:<base64url signature>".
# The signature covers the payload, which embeds per-run values, so it differs
# between two correct runs by construction. The payload itself stays comparable.
SIGNED_TAIL_RE = re.compile(r":1[A-Za-z0-9]{3,}:[A-Za-z0-9_\-=]+")
CSRF_INPUT_RE = re.compile(r'name="csrfmiddlewaretoken" value="[^"]*"')
# Mirrors the expression the SPA uses to recognise a CSRF rejection.
CSRF_FAILURE_RE = re.compile(r"csrf|CSRF|CSRF验证失败|CSRF 验证失败|403")

# JSON keys whose value is entropy by construction; masked whatever the format.
# Site-config rows whose value the rewrite sources from CHANGELOG.md / VERSION
# instead of from hardcoded settings. The keys must still exist and be compared;
# only the payload is a mandated difference.
MANDATED_CONFIG_KEYS = {
    "site.workspace_release_version",
    "site.workspace_release_title",
    "site.workspace_release_notes",
    "site.workspace_release_published_at",
}

MASK_JSON_KEYS = {
    "csrf_token",
    "captcha_code",
    "captcha_svg",
    "reference_upload_token",
    "token",
    # The stored image size depends on which image encoder produced the bytes
    # (Pillow in the reference, image/png in the rewrite). It is masked in the
    # snapshot table already; the API surfaces the same column.
    "file_size",
}

# Table columns holding values that differ between two correct runs.
VOLATILE_COLUMNS = {
    "created_at",
    "updated_at",
    "submitted_at",
    "started_at",
    "finished_at",
    "lease_expires_at",
    "expires_at",
    "deleted_at",
    "completed_at",
    "last_event_at",
}

# Tables compared by the snapshot. These are the application tables shared by the
# Django reference and the Go rewrite; framework bookkeeping tables
# (django_*, auth_*, sqlite_sequence) are intentionally excluded because the two
# stacks store session/migration state differently. Session behaviour is compared
# through the HTTP trace instead.
SNAPSHOT_TABLES = (
    "users_user",
    "users_userprofile",
    "billing_quotaaccount",
    "billing_quotaledger",
    "generator_imagechannel",
    "generator_generationtopic",
    "generator_generatetask",
    "mediahub_generatedimage",
    "mediahub_uploadsession",
    "novelviz_noveluploadedbook",
    "novelviz_novelvisualizationtask",
    "novelviz_novelsceneimage",
    "risk_ipaccessrule",
    "risk_riskevent",
    "risk_sensitivewordrule",
    "audit_callbackeventlog",
    "core_siteconfig",
)


# Columns that cannot match byte-for-byte between two runtimes: generated image
# bytes come out of different encoders (Pillow vs Go's image/jpeg), so only their
# dimensions are part of the contract.
TABLE_COLUMN_MASKS = {
    "mediahub_generatedimage": {"sha256": "<sha256>", "file_size": "<size>"},
    "novelviz_noveluploadedbook": {"sha256": "<sha256>"},
    "core_siteconfig": {"id": "<config-id>"},
    # Password hashes and Fernet ciphertexts are salted/randomised by
    # construction, so two correct runs never produce the same bytes. Login and
    # channel dispatch are verified through the HTTP trace instead.
    "users_user": {"password": "<password-hash>"},
    "generator_imagechannel": {"api_key_encrypted": "<ciphertext>"},
}

# Configuration rows whose feature the rewrite deletes outright (the 不移植
# list), so neither stack is expected to agree on them.
NON_PORTED_CONFIG_KEYS = {
    "site.release_push_key",
    "site.release_push_secret",
}


class Normalizer:
    """Replaces entropy with stable, order-of-first-seen placeholders."""

    def __init__(self) -> None:
        self._maps: dict[str, dict[str, str]] = {}

    def placeholder(self, kind: str, value: str) -> str:
        mapping = self._maps.setdefault(kind, {})
        if value not in mapping:
            mapping[value] = f"<{kind}:{len(mapping) + 1}>"
        return mapping[value]

    def apply(self, text: str) -> str:
        if not text:
            return text
        text = SVG_DATA_URI_RE.sub("<svg-data-uri>", text)
        text = DATA_CAPTCHA_ATTR_RE.sub('data-captcha="<captcha>"', text)
        text = DATA_CSRF_ATTR_RE.sub('data-csrf="<csrf>"', text)
        text = CSRF_INPUT_RE.sub('name="csrfmiddlewaretoken" value="<csrf>"', text)
        text = ISO_RE.sub("<ts>", text)
        text = DOWNLOAD_TOKEN_RE.sub("/downloads/<token>", text)
        for match in set(TASK_NO_RE.findall(text)):
            text = text.replace(match, self.placeholder("taskno", match))
        for match in set(LEDGER_NO_RE.findall(text)):
            text = text.replace(match, self.placeholder("ledgerno", match))
        for match in set(HEX64_RE.findall(text)):
            text = text.replace(match, self.placeholder("sha256", match))
        for match in set(HEX32_RE.findall(text)):
            text = text.replace(match, self.placeholder("hex32", match))
        for match in set(HEX16_RE.findall(text)):
            text = text.replace(match, self.placeholder("hex16", match))
        text = MOCK_REQUEST_ID_RE.sub("mock-<taskno>", text)
        text = REFERENCE_OBJECT_KEY_RE.sub("/tasks/R<taskno>/", text)
        text = SIGNED_TAIL_RE.sub(":1<ts>:<sig>", text)
        text = RELEASE_DATA_RE.sub(
            '<script id="workspace-release-data" type="application/json"><release></script>',
            text,
        )
        return text

    def value(self, value, key: str = ""):
        if key in MASK_JSON_KEYS and value:
            return f"<{key}>"
        if isinstance(value, str):
            return self.apply(value)
        if isinstance(value, list):
            # Configuration rows for features the rewrite deletes outright are
            # dropped from both sides rather than compared.
            return [
                self.value(item)
                for item in value
                if not (
                    isinstance(item, dict)
                    and item.get("key") in NON_PORTED_CONFIG_KEYS
                    and "is_secret" in item
                )
            ]
        if isinstance(value, dict):
            cleaned = {name: self.value(item, name) for name, item in value.items()}
            if cleaned.get("key") in MANDATED_CONFIG_KEYS:
                cleaned["value"] = "<mandated>"
            if {"key", "is_secret", "category"} <= cleaned.keys():
                # A site-config row: its id is an autoincrement value from a seed
                # sequence the rewrite shortens by the rows it deletes. The SPA
                # addresses these rows by `key`, never by id.
                cleaned["id"] = "<config-id>"
            return cleaned
        return value


class Response:
    def __init__(self, status: int, headers, body: bytes, url: str) -> None:
        self.status = status
        self.headers = headers
        self.body = body
        self.url = url

    @property
    def text(self) -> str:
        return self.body.decode("utf-8", "replace")

    def json(self):
        return json.loads(self.text)


class Client:
    """Cookie-aware HTTP client with a recorded request/response trace."""

    def __init__(self, base_url: str, normalizer: Normalizer) -> None:
        self.base_url = base_url.rstrip("/")
        self.normalizer = normalizer
        self.jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(
            urllib.request.HTTPCookieProcessor(self.jar),
            NoRedirect(),
        )
        self.trace: list[dict] = []
        self.label_stack: list[str] = []

    # ── trace bookkeeping ──

    def label(self, label: str) -> None:
        self.label_stack.append(label)

    def step(self, name: str) -> None:
        self.current_step = name

    # ── cookies ──

    def cookie_value(self, name: str) -> str:
        for cookie in self.jar:
            if cookie.name == name:
                return cookie.value or ""
        return ""

    def cookie_names(self) -> list[str]:
        return sorted(cookie.name for cookie in self.jar)

    # ── requests ──

    def request(
        self,
        method: str,
        path: str,
        *,
        json_body=None,
        data: bytes | None = None,
        headers: dict | None = None,
        record: bool = True,
        csrf: bool = True,
    ) -> Response:
        url = path if path.startswith("http") else f"{self.base_url}{path}"
        # Percent-encode non-ASCII (the corpus uses Chinese keywords) while leaving
        # the query structure untouched, matching what a browser would send.
        url = urllib.parse.quote(url, safe="/?&=%:+#@!$'()*,;[]~-._")
        body = data
        actual_headers = {"User-Agent": "mirror-diff/1.0"}
        if json_body is not None:
            body = json.dumps(json_body, ensure_ascii=False).encode("utf-8")
            actual_headers["Content-Type"] = "application/json"
        if csrf:
            token = self.cookie_value("csrftoken")
            if token:
                actual_headers["X-CSRFToken"] = token
        if headers:
            actual_headers.update(headers)
        request = urllib.request.Request(url, data=body, headers=actual_headers, method=method)
        try:
            with self.opener.open(request, timeout=120) as response:
                status = response.status
                raw_headers = response.headers
                content = response.read()
        except urllib.error.HTTPError as exc:
            status = exc.code
            raw_headers = exc.headers
            content = exc.read()
        except urllib.error.URLError as exc:  # pragma: no cover - transport failure
            raise RuntimeError(f"{method} {path} failed: {exc}") from exc

        response = Response(status, raw_headers, content, url)
        if record:
            self.trace.append(self._record(method, path, response))
        return response

    def get(self, path: str, **kwargs) -> Response:
        return self.request("GET", path, **kwargs)

    def post(self, path: str, **kwargs) -> Response:
        return self.request("POST", path, **kwargs)

    def put(self, path: str, **kwargs) -> Response:
        return self.request("PUT", path, **kwargs)

    def patch(self, path: str, **kwargs) -> Response:
        return self.request("PATCH", path, **kwargs)

    def delete(self, path: str, **kwargs) -> Response:
        return self.request("DELETE", path, **kwargs)

    # ── recording ──

    def _record(self, method: str, path: str, response: Response) -> dict:
        entry = {
            "label": self.label_stack[-1] if self.label_stack else "",
            "request": f"{method} {self.normalizer.apply(path)}",
            "status": response.status,
            "headers": self._record_headers(response),
            "body": self._record_body(response),
        }
        return entry

    def _record_headers(self, response: Response) -> dict:
        recorded = {}
        for name in RECORDED_HEADERS:
            value = response.headers.get(name)
            if value:
                recorded[name] = self.normalizer.apply(value)
        set_cookie = response.headers.get_all("Set-Cookie") or []
        cookies = []
        for raw in set_cookie:
            parts = [part.strip() for part in raw.split(";")]
            name = parts[0].split("=", 1)[0]
            attrs = sorted(
                part for part in parts[1:] if part and not part.lower().startswith("expires=")
            )
            cookies.append({"name": name, "attrs": attrs})
        if cookies:
            recorded["set-cookie"] = sorted(cookies, key=lambda item: item["name"])
        return recorded

    def _record_body(self, response: Response) -> object:
        content_type = (response.headers.get("content-type") or "").split(";")[0].strip()
        if content_type == "application/json":
            try:
                return self.normalizer.value(response.json())
            except json.JSONDecodeError:
                return {"__raw__": self.normalizer.apply(response.text[:2000])}
        if content_type.startswith("image/"):
            width, height = image_dimensions(response.body)
            return {
                "__image__": True,
                "content_type": content_type,
                "width": width,
                "height": height,
            }
        if content_type == "text/event-stream":
            return {"__sse__": self.normalizer.apply(response.text[:4000])}
        if content_type.startswith("text/html"):
            if response.status >= 400:
                # Framework error pages (Django's debug 404/403/500) are not part of
                # the product surface; only the status and the property the SPA
                # actually keys on are recorded.
                return {
                    "__html_error__": True,
                    "csrf_regex": bool(CSRF_FAILURE_RE.search(response.text)),
                }
            return {"__html__": self.normalizer.apply(response.text[:4000])}
        return {"__raw__": self.normalizer.apply(response.text[:2000])}


class NoRedirect(urllib.request.HTTPRedirectHandler):
    """Redirects are part of the contract, so never follow them silently."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):  # noqa: D102
        return None


def hash_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def png_bytes(width: int = 96, height: int = 96, color=(47, 107, 255)) -> bytes:
    """Deterministic minimal PNG so both backends fingerprint the same bytes."""
    import struct
    import zlib

    raw = b""
    for _ in range(height):
        raw += b"\x00" + bytes(color) * width

    def chunk(tag: bytes, payload: bytes) -> bytes:
        return (
            struct.pack(">I", len(payload))
            + tag
            + payload
            + struct.pack(">I", zlib.crc32(tag + payload) & 0xFFFFFFFF)
        )

    ihdr = struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0)
    return (
        b"\x89PNG\r\n\x1a\n"
        + chunk(b"IHDR", ihdr)
        + chunk(b"IDAT", zlib.compress(raw, 9))
        + chunk(b"IEND", b"")
    )


def png_data_url(**kwargs) -> str:
    return "data:image/png;base64," + base64.b64encode(png_bytes(**kwargs)).decode("ascii")


def epub_bytes(body: str = "第一章 星港\n舱门缓缓打开，星光涌入。\n第二章 归途\n他转身走向长廊。") -> bytes:
    """Deterministic minimal EPUB containing one XHTML document."""
    import zipfile

    from io import BytesIO

    buffer = BytesIO()
    with zipfile.ZipFile(buffer, "w", zipfile.ZIP_DEFLATED) as archive:
        archive.writestr("mimetype", "application/epub+zip")
        archive.writestr(
            "META-INF/container.xml",
            '<?xml version="1.0"?><container version="1.0" '
            'xmlns="urn:oasis:names:tc:opendocument:xmlns:container">'
            '<rootfiles><rootfile full-path="OEBPS/content.opf" '
            'media-type="application/oebps-package+xml"/></rootfiles></container>',
        )
        archive.writestr(
            "OEBPS/content.opf",
            '<?xml version="1.0" encoding="utf-8"?>'
            '<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="id">'
            '<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">'
            "<dc:title>Diff Fixture</dc:title><dc:identifier id=\"id\">diff-fixture</dc:identifier>"
            "<dc:language>zh</dc:language></metadata>"
            '<manifest><item id="c1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>'
            '<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/></manifest>'
            '<spine toc="ncx"><itemref idref="c1"/></spine></package>',
        )
        archive.writestr(
            "OEBPS/chapter1.xhtml",
            '<?xml version="1.0" encoding="utf-8"?>'
            '<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title>'
            "<style>p{color:red}</style></head><body>"
            f"{''.join(f'<p>{line}</p>' for line in body.splitlines())}"
            "</body></html>",
        )
        archive.writestr(
            "OEBPS/toc.ncx",
            '<?xml version="1.0"?><ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">'
            '<head><meta name="dtb:uid" content="diff-fixture"/></head><docTitle><text>Diff</text></docTitle>'
            '<navMap><navPoint id="n1"><navLabel><text>Chapter</text></navLabel>'
            '<content src="chapter1.xhtml"/></navPoint></navMap></ncx>',
        )
    return buffer.getvalue()


def image_dimensions(body: bytes) -> tuple[int, int]:
    """Read PNG/JPEG dimensions without any third-party image library."""
    if body[:8] == b"\x89PNG\r\n\x1a\n" and body[12:16] == b"IHDR":
        return int.from_bytes(body[16:20], "big"), int.from_bytes(body[20:24], "big")
    if body[:3] == b"\xff\xd8\xff":
        index = 2
        while index + 9 < len(body):
            if body[index] != 0xFF:
                index += 1
                continue
            marker = body[index + 1]
            if marker in (0xD8, 0x01) or 0xD0 <= marker <= 0xD7:
                index += 2
                continue
            length = int.from_bytes(body[index + 2 : index + 4], "big")
            if marker in (0xC0, 0xC1, 0xC2, 0xC3, 0xC5, 0xC6, 0xC7, 0xC9, 0xCA, 0xCB, 0xCD, 0xCE, 0xCF):
                height = int.from_bytes(body[index + 5 : index + 7], "big")
                width = int.from_bytes(body[index + 7 : index + 9], "big")
                return width, height
            index += 2 + length
    return 0, 0


def snapshot_database(db_path: Path, normalizer: Normalizer) -> dict:
    if not db_path.exists():
        return {"__error__": f"missing database {db_path}"}
    connection = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
    connection.row_factory = sqlite3.Row
    snapshot: dict[str, object] = {}
    for table in SNAPSHOT_TABLES:
        try:
            # core_siteconfig is compared keyed: its rowid order is the seeder's
            # insertion order, and the two release-push rows the rewrite deletes
            # would shift every later row.
            order_by = "key" if table == "core_siteconfig" else "rowid"
            rows = [
                dict(row)
                for row in connection.execute(f"SELECT * FROM {table} ORDER BY {order_by}")
            ]
        except sqlite3.OperationalError as exc:
            snapshot[table] = {"__error__": str(exc)}
            continue
        masks = TABLE_COLUMN_MASKS.get(table, {})
        cleaned = []
        for row in rows:
            if table == "core_siteconfig" and row["key"] in NON_PORTED_CONFIG_KEYS:
                continue
            if table == "core_siteconfig" and row["key"] in MANDATED_CONFIG_KEYS:
                row = {**row, "value_encrypted": "<mandated>"}
            item = {}
            for key, value in row.items():
                if key in masks and value is not None:
                    item[key] = masks[key]
                elif key in VOLATILE_COLUMNS and value is not None:
                    item[key] = "<ts>"
                else:
                    item[key] = normalizer.value(value)
            cleaned.append(item)
        snapshot[table] = cleaned
    connection.close()
    return snapshot
