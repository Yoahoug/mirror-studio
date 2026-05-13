from __future__ import annotations

import logging
import time

from django.contrib.auth.mixins import LoginRequiredMixin
from django.core.exceptions import RequestDataTooBig
from django.core.paginator import Paginator
from django.http import HttpRequest
from django.shortcuts import get_object_or_404
from django.views import View

from apps.core.api import InvalidPayloadError, api_error, api_success, parse_request_payload
from apps.mediahub.services import store_reference_image

from .models import GenerateTask, GenerationTopic
from .services import (
    bump_task_progress,
    cancel_task,
    create_reference_upload_token,
    delete_task_history,
    normalize_reference_image,
    reconcile_task_runtime_state,
    serialize_task,
    serialize_topic,
    submit_generation_task,
)
from .tasks import dispatch_generate_image_task
from apps.novelviz.models import NovelSceneImage, NovelVisualizationTask

logger = logging.getLogger(__name__)


def _json_payload(request: HttpRequest) -> dict:
    return parse_request_payload(request)


class TopicListCreateAPIView(LoginRequiredMixin, View):
    def get(self, request):
        topics = GenerationTopic.objects.filter(user=request.user, is_archived=False).order_by("-last_task_at")[:50]
        return api_success([serialize_topic(topic) for topic in topics])

    def post(self, request):
        try:
            payload = _json_payload(request)
        except InvalidPayloadError as exc:
            return api_error(str(exc), code=400001)
        title = (payload.get("title") or "新的创作").strip()[:128]
        topic = GenerationTopic.objects.create(user=request.user, title=title)
        return api_success(serialize_topic(topic))


class ReferenceUploadAPIView(LoginRequiredMixin, View):
    def post(self, request):
        try:
            payload = _json_payload(request)
            content, mime_type, reference_image_name = normalize_reference_image(
                data_url=payload.get("reference_image_data_url", ""),
                name=payload.get("reference_image_name", ""),
            )
            if not content:
                raise ValueError("请先上传参考图")
            task_no = f"R{request.user.id}{int(time.time() * 1000)}"
            reference_meta = store_reference_image(
                user_id=request.user.id,
                task_no=task_no,
                content=content,
                mime_type=mime_type,
            )
            token = create_reference_upload_token(
                user_id=request.user.id,
                reference_image_name=reference_image_name,
                reference_meta=reference_meta,
            )
        except InvalidPayloadError as exc:
            return api_error(str(exc), code=400001)
        except RequestDataTooBig:
            return api_error("参考图过大，请压缩后再试", code=400100)
        except ValueError as exc:
            return api_error(str(exc), code=400100)
        return api_success(
            {
                "reference_upload_token": token,
                "reference_image_name": reference_image_name,
                "reference_image_attached": True,
            }
        )


class TaskListCreateAPIView(LoginRequiredMixin, View):
    def get(self, request):
        topic_id = request.GET.get("topic_id")
        keyword = (request.GET.get("q") or request.GET.get("keyword") or "").strip()
        try:
            page = max(int(request.GET.get("page", 1)), 1)
        except (TypeError, ValueError):
            page = 1
        try:
            page_size = int(request.GET.get("page_size", 24))
        except (TypeError, ValueError):
            page_size = 24
        page_size = min(max(page_size, 1), 48)

        queryset = GenerateTask.objects.filter(user=request.user, is_deleted=False).select_related("topic", "channel").prefetch_related("images")
        if topic_id:
            queryset = queryset.filter(topic_id=topic_id)
        if keyword:
            queryset = queryset.filter(prompt__icontains=keyword)

        paginator = Paginator(queryset, page_size)
        page_obj = paginator.get_page(page)
        return api_success(
            {
                "items": [
                    serialize_task(reconcile_task_runtime_state(task), user=request.user, include_urls=True)
                    for task in page_obj.object_list
                ],
                "pagination": {
                    "page": page_obj.number,
                    "page_size": page_size,
                    "total": paginator.count,
                    "total_pages": paginator.num_pages,
                    "has_next": page_obj.has_next(),
                    "has_previous": page_obj.has_previous(),
                },
            }
        )

    def post(self, request):
        try:
            payload = _json_payload(request)
            task, created = submit_generation_task(
                user=request.user,
                prompt=payload.get("prompt", ""),
                negative_prompt=payload.get("negative_prompt", ""),
                style_code=payload.get("style_code", "general"),
                size_code=payload.get("size_code", "1024x1024"),
                generation_mode=payload.get("generation_mode", GenerateTask.Mode.TEXT_TO_IMAGE),
                reference_image_data_url=payload.get("reference_image_data_url", ""),
                reference_image_name=payload.get("reference_image_name", ""),
                reference_upload_token=payload.get("reference_upload_token", ""),
                topic_id=payload.get("topic_id"),
                client_request_id=payload.get("client_request_id", ""),
                client_ip=request.client_ip,
            )
            if created:
                try:
                    dispatch_generate_image_task(task.task_no)
                except Exception:
                    logger.exception("提交后触发生图任务失败 task_no=%s", task.task_no)
                    task.refresh_from_db()
            task = self._wait_for_task(task=task, wait_seconds=payload.get("wait_seconds", 0))
        except InvalidPayloadError as exc:
            return api_error(str(exc), code=400001)
        except RequestDataTooBig:
            return api_error("参考图过大，请压缩后再试", code=400100)
        except ValueError as exc:
            return api_error(str(exc), code=400100)
        except PermissionError as exc:
            return api_error(str(exc), code=400403, status=403)
        return api_success(serialize_task(task, user=request.user, include_urls=True, include_reference=False))

    def _wait_for_task(self, *, task: GenerateTask, wait_seconds) -> GenerateTask:
        try:
            seconds = min(max(float(wait_seconds or 0), 0), 10)
        except (TypeError, ValueError):
            seconds = 0
        if seconds <= 0:
            task.refresh_from_db()
            return task

        deadline = time.monotonic() + seconds
        milestones = [35, 52, 68, 82, 90]
        step = 0
        terminal_statuses = {
            GenerateTask.Status.SUCCEEDED,
            GenerateTask.Status.FAILED,
            GenerateTask.Status.CANCELED,
        }
        while time.monotonic() < deadline:
            task = (
                GenerateTask.objects.filter(pk=task.pk)
                .select_related("topic")
                .prefetch_related("images")
                .first()
            )
            if not task:
                break
            if task.status in terminal_statuses or task.images.exists():
                return task
            if task.status in [GenerateTask.Status.QUEUED, GenerateTask.Status.RUNNING]:
                bump_task_progress(task, milestones[min(step, len(milestones) - 1)])
            step += 1
            time.sleep(0.5)

        task.refresh_from_db()
        return task


class TaskDetailAPIView(LoginRequiredMixin, View):
    def get(self, request, task_no: str):
        task = get_object_or_404(
            GenerateTask.objects.filter(user=request.user, is_deleted=False).select_related("topic", "channel").prefetch_related("images"),
            task_no=task_no,
        )
        task = reconcile_task_runtime_state(task)
        return api_success(serialize_task(task, user=request.user, include_urls=True, include_reference=False))

    def delete(self, request, task_no: str):
        try:
            task = delete_task_history(user=request.user, task_no=task_no)
        except ValueError as exc:
            return api_error(str(exc), code=400101)
        return api_success({"task_no": task.task_no, "deleted": True})


class TaskCancelAPIView(LoginRequiredMixin, View):
    def post(self, request, task_no: str):
        task = cancel_task(user=request.user, task_no=task_no)
        return api_success({"task_no": task.task_no, "status": task.status})


def _serialize_novel_history_task(task: NovelVisualizationTask) -> dict:
    images = []
    for image in task.images.all():
        is_available = image.status == NovelSceneImage.Status.SUCCEEDED and bool(image.image_url)
        preview_url = f"{image.image_url}?preview=1" if is_available else ""
        download_url = f"{image.image_url}?download=1" if is_available else ""
        images.append(
            {
                "id": image.id,
                "status": "available" if is_available else image.status,
                "width": 1024,
                "height": 1024,
                "expires_at": None,
                "preview_url": preview_url,
                "download_url": download_url,
                "image_url": image.image_url,
                "scene_number": image.scene_number,
                "title": image.title,
                "chapter": image.chapter,
                "story_significance": image.story_significance,
                "excerpt": image.excerpt,
            }
        )
    prompt = f"小说指定剧情：{task.book_filename}" if task.scene_mode == NovelVisualizationTask.SceneMode.TARGETED else f"小说可视化：{task.book_filename}"
    return {
        "task_no": f"novelviz:{task.id}",
        "task_kind": "novelviz",
        "novel_task_id": task.id,
        "status": task.status,
        "progress_percent": task.progress_percent,
        "generation_mode": "novel_visualization",
        "prompt": prompt,
        "negative_prompt": "",
        "style_code": "",
        "size_code": "1024x1024",
        "reference_image_name": task.book_filename,
        "reference_image_attached": False,
        "reference_image_data_url": "",
        "quota_cost": 0,
        "error_code": "",
        "error_message": task.error_message,
        "topic_id": None,
        "book_filename": task.book_filename,
        "scene_mode": task.scene_mode,
        "target_plot": task.target_plot,
        "submitted_at": task.created_at.isoformat() if task.created_at else None,
        "started_at": task.started_at.isoformat() if task.started_at else None,
        "finished_at": task.finished_at.isoformat() if task.finished_at else None,
        "images": images,
    }


class WorkspaceHistoryAPIView(LoginRequiredMixin, View):
    def get(self, request):
        keyword = (request.GET.get("q") or request.GET.get("keyword") or "").strip()
        try:
            page = max(int(request.GET.get("page", 1)), 1)
        except (TypeError, ValueError):
            page = 1
        try:
            page_size = int(request.GET.get("page_size", 24))
        except (TypeError, ValueError):
            page_size = 24
        page_size = min(max(page_size, 1), 48)

        generation_tasks = GenerateTask.objects.filter(user=request.user, is_deleted=False).select_related("topic", "channel").prefetch_related("images")
        novel_tasks = NovelVisualizationTask.objects.filter(user=request.user).prefetch_related("images")
        if keyword:
            generation_tasks = generation_tasks.filter(prompt__icontains=keyword)
            novel_tasks = novel_tasks.filter(book_filename__icontains=keyword)

        items = [
            (task.created_at, "generation", task)
            for task in generation_tasks
        ] + [
            (task.created_at, "novelviz", task)
            for task in novel_tasks
        ]
        items.sort(key=lambda item: (item[0], getattr(item[2], "id", 0)), reverse=True)

        paginator = Paginator(items, page_size)
        page_obj = paginator.get_page(page)
        serialized = []
        for _, kind, task in page_obj.object_list:
            if kind == "novelviz":
                serialized.append(_serialize_novel_history_task(task))
            else:
                serialized.append(serialize_task(reconcile_task_runtime_state(task), user=request.user, include_urls=True))
        return api_success(
            {
                "items": serialized,
                "pagination": {
                    "page": page_obj.number,
                    "page_size": page_size,
                    "total": paginator.count,
                    "total_pages": paginator.num_pages,
                    "has_next": page_obj.has_next(),
                    "has_previous": page_obj.has_previous(),
                },
            }
        )
