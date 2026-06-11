from __future__ import annotations

import asyncio
import base64
import hashlib
import json
import logging
import os
import re
import shutil
import subprocess
import sys
import threading
import time
import uuid
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import aiohttp
import requests
from asgiref.sync import sync_to_async
from bs4 import BeautifulSoup
from django.conf import settings
from django.db import close_old_connections, transaction
from django.utils import timezone
from ebooklib import ITEM_DOCUMENT, epub
from requests import exceptions as request_exceptions

from apps.generator.models import ImageChannel

from .models import NovelSceneImage, NovelUploadedBook, NovelVisualizationTask

logger = logging.getLogger(__name__)


SCENE_KEYS = {
    "number": ["number", "编号", "scene_number", "sceneNumber", "id"],
    "title": ["title", "中文标题", "标题"],
    "chapter": ["chapter", "所在章节", "章节"],
    "story_significance": ["story_significance", "storySignificance", "故事意义", "significance"],
    "excerpt": ["excerpt", "原文摘录", "quote"],
    "visual_prompt": ["visual_prompt", "visualPrompt", "详细英文视觉描述", "english_visual_description", "prompt"],
}

AUDIT_KEYWORDS = (
    "content_policy",
    "safety",
    "moderation",
    "policy",
    "unsafe",
    "sensitive",
    "blocked",
    "rejected",
    "审核",
    "安全",
    "拒绝",
    "违规",
)


@dataclass(frozen=True)
class NovelBook:
    id: str
    source: str
    filename: str
    display_name: str
    path: Path
    size: int
    uploaded_book_id: int | None = None
    created_at: str | None = None


class NovelVisualizationError(Exception):
    pass


@dataclass(frozen=True)
class SceneLLMConfig:
    url: str
    api_key: str
    model_name: str
    source_name: str


def _join_provider_url(base_url: str, api_path: str) -> str:
    return f"{base_url.rstrip('/')}/{api_path.lstrip('/')}"


def resolve_scene_llm_config(channel_code: str = "") -> SceneLLMConfig:
    channels = ImageChannel.objects.filter(
        is_active=True,
        channel_type=ImageChannel.ChannelType.LLM,
    ).exclude(api_key_encrypted="")
    channel = channels.filter(channel_code=channel_code).first() if channel_code else channels.order_by("priority", "id").first()
    if channel and channel.api_key:
        return SceneLLMConfig(
            url=_join_provider_url(channel.base_url, channel.api_path or settings.NOVELVIZ_TEXT_API_PATH),
            api_key=channel.api_key,
            model_name=channel.model_name,
            source_name=channel.name,
        )
    if settings.NOVELVIZ_TEXT_API_KEY:
        return SceneLLMConfig(
            url=_join_provider_url(settings.NOVELVIZ_API_BASE_URL, settings.NOVELVIZ_TEXT_API_PATH),
            api_key=settings.NOVELVIZ_TEXT_API_KEY,
            model_name=settings.NOVELVIZ_TEXT_MODEL,
            source_name="NOVELVIZ_TEXT_API_KEY",
        )
    raise NovelVisualizationError("未配置可用小说分析 LLM")


def list_epub_books(user=None) -> list[NovelBook]:
    novel_dir = Path(settings.NOVELVIZ_BOOK_DIR)
    books: list[NovelBook] = []
    if novel_dir.exists():
        books.extend(
            NovelBook(
                id=f"builtin:{path.name}",
                source=NovelVisualizationTask.BookSource.BUILTIN,
                filename=path.name,
                display_name=path.name,
                path=path,
                size=path.stat().st_size,
            )
            for path in sorted(novel_dir.glob("*.epub"), key=lambda item: item.name.lower())
            if path.is_file()
        )
    if user and getattr(user, "is_authenticated", False):
        media_root = Path(settings.MEDIA_ROOT).resolve()
        for uploaded in NovelUploadedBook.objects.filter(user=user).order_by("-created_at", "-id")[:50]:
            path = (media_root / uploaded.storage_path).resolve()
            books.append(
                NovelBook(
                    id=f"uploaded:{uploaded.id}",
                    source=NovelVisualizationTask.BookSource.UPLOADED,
                    filename=uploaded.display_name,
                    display_name=uploaded.display_name,
                    path=path,
                    size=uploaded.file_size,
                    uploaded_book_id=uploaded.id,
                    created_at=uploaded.created_at.isoformat() if uploaded.created_at else None,
                )
            )
    return books


def resolve_book_path(filename: str) -> Path:
    clean_name = Path((filename or "").strip()).name
    if not clean_name.lower().endswith(".epub"):
        raise ValueError("请选择 EPUB 小说文件")
    novel_dir = Path(settings.NOVELVIZ_BOOK_DIR).resolve()
    path = (novel_dir / clean_name).resolve()
    if novel_dir not in path.parents and path != novel_dir:
        raise ValueError("EPUB 文件路径不合法")
    if not path.exists() or not path.is_file():
        raise ValueError("EPUB 文件不存在")
    return path


def resolve_uploaded_book_path(uploaded_book_id: int, user) -> Path:
    uploaded = NovelUploadedBook.objects.filter(id=uploaded_book_id, user=user).first()
    if not uploaded:
        raise ValueError("上传的 EPUB 文件不存在")
    media_root = Path(settings.MEDIA_ROOT).resolve()
    path = (media_root / uploaded.storage_path).resolve()
    if media_root not in path.parents or not path.exists() or not path.is_file():
        raise ValueError("上传的 EPUB 文件不存在")
    return path


def resolve_task_book_path(task: NovelVisualizationTask) -> Path:
    if task.book_source == NovelVisualizationTask.BookSource.UPLOADED:
        if not task.uploaded_book_id:
            raise ValueError("上传的 EPUB 文件不存在")
        return resolve_uploaded_book_path(task.uploaded_book_id, task.user)
    return resolve_book_path(task.book_filename)


def _clean_epub_display_name(filename: str) -> str:
    clean_name = Path((filename or "").strip()).name
    clean_name = re.sub(r"[\x00-\x1f/\\:]+", "-", clean_name).strip(" .")
    if not clean_name:
        clean_name = "novel.epub"
    if not clean_name.lower().endswith(".epub"):
        raise ValueError("请上传 EPUB 小说文件")
    return clean_name[:255]


def serialize_uploaded_book(uploaded: NovelUploadedBook) -> dict[str, Any]:
    return {
        "id": f"uploaded:{uploaded.id}",
        "source": NovelVisualizationTask.BookSource.UPLOADED,
        "filename": uploaded.display_name,
        "display_name": uploaded.display_name,
        "size": uploaded.file_size,
        "uploaded_book_id": uploaded.id,
        "created_at": uploaded.created_at.isoformat() if uploaded.created_at else None,
    }


def _uploaded_epub_target_path(*, display_name: str, user) -> tuple[Path, Path]:
    relative_dir = Path(settings.NOVELVIZ_UPLOAD_DIR) / "users" / str(user.id)
    relative_path = relative_dir / f"{uuid.uuid4().hex}-{display_name}"
    media_root = Path(settings.MEDIA_ROOT).resolve()
    absolute_path = (media_root / relative_path).resolve()
    if media_root not in absolute_path.parents:
        raise ValueError("上传路径不合法")
    absolute_path.parent.mkdir(parents=True, exist_ok=True)
    return relative_path, absolute_path


def store_uploaded_epub(uploaded_file, user) -> NovelUploadedBook:
    display_name = _clean_epub_display_name(getattr(uploaded_file, "name", "novel.epub"))
    max_bytes = int(settings.NOVELVIZ_MAX_EPUB_UPLOAD_BYTES)
    relative_path, absolute_path = _uploaded_epub_target_path(display_name=display_name, user=user)

    digest = hashlib.sha256()
    total_size = 0
    try:
        with absolute_path.open("wb") as target:
            for chunk in uploaded_file.chunks():
                total_size += len(chunk)
                if total_size > max_bytes:
                    raise ValueError(f"EPUB 文件不能超过 {max_bytes // 1024 // 1024} MB")
                digest.update(chunk)
                target.write(chunk)
        if total_size <= 0:
            raise ValueError("上传的 EPUB 文件为空")
        return NovelUploadedBook.objects.create(
            user=user,
            display_name=display_name,
            storage_path=str(relative_path),
            file_size=total_size,
            sha256=digest.hexdigest(),
        )
    except Exception:
        absolute_path.unlink(missing_ok=True)
        raise


def store_uploaded_epub_path(*, source_path: Path, display_name: str, user, sha256: str = "") -> NovelUploadedBook:
    display_name = _clean_epub_display_name(display_name)
    max_bytes = int(settings.NOVELVIZ_MAX_EPUB_UPLOAD_BYTES)
    source_path = Path(source_path)
    if not source_path.exists() or not source_path.is_file():
        raise ValueError("上传的 EPUB 文件不存在")
    total_size = source_path.stat().st_size
    if total_size <= 0:
        raise ValueError("上传的 EPUB 文件为空")
    if total_size > max_bytes:
        raise ValueError(f"EPUB 文件不能超过 {max_bytes // 1024 // 1024} MB")
    if not sha256:
        digest = hashlib.sha256()
        with source_path.open("rb") as source:
            for chunk in iter(lambda: source.read(1024 * 1024), b""):
                digest.update(chunk)
        sha256 = digest.hexdigest()
    relative_path, absolute_path = _uploaded_epub_target_path(display_name=display_name, user=user)
    try:
        with source_path.open("rb") as source, absolute_path.open("wb") as target:
            shutil.copyfileobj(source, target, length=1024 * 1024)
        return NovelUploadedBook.objects.create(
            user=user,
            display_name=display_name,
            storage_path=str(relative_path),
            file_size=total_size,
            sha256=sha256,
        )
    except Exception:
        absolute_path.unlink(missing_ok=True)
        raise


def extract_epub_text(path: Path) -> str:
    book = epub.read_epub(str(path))
    chunks: list[str] = []
    for item in book.get_items():
        if item.get_type() != ITEM_DOCUMENT:
            continue
        soup = BeautifulSoup(item.get_content(), "html.parser")
        for tag in soup(["script", "style", "nav"]):
            tag.decompose()
        text = soup.get_text("\n", strip=True)
        text = re.sub(r"\n{3,}", "\n\n", text)
        if text:
            chunks.append(text)
    full_text = "\n\n".join(chunks).strip()
    if not full_text:
        raise NovelVisualizationError("EPUB 未提取到正文内容")
    return full_text


def _strip_json_code_fence(content: str) -> str:
    text = (content or "").strip()
    fence_match = re.search(r"```(?:json)?\s*(.*?)\s*```", text, flags=re.IGNORECASE | re.DOTALL)
    if fence_match:
        return fence_match.group(1).strip()
    return text


def parse_scene_json(content: str, *, max_scenes: int | None = None) -> list[dict[str, Any]]:
    text = _strip_json_code_fence(content)
    try:
        parsed = json.loads(text)
    except json.JSONDecodeError:
        start = text.find("[")
        end = text.rfind("]")
        if start < 0 or end <= start:
            raise
        parsed = json.loads(text[start : end + 1])
    if isinstance(parsed, dict):
        parsed = parsed.get("scenes") or parsed.get("场景") or parsed.get("items")
    if not isinstance(parsed, list):
        raise NovelVisualizationError("AI 返回的场景 JSON 不是数组")
    limit = max(1, int(max_scenes or settings.NOVELVIZ_MAX_SCENES))
    scenes = [_normalize_scene(item, index + 1) for index, item in enumerate(parsed[:limit])]
    return [scene for scene in scenes if scene["title"] and scene["visual_prompt"]]


def _first_value(data: dict[str, Any], keys: list[str], default: Any = "") -> Any:
    for key in keys:
        if key in data and data[key] not in {None, ""}:
            return data[key]
    return default


def _normalize_scene(raw: Any, fallback_number: int) -> dict[str, Any]:
    if not isinstance(raw, dict):
        return {
            "number": fallback_number,
            "title": f"场景 {fallback_number}",
            "chapter": "",
            "story_significance": "",
            "excerpt": "",
            "visual_prompt": str(raw or "").strip(),
        }
    try:
        number = int(_first_value(raw, SCENE_KEYS["number"], fallback_number))
    except (TypeError, ValueError):
        number = fallback_number
    return {
        "number": number or fallback_number,
        "title": str(_first_value(raw, SCENE_KEYS["title"], f"场景 {fallback_number}")).strip()[:160],
        "chapter": str(_first_value(raw, SCENE_KEYS["chapter"], "")).strip()[:160],
        "story_significance": str(_first_value(raw, SCENE_KEYS["story_significance"], "")).strip(),
        "excerpt": str(_first_value(raw, SCENE_KEYS["excerpt"], "")).strip(),
        "visual_prompt": str(_first_value(raw, SCENE_KEYS["visual_prompt"], "")).strip(),
    }


def _auto_scene_llm_payload(novel_text: str, model_name: str) -> dict[str, Any]:
    system_prompt = (
        "You are a senior film concept art director and literary analyst. "
        "Read the full Chinese novel deeply, identify the most visually shocking scenes in plot order, "
        "and return only valid JSON."
    )
    user_prompt = f"""
请深度理解以下小说全文，按情节顺序挑选不超过 {settings.NOVELVIZ_MAX_SCENES} 个最震撼、最适合电影级概念图的视觉场景。

要求：
1. 返回 JSON 数组，不要输出解释文字。
2. 每个对象必须包含：编号、中文标题、所在章节、故事意义、原文摘录、详细英文视觉描述。
3. 详细英文视觉描述必须 200-300 words，写实、电影级、动态、生动、真实摄影质感，不要卡通、不要动漫、不要插画感。
4. 若涉及危险、战争、灾难等内容，描述为宏大电影场面和情绪张力，避免血腥细节。
5. 场景数量最多 {settings.NOVELVIZ_MAX_SCENES} 个。

小说全文：
{novel_text}
""".strip()
    return {
        "model": model_name,
        "messages": [
            {"role": "system", "content": system_prompt},
            {"role": "user", "content": user_prompt},
        ],
        "temperature": 0.7,
    }


def _targeted_scene_llm_payload(novel_text: str, model_name: str, target_plot: str) -> dict[str, Any]:
    system_prompt = (
        "You are a senior film concept art director and literary analyst. "
        "Read the full Chinese novel deeply, locate and interpret the user's requested plot beat in context, "
        "then return only valid JSON for one cinematic image."
    )
    user_prompt = f"""
请深度理解以下小说全文，并围绕用户指定的剧情只生成 1 个最适合画成电影概念图的镜头。

用户想呈现的剧情：
{target_plot}

要求：
1. 必须先结合全文判断这段剧情在小说中的上下文、人物关系、情绪和故事意义，再提炼一个画面瞬间。
2. 只返回 JSON 数组，数组里只能有 1 个对象，不要输出解释文字。
3. 对象必须包含：编号、中文标题、所在章节、故事意义、原文摘录、详细英文视觉描述。
4. 如果用户描述比较模糊，请在全文里寻找最匹配的剧情，不要拒绝；如果没有逐字匹配，也要基于全书语境选择最接近的一幕。
5. 详细英文视觉描述必须 200-300 words，写实、电影级、动态、生动、真实摄影质感，不要卡通、不要动漫、不要插画感。
6. 若涉及危险、战争、灾难等内容，描述为宏大电影场面和情绪张力，避免血腥细节。

小说全文：
{novel_text}
""".strip()
    return {
        "model": model_name,
        "messages": [
            {"role": "system", "content": system_prompt},
            {"role": "user", "content": user_prompt},
        ],
        "temperature": 0.65,
    }


def request_scene_plan(novel_text: str, channel_code: str = "", *, target_plot: str = "") -> list[dict[str, Any]]:
    llm_config = resolve_scene_llm_config(channel_code)
    headers = {
        "Authorization": f"Bearer {llm_config.api_key}",
        "Content-Type": "application/json",
    }
    target_plot = (target_plot or "").strip()
    payload = (
        _targeted_scene_llm_payload(novel_text, llm_config.model_name, target_plot)
        if target_plot
        else _auto_scene_llm_payload(novel_text, llm_config.model_name)
    )
    max_scenes = 1 if target_plot else settings.NOVELVIZ_MAX_SCENES
    last_error: Exception | None = None
    for attempt in range(1, settings.NOVELVIZ_TEXT_MAX_RETRIES + 1):
        try:
            response = requests.post(
                llm_config.url,
                headers=headers,
                json=payload,
                timeout=(settings.NOVELVIZ_TEXT_CONNECT_TIMEOUT_SECONDS, settings.NOVELVIZ_TEXT_READ_TIMEOUT_SECONDS),
            )
            response.raise_for_status()
            data = response.json()
            content = data["choices"][0]["message"]["content"]
            scenes = parse_scene_json(content, max_scenes=max_scenes)
            if not scenes:
                raise NovelVisualizationError("AI 没有返回可用场景")
            return scenes
        except request_exceptions.ConnectTimeout as exc:
            last_error = exc
            logger.warning("小说场景分析连接超时 attempt=%s", attempt)
        except request_exceptions.ReadTimeout as exc:
            last_error = exc
            logger.warning("小说场景分析读取超时 attempt=%s", attempt)
        except request_exceptions.RequestException as exc:
            last_error = exc
            logger.warning("小说场景分析请求异常 attempt=%s error=%s", attempt, exc)
        except (KeyError, TypeError, ValueError, json.JSONDecodeError, NovelVisualizationError) as exc:
            last_error = exc
            logger.warning("小说场景分析响应解析失败 attempt=%s error=%s", attempt, exc)
        if attempt < settings.NOVELVIZ_TEXT_MAX_RETRIES:
            time.sleep(min(2 * attempt, 6))
    raise NovelVisualizationError(f"AI 场景分析失败：{last_error}")


def _novelviz_event_log_limit() -> int:
    return max(1, int(getattr(settings, "NOVELVIZ_EVENT_LOG_LIMIT", 120) or 120))


def _novelviz_event_payload_max_chars() -> int:
    return max(1000, int(getattr(settings, "NOVELVIZ_EVENT_PAYLOAD_MAX_CHARS", 120_000) or 120_000))


def _novelviz_event_field_max_chars() -> int:
    return max(200, int(getattr(settings, "NOVELVIZ_EVENT_FIELD_MAX_CHARS", 4000) or 4000))


def _truncate_event_string(value: str) -> str:
    max_chars = _novelviz_event_field_max_chars()
    if len(value) <= max_chars:
        return value
    return f"{value[:max_chars]}...[truncated {len(value) - max_chars} chars]"


def _sanitize_event_payload(value: Any) -> Any:
    if isinstance(value, str):
        return _truncate_event_string(value)
    if isinstance(value, dict):
        sanitized: dict[str, Any] = {}
        for key, item in value.items():
            if key == "logs":
                if item in ([], None):
                    sanitized["logs"] = []
                continue
            sanitized[str(key)[:120]] = _sanitize_event_payload(item)
        return sanitized
    if isinstance(value, list):
        return [_sanitize_event_payload(item) for item in value[:50]]
    if isinstance(value, tuple):
        return [_sanitize_event_payload(item) for item in value[:50]]
    return value


def _fit_event_payload(value: dict[str, Any]) -> dict[str, Any]:
    payload = _sanitize_event_payload(value if isinstance(value, dict) else {"value": value})
    max_chars = _novelviz_event_payload_max_chars()
    try:
        encoded = json.dumps(payload, ensure_ascii=False, separators=(",", ":"))
    except TypeError:
        payload = {"message": _truncate_event_string(str(value))}
        encoded = json.dumps(payload, ensure_ascii=False, separators=(",", ":"))
    if len(encoded) <= max_chars:
        return payload
    message = str(payload.get("message") or payload.get("error") or "事件内容过大，已裁剪")
    return {
        "message": _truncate_event_string(message),
        "truncated": True,
        "original_chars": len(encoded),
    }


def append_task_event(task_id: int, event_type: str, payload: dict[str, Any]) -> dict[str, Any]:
    safe_payload = _fit_event_payload(payload)
    with transaction.atomic():
        task = NovelVisualizationTask.objects.select_for_update().get(pk=task_id)
        task.last_event_id += 1
        event = {
            "id": task.last_event_id,
            "type": event_type,
            "payload": safe_payload,
            "created_at": timezone.now().isoformat(),
        }
        logs = list(task.logs or [])
        logs.append(event)
        task.logs = logs[-_novelviz_event_log_limit():]
        update_fields = ["last_event_id", "logs", "updated_at"]
        progress = safe_payload.get("progress_percent")
        if isinstance(progress, int):
            task.progress_percent = max(0, min(100, progress))
            update_fields.append("progress_percent")
        task.save(update_fields=update_fields)
    return event


def task_log(task_id: int, message: str, progress_percent: int | None = None, **extra: Any) -> dict[str, Any]:
    payload = {"message": message, **extra}
    if progress_percent is not None:
        payload["progress_percent"] = progress_percent
    return append_task_event(task_id, "log", payload)


def serialize_scene_image(image: NovelSceneImage) -> dict[str, Any]:
    preview_url = f"{image.image_url}?preview=1" if image.image_url else ""
    download_url = f"{image.image_url}?download=1" if image.image_url else ""
    return {
        "id": image.id,
        "scene_number": image.scene_number,
        "title": image.title,
        "chapter": image.chapter,
        "story_significance": image.story_significance,
        "excerpt": image.excerpt,
        "visual_prompt": image.visual_prompt,
        "status": image.status,
        "error_message": image.error_message,
        "image_url": image.image_url,
        "preview_url": preview_url,
        "download_url": download_url,
        "attempts": image.attempts,
        "created_at": image.created_at.isoformat() if image.created_at else None,
        "finished_at": image.finished_at.isoformat() if image.finished_at else None,
    }


def serialize_task(task: NovelVisualizationTask, *, include_logs: bool = True) -> dict[str, Any]:
    images = list(task.images.all())
    return {
        "id": task.id,
        "book_filename": task.book_filename,
        "book_source": task.book_source,
        "uploaded_book_id": task.uploaded_book_id,
        "scene_mode": task.scene_mode,
        "target_plot": task.target_plot,
        "status": task.status,
        "progress_percent": task.progress_percent,
        "error_message": task.error_message,
        "scenes": task.scenes_json or [],
        "images": [serialize_scene_image(image) for image in images],
        "logs": task.logs if include_logs else [],
        "created_at": task.created_at.isoformat() if task.created_at else None,
        "started_at": task.started_at.isoformat() if task.started_at else None,
        "finished_at": task.finished_at.isoformat() if task.finished_at else None,
    }


def reset_task_for_retry(task_id: int, user) -> NovelVisualizationTask:
    with transaction.atomic():
        task = NovelVisualizationTask.objects.select_for_update().get(pk=task_id, user=user)
        if task.status not in {NovelVisualizationTask.Status.FAILED, NovelVisualizationTask.Status.CANCELED}:
            raise NovelVisualizationError("只有失败或已取消的小说可视化任务可以重试")

        if not settings.NOVELVIZ_IMAGE_API_KEY:
            raise NovelVisualizationError("未配置小说生图模型 Key")

        has_scene_plan = bool(task.scenes_json) and task.images.exists()
        if has_scene_plan:
            NovelSceneImage.objects.filter(task=task).exclude(status=NovelSceneImage.Status.SUCCEEDED).update(
                status=NovelSceneImage.Status.PENDING,
                error_message="",
                image_path="",
                image_url="",
                attempts=0,
                finished_at=None,
                updated_at=timezone.now(),
            )
            progress_percent = 36
            logs = list(task.logs or [])[-30:]
        else:
            resolve_task_book_path(task)
            resolve_scene_llm_config(task.llm_channel_code)
            NovelSceneImage.objects.filter(task=task).delete()
            task.scenes_json = []
            progress_percent = 1
            logs = []

        task.status = NovelVisualizationTask.Status.QUEUED
        task.progress_percent = progress_percent
        task.logs = logs
        task.error_message = ""
        # Keep scene_mode and target_plot unchanged so retry repeats the same user intent.
        task.started_at = None
        task.finished_at = None
        task.last_event_id = max((int(item.get("id") or 0) for item in logs), default=0)
        task.save(
            update_fields=[
                "status",
                "progress_percent",
                "logs",
                "scenes_json",
                "error_message",
                "started_at",
                "finished_at",
                "last_event_id",
                "updated_at",
            ]
        )
    return NovelVisualizationTask.objects.prefetch_related("images").get(pk=task_id)


def _scene_from_plan(scene: dict[str, Any], index: int) -> dict[str, str]:
    return {
        "title": str(scene.get("title") or f"场景 {index}")[:160],
        "chapter": str(scene.get("chapter") or "")[:160],
        "story_significance": str(scene.get("story_significance") or ""),
        "excerpt": str(scene.get("excerpt") or ""),
        "visual_prompt": str(scene.get("visual_prompt") or ""),
    }


def _prepare_existing_scene_retry(task: NovelVisualizationTask) -> bool:
    scenes = list(task.scenes_json or [])
    if not scenes:
        return False

    existing = {image.scene_number: image for image in task.images.all()}
    for index, scene in enumerate(scenes, start=1):
        if index in existing:
            continue
        scene_payload = _scene_from_plan(scene, index)
        NovelSceneImage.objects.create(task=task, scene_number=index, **scene_payload)

    return NovelSceneImage.objects.filter(
        task=task,
        status__in=[
            NovelSceneImage.Status.PENDING,
            NovelSceneImage.Status.RUNNING,
            NovelSceneImage.Status.FAILED,
        ],
    ).exists()


def _run_image_only_retry(task_id: int, task: NovelVisualizationTask) -> bool:
    if not _prepare_existing_scene_retry(task):
        return False

    task.progress_percent = max(task.progress_percent, 36)
    task.save(update_fields=["progress_percent", "updated_at"])
    append_task_event(
        task_id,
        "scene_plan_ready",
        {
            "message": "已保留上次分析结果，开始重新生成失败图片",
            "progress_percent": 36,
            "scenes": task.scenes_json or [],
            "task": serialize_task(NovelVisualizationTask.objects.prefetch_related("images").get(pk=task_id), include_logs=False),
        },
    )
    asyncio.run(_generate_scene_images(task_id))
    return True


def _task_is_stalled(task: NovelVisualizationTask) -> bool:
    if task.status != NovelVisualizationTask.Status.RUNNING:
        return False
    if not task.started_at:
        return False
    stall_seconds = max(int(getattr(settings, "NOVELVIZ_STALL_REQUEUE_SECONDS", 60)), 30)
    return (timezone.now() - task.started_at).total_seconds() >= stall_seconds and (task.last_event_id or 0) == 0


def _rescue_stalled_task(task: NovelVisualizationTask) -> bool:
    if not _task_is_stalled(task):
        return False
    logger.warning("小说可视化任务疑似卡住，准备重新排队 task_id=%s", task.id)
    task.status = NovelVisualizationTask.Status.QUEUED
    task.progress_percent = 1
    task.error_message = ""
    task.started_at = None
    task.finished_at = None
    task.last_event_id = 0
    task.logs = []
    task.scenes_json = []
    NovelSceneImage.objects.filter(task=task).delete()
    task.save(
        update_fields=[
            "status",
            "progress_percent",
            "error_message",
            "started_at",
            "finished_at",
            "last_event_id",
            "logs",
            "scenes_json",
            "updated_at",
        ]
    )
    dispatch_novel_visualization_task(task.id)
    return True


def _prepare_scene_prompt(scene: NovelSceneImage) -> str:
    return f"""
Create a cinematic, photorealistic concept image for this novel scene.
Style: live-action film still, realistic human scale, dramatic natural lighting, tactile materials, atmospheric depth, physically plausible camera lens, high-end production design, emotionally intense, vivid and dynamic.
Avoid: cartoon, anime, manga, illustration, painterly style, flat colors, childish proportions, over-stylized fantasy art.

Scene title: {scene.title}
Chapter: {scene.chapter}
Story meaning: {scene.story_significance}
Visual description:
{scene.visual_prompt}
""".strip()


def _rewrite_prompt_for_safety(prompt: str) -> str:
    return f"""
Rewrite this image prompt to be acceptable for a strict safety filter while preserving a realistic cinematic concept-art look and the story's awe, scale, tension, and emotional impact.
Use non-graphic language. Avoid explicit injury, gore, cruelty, identifiable public figures, hateful symbols, and sexual content.
Keep it photorealistic, live-action, dramatic, and vivid.

Original prompt:
{prompt}
""".strip()


def _rewrite_prompt_for_retry(prompt: str, error_message: str) -> str:
    return f"""
Rewrite this image prompt for a second generation attempt.
Keep the same story beat and cinematic realism, but make the composition clearer, reduce unnecessary detail, and focus on one unmistakable visual moment.
Use concise English. Avoid overly long nested clauses.
Do not mention the failure directly.

Original prompt:
{prompt}

Recent failure context:
{error_message}
""".strip()


def _is_audit_error(message: str) -> bool:
    lowered = (message or "").lower()
    return any(keyword in lowered for keyword in AUDIT_KEYWORDS)


def _get_scene_for_generation(scene_id: int) -> NovelSceneImage:
    return NovelSceneImage.objects.select_related("task").get(pk=scene_id)


def _get_scene_ids(task_id: int) -> list[int]:
    return list(
        NovelSceneImage.objects.filter(
            task_id=task_id,
            status__in=[
                NovelSceneImage.Status.PENDING,
                NovelSceneImage.Status.RUNNING,
                NovelSceneImage.Status.FAILED,
            ],
        )
        .order_by("scene_number")
        .values_list("id", flat=True)
    )


def _mark_scene_running(scene_id: int, attempt: int) -> dict[str, Any]:
    scene = _get_scene_for_generation(scene_id)
    scene.status = NovelSceneImage.Status.RUNNING
    scene.attempts = attempt
    scene.error_message = ""
    scene.save(update_fields=["status", "attempts", "error_message", "updated_at"])
    return serialize_scene_image(scene)


def _mark_scene_attempt_failed(scene_id: int, error_message: str) -> tuple[dict[str, Any], int]:
    scene = NovelSceneImage.objects.get(pk=scene_id)
    scene.error_message = error_message[:255]
    scene.save(update_fields=["error_message", "updated_at"])
    return serialize_scene_image(scene), scene.scene_number


def _mark_scene_succeeded(scene_id: int, content: bytes, task_id: int) -> dict[str, Any]:
    scene = _get_scene_for_generation(scene_id)
    relative_path = Path("novelviz") / "users" / str(scene.task.user_id) / "tasks" / str(task_id) / f"scene-{scene.scene_number:02d}.png"
    absolute_path = Path(settings.MEDIA_ROOT) / relative_path
    absolute_path.parent.mkdir(parents=True, exist_ok=True)
    absolute_path.write_bytes(content)
    scene.status = NovelSceneImage.Status.SUCCEEDED
    scene.image_path = str(relative_path)
    scene.image_url = f"/api/novelviz/tasks/{task_id}/images/{scene.id}.png"
    scene.error_message = ""
    scene.mark_finished()
    scene.save(update_fields=["status", "image_path", "image_url", "error_message", "finished_at", "updated_at"])
    return serialize_scene_image(scene)


def _mark_scene_failed(scene_id: int) -> dict[str, Any]:
    scene = NovelSceneImage.objects.get(pk=scene_id)
    scene.status = NovelSceneImage.Status.FAILED
    scene.mark_finished()
    scene.save(update_fields=["status", "finished_at", "updated_at"])
    return serialize_scene_image(scene)


def _calculate_image_progress(task_id: int) -> int:
    total = NovelSceneImage.objects.filter(task_id=task_id).count()
    if total <= 0:
        return 36
    finished = NovelSceneImage.objects.filter(
        task_id=task_id,
        status__in=[NovelSceneImage.Status.SUCCEEDED, NovelSceneImage.Status.FAILED],
    ).count()
    return min(96, 36 + round((finished / total) * 60))


async def _post_image_generation(session: aiohttp.ClientSession, prompt: str) -> bytes:
    url = _join_provider_url(settings.NOVELVIZ_API_BASE_URL, settings.NOVELVIZ_IMAGE_API_PATH)
    headers = {
        "Authorization": f"Bearer {settings.NOVELVIZ_IMAGE_API_KEY}",
        "Content-Type": "application/json",
    }
    payload = {
        "model": settings.NOVELVIZ_IMAGE_MODEL,
        "prompt": prompt,
        "size": settings.NOVELVIZ_IMAGE_SIZE,
        "quality": settings.NOVELVIZ_IMAGE_QUALITY,
        "response_format": "b64_json",
    }
    async with session.post(url, headers=headers, json=payload, timeout=settings.NOVELVIZ_IMAGE_TIMEOUT_SECONDS) as response:
        text = await response.text()
        if response.status >= 400:
            raise NovelVisualizationError(f"图片接口失败 HTTP {response.status}: {text[:500]}")
        try:
            data = json.loads(text)
            b64_data = data["data"][0]["b64_json"]
            return base64.b64decode(b64_data)
        except (KeyError, TypeError, ValueError, json.JSONDecodeError) as exc:
            raise NovelVisualizationError("图片接口返回格式异常") from exc


async def _generate_one_scene(session: aiohttp.ClientSession, semaphore: asyncio.Semaphore, task_id: int, scene_id: int) -> None:
    async with semaphore:
        scene = await sync_to_async(_get_scene_for_generation, thread_sensitive=True)(scene_id)
        prompt = _prepare_scene_prompt(scene)
        max_retries = max(int(settings.NOVELVIZ_IMAGE_MAX_RETRIES), 1)
        for attempt in range(1, max_retries + 1):
            scene_payload = await sync_to_async(_mark_scene_running, thread_sensitive=True)(scene_id, attempt)
            await sync_to_async(append_task_event, thread_sensitive=True)(
                task_id,
                "image_started",
                {
                    "message": f"开始生成第 {scene_payload['scene_number']} 张：{scene_payload['title']}",
                    "scene": scene_payload,
                },
            )
            try:
                content = await _post_image_generation(session, prompt)
                scene_payload = await sync_to_async(_mark_scene_succeeded, thread_sensitive=True)(scene_id, content, task_id)
                progress_percent = await sync_to_async(_calculate_image_progress, thread_sensitive=True)(task_id)
                await sync_to_async(append_task_event, thread_sensitive=True)(
                    task_id,
                    "image_done",
                    {
                        "message": f"第 {scene_payload['scene_number']} 张已完成",
                        "progress_percent": progress_percent,
                        "scene": scene_payload,
                    },
                )
                return
            except (asyncio.TimeoutError, aiohttp.ClientError, NovelVisualizationError) as exc:
                error_message = str(exc).strip() or exc.__class__.__name__
                error_message = error_message[:255]
                scene_payload, scene_number = await sync_to_async(_mark_scene_attempt_failed, thread_sensitive=True)(scene_id, error_message)
                await sync_to_async(append_task_event, thread_sensitive=True)(
                    task_id,
                    "log",
                    {
                        "message": f"第 {scene_number} 张生成失败，准备重试" if attempt < max_retries else f"第 {scene_number} 张生成失败",
                        "scene_id": scene_payload["id"],
                        "attempt": attempt,
                        "error": error_message,
                    },
                )
                if _is_audit_error(error_message):
                    prompt = _rewrite_prompt_for_safety(prompt)
                elif attempt < max_retries:
                    prompt = _rewrite_prompt_for_retry(prompt, error_message)
                if attempt < max_retries:
                    await asyncio.sleep(min(3 * attempt, 8))
                continue
        scene_payload = await sync_to_async(_mark_scene_failed, thread_sensitive=True)(scene_id)
        progress_percent = await sync_to_async(_calculate_image_progress, thread_sensitive=True)(task_id)
        await sync_to_async(append_task_event, thread_sensitive=True)(
            task_id,
            "image_failed",
            {
                "message": f"第 {scene_payload['scene_number']} 张最终失败",
                "progress_percent": progress_percent,
                "scene": scene_payload,
            },
        )


async def _generate_scene_images(task_id: int) -> None:
    timeout = aiohttp.ClientTimeout(total=None, sock_connect=30, sock_read=settings.NOVELVIZ_IMAGE_TIMEOUT_SECONDS)
    concurrency = max(1, min(int(settings.NOVELVIZ_IMAGE_CONCURRENCY), 2))
    connector = aiohttp.TCPConnector(limit=concurrency)
    semaphore = asyncio.Semaphore(concurrency)
    scene_ids = await sync_to_async(_get_scene_ids, thread_sensitive=True)(task_id)
    async with aiohttp.ClientSession(timeout=timeout, connector=connector) as session:
        await asyncio.gather(*[_generate_one_scene(session, semaphore, task_id, scene_id) for scene_id in scene_ids])


def _set_task_status(task_id: int, status: str, *, progress_percent: int | None = None, error_message: str = "") -> NovelVisualizationTask:
    task = NovelVisualizationTask.objects.get(pk=task_id)
    task.status = status
    if progress_percent is not None:
        task.progress_percent = progress_percent
    task.error_message = (error_message or "")[:255]
    if status == NovelVisualizationTask.Status.RUNNING:
        task.started_at = task.started_at or timezone.now()
    if status in {NovelVisualizationTask.Status.SUCCEEDED, NovelVisualizationTask.Status.FAILED, NovelVisualizationTask.Status.CANCELED}:
        task.finished_at = timezone.now()
    task.save(update_fields=["status", "progress_percent", "error_message", "started_at", "finished_at", "updated_at"])
    return task


def run_novel_visualization_task(task_id: int) -> None:
    close_old_connections()
    try:
        task = _set_task_status(task_id, NovelVisualizationTask.Status.RUNNING, progress_percent=3)
        is_targeted = task.scene_mode == NovelVisualizationTask.SceneMode.TARGETED and bool(task.target_plot.strip())
        append_task_event(
            task_id,
            "task_started",
            {
                "message": "指定剧情单图任务已启动" if is_targeted else "小说可视化任务已启动",
                "progress_percent": 3,
            },
        )
        if not _run_image_only_retry(task_id, task):
            task_log(task_id, f"正在读取 EPUB：{task.book_filename}", 8)
            book_path = resolve_task_book_path(task)
            novel_text = extract_epub_text(book_path)
            llm_config = resolve_scene_llm_config(task.llm_channel_code)
            analysis_message = (
                f"正文提取完成，共 {len(novel_text):,} 字，开始使用 {llm_config.source_name} 分析指定剧情"
                if is_targeted
                else f"正文提取完成，共 {len(novel_text):,} 字，开始使用 {llm_config.source_name} 深度分析"
            )
            task_log(task_id, analysis_message, 18, text_length=len(novel_text), scene_mode=task.scene_mode)
            scenes = request_scene_plan(
                novel_text,
                task.llm_channel_code,
                target_plot=task.target_plot if is_targeted else "",
            )
            with transaction.atomic():
                task = NovelVisualizationTask.objects.select_for_update().get(pk=task_id)
                task.scenes_json = scenes
                task.progress_percent = 36
                task.save(update_fields=["scenes_json", "progress_percent", "updated_at"])
                NovelSceneImage.objects.filter(task=task).delete()
                for index, scene in enumerate(scenes, start=1):
                    NovelSceneImage.objects.create(
                        task=task,
                        scene_number=index,
                        title=scene["title"],
                        chapter=scene["chapter"],
                        story_significance=scene["story_significance"],
                        excerpt=scene["excerpt"],
                        visual_prompt=scene["visual_prompt"],
                    )
            append_task_event(
                task_id,
                "scene_plan_ready",
                {
                    "message": "已定位指定剧情镜头，开始生成单张图片" if is_targeted else f"已挑选 {len(scenes)} 个震撼场景，开始并发生成图片",
                    "progress_percent": 36,
                    "scenes": scenes,
                    "task": serialize_task(NovelVisualizationTask.objects.prefetch_related("images").get(pk=task_id), include_logs=False),
                },
            )
            asyncio.run(_generate_scene_images(task_id))
        task = NovelVisualizationTask.objects.prefetch_related("images").get(pk=task_id)
        succeeded = task.images.filter(status=NovelSceneImage.Status.SUCCEEDED).count()
        failed = task.images.filter(status=NovelSceneImage.Status.FAILED).count()
        status = NovelVisualizationTask.Status.SUCCEEDED if succeeded else NovelVisualizationTask.Status.FAILED
        error = "" if succeeded else "全部场景图片生成失败"
        task = _set_task_status(task_id, status, progress_percent=100, error_message=error)
        event_type = "task_done" if succeeded else "task_failed"
        append_task_event(
            task_id,
            event_type,
            {
                "message": f"任务完成：成功 {succeeded} 张，失败 {failed} 张" if succeeded else error,
                "progress_percent": 100,
                "task": serialize_task(NovelVisualizationTask.objects.prefetch_related("images").get(pk=task_id), include_logs=False),
            },
        )
    except Exception as exc:
        logger.exception("小说可视化任务失败 task_id=%s", task_id)
        message = str(exc)[:255] or "小说可视化任务失败"
        _set_task_status(task_id, NovelVisualizationTask.Status.FAILED, progress_percent=100, error_message=message)
        append_task_event(
            task_id,
            "task_failed",
            {
                "message": message,
                "progress_percent": 100,
                "task": serialize_task(NovelVisualizationTask.objects.prefetch_related("images").get(pk=task_id), include_logs=False),
            },
        )
    finally:
        close_old_connections()


def dispatch_novel_visualization_task(task_id: int) -> None:
    if getattr(settings, "NOVELVIZ_RUN_INLINE", False):
        run_novel_visualization_task(task_id)
        return
    if getattr(settings, "NOVELVIZ_USE_SUBPROCESS", True):
        env = os.environ.copy()
        env.setdefault("DJANGO_SETTINGS_MODULE", os.environ.get("DJANGO_SETTINGS_MODULE", "config.settings.dev"))
        log_dir = Path(settings.MEDIA_ROOT) / "novelviz" / "logs"
        log_dir.mkdir(parents=True, exist_ok=True)
        log_path = log_dir / f"task-{task_id}.log"
        with log_path.open("ab") as log_file:
            subprocess.Popen(
                [sys.executable, str(settings.BASE_DIR / "manage.py"), "run_novelviz_task", str(task_id)],
                cwd=str(settings.BASE_DIR),
                env=env,
                stdout=log_file,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
        return
    thread = threading.Thread(target=run_novel_visualization_task, args=(task_id,), daemon=True)
    thread.start()
