from __future__ import annotations

import logging
import time

import requests
from django.conf import settings
from django.db import close_old_connections

from apps.mediahub.services import delete_storage_object, fetch_remote_image, store_generated_image

from .models import GenerateTask
from .providers import ProviderRequestError, get_adapter
from .services import (
    extend_task_lease,
    is_task_abort_requested,
    mark_task_failed,
    mark_task_running,
    mark_task_succeeded,
    requeue_failed_task,
)

logger = logging.getLogger(__name__)


def _is_retryable_error(exc: Exception) -> bool:
    if isinstance(exc, ProviderRequestError):
        if exc.status_code is None:
            return True
        return exc.status_code >= 500 or exc.status_code in {408, 409, 425, 429}
    return isinstance(exc, (requests.RequestException, TimeoutError))


def _task_should_abort(task_no: str) -> bool:
    return is_task_abort_requested(task_no)


def _generate_claimed_task(task: GenerateTask) -> str:
    adapter = get_adapter(task=task)
    logger.info(
        "开始执行生图任务 task_no=%s mode=%s adapter=%s channel=%s",
        task.task_no,
        task.generation_mode,
        adapter.__class__.__name__,
        getattr(task.channel, "channel_code", ""),
    )
    max_attempts = max(int(getattr(task.channel, "max_retries", 1) or 1), 1)
    last_exc: Exception | None = None
    last_raw_response: dict = {}
    last_request_id = ""
    attempts_used = 0
    try:
        for attempt in range(1, max_attempts + 1):
            attempts_used = attempt
            try:
                extend_task_lease(task.task_no)
                if _task_should_abort(task.task_no):
                    logger.info("生图任务已取消，停止执行 task_no=%s", task.task_no)
                    return task.task_no
                result = adapter.generate(task=task)
                extend_task_lease(task.task_no)
                if _task_should_abort(task.task_no):
                    logger.info("生图任务已取消，忽略上游返回 task_no=%s", task.task_no)
                    return task.task_no
                stored_images = []
                for index, image in enumerate(result.images, start=1):
                    content = image.content
                    if not content and image.remote_url:
                        content, mime_type = fetch_remote_image(image.remote_url)
                        image.mime_type = mime_type
                    if not content:
                        raise ValueError("上游未返回可存储图片内容")
                    stored_images.append(
                        store_generated_image(
                            user_id=task.user_id,
                            task_no=task.task_no,
                            image_index=index,
                            content=content,
                            mime_type=image.mime_type,
                        )
                    )
                provider_response = dict(result.raw_response)
                provider_response["_meta"] = {
                    "attempts_used": attempt,
                    "max_attempts": max_attempts,
                }
                mark_task_succeeded(
                    task_no=task.task_no,
                    provider_request_id=result.request_id,
                    provider_response_json=provider_response,
                    stored_images=stored_images,
                )
                logger.info(
                    "生图任务执行成功 task_no=%s images=%s request_id=%s",
                    task.task_no,
                    len(stored_images),
                    result.request_id,
                )
                return task.task_no
            except Exception as exc:
                if _task_should_abort(task.task_no):
                    logger.info("生图任务在执行过程中被终止 task_no=%s", task.task_no)
                    return task.task_no
                last_exc = exc
                if isinstance(exc, ProviderRequestError):
                    last_request_id = exc.request_id
                    last_raw_response = {
                        "status_code": exc.status_code,
                        "response": exc.raw_response,
                    }
                else:
                    last_raw_response = {
                        "exception_type": exc.__class__.__name__,
                    }
                if attempt >= max_attempts or not _is_retryable_error(exc):
                    raise
                time.sleep(min(1.5 * attempt, 3))
        return task.task_no
    except ProviderRequestError as exc:
        last_raw_response.update(
            {
                "adapter": adapter.__class__.__name__,
                "attempts_used": attempts_used,
                "max_attempts": max_attempts,
            }
        )
        if _task_should_abort(task.task_no):
            return task.task_no
        if getattr(task, "reference_object_key", ""):
            delete_storage_object(bucket_name=task.reference_bucket_name or settings.MINIO_BUCKET, object_key=task.reference_object_key)
        mark_task_failed(
            task_no=task.task_no,
            error_code="generate_failed",
            error_message=str(exc),
            provider_request_id=last_request_id,
            raw_response=last_raw_response,
        )
        raise
    except Exception as exc:
        last_raw_response.update(
            {
                "adapter": adapter.__class__.__name__,
                "attempts_used": attempts_used,
                "max_attempts": max_attempts,
            }
        )
        if _task_should_abort(task.task_no):
            return task.task_no
        if getattr(task, "reference_object_key", ""):
            delete_storage_object(bucket_name=task.reference_bucket_name or settings.MINIO_BUCKET, object_key=task.reference_object_key)
        mark_task_failed(
            task_no=task.task_no,
            error_code="generate_failed",
            error_message=str(last_exc or exc),
            provider_request_id=last_request_id,
            raw_response=last_raw_response,
        )
        raise


def generate_image_task(task_no: str) -> str:
    task = mark_task_running(task_no)
    if not task:
        return task_no
    return _generate_claimed_task(task)


def generate_next_image_task() -> str | None:
    task = mark_task_running()
    if not task:
        return None
    return _generate_claimed_task(task)


def retry_failed_storage_task(task_no: str) -> str:
    task = requeue_failed_task(task_no)
    if not task:
        return task_no
    dispatch_generate_image_task(task_no)
    return task_no


def run_generation_worker_once() -> str | None:
    close_old_connections()
    try:
        return generate_next_image_task()
    except Exception:
        logger.exception("后台生图 worker 执行失败")
        return None
    finally:
        close_old_connections()


def dispatch_generate_image_task(task_no: str) -> None:
    if getattr(settings, "GENERATION_TASKS_RUN_INLINE", False) or getattr(settings, "CELERY_TASK_ALWAYS_EAGER", False):
        try:
            generate_image_task(task_no)
        except Exception:
            logger.exception("同步生图任务执行失败 task_no=%s", task_no)
        return
    logger.info(
        "生图任务已入队，等待 worker 拉取 task_no=%s",
        task_no,
    )
