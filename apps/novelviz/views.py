from __future__ import annotations

import io
import json
import re
import time
from pathlib import Path
from urllib.parse import quote

from django.conf import settings
from django.contrib.auth.mixins import LoginRequiredMixin
from django.http import FileResponse, Http404, HttpResponse, StreamingHttpResponse
from django.shortcuts import get_object_or_404
from django.views import View
from PIL import Image

from apps.core.api import InvalidPayloadError, api_error, api_success, parse_request_payload

from .models import NovelSceneImage, NovelVisualizationTask
from .services import (
    NovelVisualizationError,
    dispatch_novel_visualization_task,
    list_epub_books,
    _rescue_stalled_task,
    reset_task_for_retry,
    resolve_book_path,
    resolve_scene_llm_config,
    resolve_uploaded_book_path,
    serialize_uploaded_book,
    serialize_task,
    store_uploaded_epub,
)


def _serialize_book(book) -> dict:
    if isinstance(book, dict):
        return book
    return {
        "id": book.id,
        "source": book.source,
        "filename": book.filename,
        "display_name": book.display_name,
        "size": book.size,
        "uploaded_book_id": book.uploaded_book_id,
        "created_at": book.created_at,
    }


def _scene_filename(image: NovelSceneImage) -> str:
    stem = re.sub(r"\s+", " ", (image.title or "").strip())
    stem = re.sub(r'[\\/:*?"<>|]+', "", stem)[:48].strip()
    return f"{stem or f'scene-{image.scene_number:02d}'}.png"


def _content_disposition(filename: str, *, inline: bool) -> str:
    disposition = "inline" if inline else "attachment"
    return f'{disposition}; filename="novel-scene.png"; filename*=UTF-8\'\'{quote(filename)}'


def _build_scene_preview_content(image_path: Path, *, max_dimension: int = 768) -> tuple[bytes, str]:
    content = image_path.read_bytes()
    try:
        rendered = Image.open(io.BytesIO(content))
        rendered.load()
    except Exception:
        return content, "image/png"

    longest_edge = max(rendered.size)
    if longest_edge > max_dimension:
        rendered.thumbnail((max_dimension, max_dimension), Image.Resampling.LANCZOS)

    has_alpha = rendered.mode in {"RGBA", "LA"} or (rendered.mode == "P" and "transparency" in rendered.info)
    buffer = io.BytesIO()
    if has_alpha:
        rendered.save(buffer, format="PNG", optimize=True)
        return buffer.getvalue(), "image/png"

    rendered.convert("RGB").save(buffer, format="JPEG", quality=82, optimize=True, progressive=True)
    return buffer.getvalue(), "image/jpeg"


class NovelBookListAPIView(LoginRequiredMixin, View):
    def get(self, request):
        return api_success([_serialize_book(book) for book in list_epub_books(request.user)])


class NovelBookUploadAPIView(LoginRequiredMixin, View):
    def post(self, request):
        uploaded_file = request.FILES.get("file")
        if not uploaded_file:
            return api_error("请选择 EPUB 文件", code=400101)
        try:
            uploaded_book = store_uploaded_epub(uploaded_file, request.user)
        except ValueError as exc:
            return api_error(str(exc), code=400102)
        return api_success(serialize_uploaded_book(uploaded_book), status=201)


class NovelTaskListCreateAPIView(LoginRequiredMixin, View):
    def get(self, request):
        tasks = list(
            NovelVisualizationTask.objects.filter(user=request.user)
            .defer("logs")
            .prefetch_related("images")
            .order_by("-created_at")[:30]
        )
        for task in tasks:
            try:
                _rescue_stalled_task(task)
            except Exception:
                pass
        tasks = (
            NovelVisualizationTask.objects.filter(user=request.user)
            .defer("logs")
            .prefetch_related("images")
            .order_by("-created_at")[:30]
        )
        return api_success([serialize_task(task, include_logs=False) for task in tasks])

    def post(self, request):
        try:
            payload = parse_request_payload(request)
            book_source = str(payload.get("book_source") or NovelVisualizationTask.BookSource.BUILTIN).strip()
            book_filename = str(payload.get("book_filename") or "").strip()
            uploaded_book_id = int(payload.get("uploaded_book_id") or 0)
            llm_channel_code = str(payload.get("llm_channel_code") or "").strip()[:64]
            scene_mode = str(payload.get("scene_mode") or NovelVisualizationTask.SceneMode.AUTO).strip()
            target_plot = str(payload.get("target_plot") or "").strip()
            if book_source == NovelVisualizationTask.BookSource.UPLOADED:
                upload_path = resolve_uploaded_book_path(uploaded_book_id, request.user)
                book_filename = book_filename or upload_path.name
            else:
                book_source = NovelVisualizationTask.BookSource.BUILTIN
                uploaded_book_id = 0
                resolve_book_path(book_filename)
            if scene_mode not in {
                NovelVisualizationTask.SceneMode.AUTO,
                NovelVisualizationTask.SceneMode.TARGETED,
            }:
                scene_mode = NovelVisualizationTask.SceneMode.AUTO
            if scene_mode == NovelVisualizationTask.SceneMode.TARGETED:
                if len(target_plot) < 4:
                    return api_error("请填写想呈现的剧情", code=400103)
                target_plot = target_plot[:2000]
            else:
                target_plot = ""
            resolve_scene_llm_config(llm_channel_code)
        except InvalidPayloadError as exc:
            return api_error(str(exc), code=400001)
        except (TypeError, ValueError) as exc:
            return api_error(str(exc), code=400100)
        except Exception as exc:
            return api_error(str(exc), code=400500, status=500)
        if not settings.NOVELVIZ_IMAGE_API_KEY:
            return api_error("未配置小说生图模型 Key", code=400501, status=500)

        task = NovelVisualizationTask.objects.create(
            user=request.user,
            book_filename=book_filename,
            book_source=book_source,
            uploaded_book_id=uploaded_book_id or None,
            llm_channel_code=llm_channel_code,
            scene_mode=scene_mode,
            target_plot=target_plot,
            status=NovelVisualizationTask.Status.QUEUED,
            progress_percent=1,
        )
        dispatch_novel_visualization_task(task.id)
        task.refresh_from_db()
        return api_success(serialize_task(task), status=201)


class NovelTaskDetailAPIView(LoginRequiredMixin, View):
    def get(self, request, task_id: int):
        task = get_object_or_404(
            NovelVisualizationTask.objects.filter(user=request.user).prefetch_related("images"),
            id=task_id,
        )
        try:
            _rescue_stalled_task(task)
            task.refresh_from_db()
        except Exception:
            pass
        return api_success(serialize_task(task))

    def delete(self, request, task_id: int):
        task = get_object_or_404(NovelVisualizationTask.objects.filter(user=request.user), id=task_id)
        task.delete()
        return api_success({"task_id": task_id, "deleted": True})


class NovelTaskRetryAPIView(LoginRequiredMixin, View):
    def post(self, request, task_id: int):
        try:
            task = reset_task_for_retry(task_id, request.user)
        except NovelVisualizationTask.DoesNotExist:
            raise Http404("任务不存在")
        except (NovelVisualizationError, ValueError) as exc:
            return api_error(str(exc), code=400100)
        except Exception as exc:
            return api_error(str(exc), code=400500, status=500)

        dispatch_novel_visualization_task(task.id)
        task.refresh_from_db()
        return api_success(serialize_task(task), status=200)


class NovelTaskEventsView(LoginRequiredMixin, View):
    def get(self, request, task_id: int):
        task = get_object_or_404(NovelVisualizationTask.objects.filter(user=request.user), id=task_id)
        try:
            _rescue_stalled_task(task)
            task.refresh_from_db()
        except Exception:
            pass
        try:
            last_seen_id = int(request.GET.get("last_event_id") or 0)
        except (TypeError, ValueError):
            last_seen_id = 0

        def event_stream():
            nonlocal last_seen_id
            heartbeat_at = time.monotonic()
            while True:
                current = NovelVisualizationTask.objects.filter(id=task.id, user=request.user).first()
                if not current:
                    yield "event: task_failed\ndata: {\"message\":\"任务不存在\"}\n\n"
                    return
                logs = list(current.logs or [])
                for event in logs:
                    event_id = int(event.get("id") or 0)
                    if event_id <= last_seen_id:
                        continue
                    last_seen_id = event_id
                    event_name = event.get("type") or "log"
                    payload = event.get("payload") or {}
                    yield f"id: {event_id}\nevent: {event_name}\ndata: {json.dumps(payload, ensure_ascii=False)}\n\n"

                if current.status in {
                    NovelVisualizationTask.Status.SUCCEEDED,
                    NovelVisualizationTask.Status.FAILED,
                    NovelVisualizationTask.Status.CANCELED,
                } and last_seen_id >= current.last_event_id:
                    return

                if time.monotonic() - heartbeat_at >= 15:
                    heartbeat_at = time.monotonic()
                    yield ": heartbeat\n\n"
                time.sleep(max(float(settings.NOVELVIZ_SSE_POLL_SECONDS), 0.25))

        response = StreamingHttpResponse(event_stream(), content_type="text/event-stream")
        response["Cache-Control"] = "no-cache"
        response["X-Accel-Buffering"] = "no"
        return response


class NovelSceneImageView(LoginRequiredMixin, View):
    def get(self, request, task_id: int, image_id: int):
        image = get_object_or_404(
            NovelSceneImage.objects.filter(
                id=image_id,
                task_id=task_id,
                task__user=request.user,
                status=NovelSceneImage.Status.SUCCEEDED,
            ),
        )
        if not image.image_path:
            raise Http404("图片不存在")
        media_root = Path(settings.MEDIA_ROOT).resolve()
        image_path = (media_root / image.image_path).resolve()
        if media_root not in image_path.parents or not image_path.exists():
            raise Http404("图片不存在")

        preview = request.GET.get("preview") == "1"
        download = request.GET.get("download") == "1"
        inline = not download or request.GET.get("disposition") == "inline" or preview
        filename = _scene_filename(image)
        if preview:
            content, content_type = _build_scene_preview_content(image_path)
            response = HttpResponse(content, content_type=content_type)
        else:
            response = FileResponse(image_path.open("rb"), content_type="image/png")
        response["Content-Disposition"] = _content_disposition(filename, inline=inline)
        response["Cache-Control"] = "private, max-age=3600"
        return response
