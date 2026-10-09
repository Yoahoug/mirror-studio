"""Fake image/LLM upstream used by the differential harness.

Records every inbound request (so we can prove the Go rewrite speaks the same
wire protocol as the Django version) and answers with a fixed, deterministic
payload. Development asset — never shipped in the container image.
"""

from __future__ import annotations

import argparse
import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from harness import epub_bytes, image_dimensions, png_bytes  # noqa: F401

SCENE_PLAN = [
    {
        "编号": 1,
        "中文标题": "星港初见",
        "所在章节": "第一章",
        "故事意义": "主角第一次见到星港，世界观在此展开。",
        "原文摘录": "舱门缓缓打开，星光涌入。",
        "详细英文视觉描述": (
            "A vast orbital star port opens before the hero; volumetric starlight spills "
            "through the docking bay, industrial gantries receding into haze, live-action "
            "film still, dramatic natural lighting, photorealistic concept art."
        ),
    },
    {
        "编号": 2,
        "中文标题": "长廊独行",
        "所在章节": "第二章",
        "故事意义": "主角做出离开的决定。",
        "原文摘录": "他转身走向长廊。",
        "详细英文视觉描述": (
            "A lone figure walks down an endless corridor of polished alloy, reflections "
            "stretching behind him, cinematic wide lens, moody practical lighting, "
            "photorealistic live-action film still."
        ),
    },
]

PNG = png_bytes(width=64, height=64)


class Recorder:
    def __init__(self, log_path: Path) -> None:
        self.log_path = log_path
        self.lock = threading.Lock()
        self.entries: list[dict] = []
        log_path.write_text("", encoding="utf-8")

    def add(self, entry: dict) -> None:
        with self.lock:
            self.entries.append(entry)
            with self.log_path.open("a", encoding="utf-8") as handle:
                handle.write(json.dumps(entry, ensure_ascii=False, sort_keys=True) + "\n")

    def reset(self) -> None:
        with self.lock:
            self.entries = []
            self.log_path.write_text("", encoding="utf-8")


RECORDER: Recorder | None = None


def _summarize_body(content_type: str, body: bytes) -> object:
    """Record the wire payload with only runtime-independent facts.

    File uploads are summarised by part name, filename and decoded dimensions:
    the exact bytes of a re-encoded JPEG differ between Pillow and Go's encoder
    and are not part of the upstream contract.
    """
    if not body:
        return ""
    if "multipart/form-data" in content_type:
        boundary = content_type.split("boundary=", 1)[-1].strip().strip('"')
        parts = []
        for raw in body.split(b"--" + boundary.encode()):
            if not raw.strip() or raw.strip() == b"--":
                continue
            header_blob, _, payload = raw.partition(b"\r\n\r\n")
            headers = header_blob.decode("utf-8", "replace")
            name = ""
            filename = ""
            part_type = ""
            for line in headers.splitlines():
                if line.lower().startswith("content-disposition:"):
                    for piece in line.split(";"):
                        piece = piece.strip()
                        if piece.startswith("name="):
                            name = piece[5:].strip('"')
                        elif piece.startswith("filename="):
                            filename = piece[9:].strip('"')
                elif line.lower().startswith("content-type:"):
                    part_type = line.split(":", 1)[1].strip()
            payload = payload.rstrip(b"\r\n-")
            entry = {"name": name}
            if filename:
                width, height = image_dimensions(payload)
                entry.update(
                    {
                        "filename": filename,
                        "content_type": part_type,
                        "length": len(payload),
                        "width": width,
                        "height": height,
                    }
                )
            else:
                entry["text"] = payload.decode("utf-8", "replace")
            parts.append(entry)
        return {"__multipart__": sorted(parts, key=lambda item: item["name"])}
    if "json" in content_type:
        try:
            return json.loads(body.decode("utf-8"))
        except json.JSONDecodeError:
            return {"__raw__": body.decode("utf-8", "replace")[:2000]}
    if "octet-stream" in content_type:
        return {"__binary__": len(body)}
    return {"__raw__": body.decode("utf-8", "replace")[:2000]}


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *_args):  # keep the harness output clean
        return

    def _record(self, body: bytes) -> None:
        if RECORDER is None:
            return
        RECORDER.add(
            {
                "method": self.command,
                "path": self.path,
                "authorization": self.headers.get("Authorization", ""),
                "content_type": (self.headers.get("Content-Type") or "").split(";")[0],
                "body": _summarize_body(self.headers.get("Content-Type") or "", body),
            }
        )

    def _send(self, status: int, payload: dict | bytes, content_type: str) -> None:
        body = payload if isinstance(payload, bytes) else json.dumps(payload, ensure_ascii=False).encode()
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:  # noqa: N802
        self._record(b"")
        if self.path.startswith("/result"):
            self._send(200, PNG, "image/png")
            return
        if self.path.startswith("/slow-result"):
            self._send(200, PNG, "image/png")
            return
        self._send(404, {"error": {"message": "not found"}}, "application/json")

    def do_POST(self) -> None:  # noqa: N802
        length = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(length) if length else b""
        self._record(body)
        path = self.path.split("?")[0]
        if path.endswith("/images/generations"):
            self._send(
                200,
                {
                    "created": 1777220495,
                    "data": [
                        {
                            "b64_json": __import__("base64").b64encode(PNG).decode(),
                            "revised_prompt": "fixture",
                        }
                    ],
                    "model": "fixture-model",
                },
                "application/json",
            )
            return
        if path.endswith("/images/edits"):
            self._send(
                200,
                {
                    "created": 1777220495,
                    "data": [{"b64_json": __import__("base64").b64encode(PNG).decode()}],
                },
                "application/json",
            )
            return
        if path.endswith("/chat/completions"):
            self._send(
                200,
                {
                    "choices": [
                        {"message": {"role": "assistant", "content": json.dumps(SCENE_PLAN, ensure_ascii=False)}}
                    ]
                },
                "application/json",
            )
            return
        if path.endswith("/remote-image"):
            self._send(
                200,
                {"data": [{"url": f"http://127.0.0.1:{self.server.server_port}/result.png"}]},
                "application/json",
            )
            return
        self._send(404, {"error": {"message": "not found"}}, "application/json")


class Server(ThreadingHTTPServer):
    daemon_threads = True
    allow_reuse_address = True


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=8099)
    parser.add_argument("--log", default="./upstream.jsonl")
    parser.add_argument("--reset", action="store_true")
    args = parser.parse_args()
    global RECORDER
    RECORDER = Recorder(Path(args.log))
    server = Server(("127.0.0.1", args.port), Handler)
    print(f"fake upstream listening on 127.0.0.1:{args.port}, log={args.log}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
