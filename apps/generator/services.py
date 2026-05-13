from __future__ import annotations

import base64
import binascii
import io
import json
import secrets
from datetime import timedelta
from pathlib import Path
from re import IGNORECASE, compile as re_compile

from django.conf import settings
from django.core.signing import BadSignature, SignatureExpired, TimestampSigner
from django.db import transaction
from django.db.models import Q
from django.utils import timezone
from PIL import Image

from apps.audit.services import log_callback_event
from apps.billing.models import QuotaAccount
from apps.billing.services import consume_frozen_quota, freeze_quota_for_task, release_frozen_quota
from apps.mediahub.models import GeneratedImage
from apps.mediahub.services import delete_storage_object, issue_download_url, read_storage_object, store_reference_image
from apps.risk.services import ensure_prompt_safe, ensure_task_rate_limit

from .models import GenerateTask, GenerationTopic, ImageChannel

ALLOWED_SIZES = {"1024x1024", "1536x1024", "1024x1536"}
ALLOWED_STYLES = {"general", "cinematic", "anime", "photography"}
ALLOWED_GENERATION_MODES = {
    GenerateTask.Mode.TEXT_TO_IMAGE,
    GenerateTask.Mode.IMAGE_TO_IMAGE,
}
PUBLIC_FAILURE_MESSAGE = "生成失败，请稍后重试。若多次失败请联系管理员排查上游渠道。"
REFERENCE_IMAGE_DATA_URL_RE = re_compile(
    r"^data:(image/(png|jpeg|jpg|webp));base64,([A-Za-z0-9+/=\s]+)$",
    IGNORECASE,
)
REFERENCE_UPLOAD_TOKEN_SALT = "generator.reference-upload"


def generate_task_no(prefix: str = "G") -> str:
    return f"{prefix}{timezone.now():%Y%m%d%H%M%S}{secrets.token_hex(4).upper()}"


def get_default_channel() -> ImageChannel:
    channel = ImageChannel.objects.filter(
        is_active=True,
        channel_type=ImageChannel.ChannelType.IMAGE,
    ).order_by("priority", "id").first()
    if not channel:
        raise ValueError("未配置可用生图渠道")
    return channel


def clamp_progress(value: int) -> int:
    return max(0, min(100, int(value)))


def bump_task_progress(task: GenerateTask, progress_percent: int) -> GenerateTask:
    next_progress = clamp_progress(progress_percent)
    if task.progress_percent >= next_progress:
        return task
    task.progress_percent = next_progress
    task.save(update_fields=["progress_percent", "updated_at"])
    return task


def build_topic_title(prompt: str) -> str:
    stripped = (prompt or "").strip()
    return stripped[:24] or "新的创作"


def _validate_reference_image_content(*, content: bytes, mime_type: str, name: str = "") -> tuple[bytes, str, str]:
    normalized_mime_type = (mime_type or "").strip().lower().replace("jpg", "jpeg")
    raw_name = Path((name or "").strip() or "reference-image").name[:255]
    if normalized_mime_type not in {"image/png", "image/jpeg", "image/webp"}:
        raise ValueError("参考图仅支持 PNG、JPG、JPEG、WEBP")
    if len(content) > settings.MAX_REFERENCE_IMAGE_BYTES:
        raise ValueError("参考图过大，请压缩后再试")

    try:
        image = Image.open(io.BytesIO(content))
        image.verify()
    except Exception as exc:
        raise ValueError("参考图无法识别，请重新上传") from exc
    return content, normalized_mime_type, raw_name


def normalize_reference_image(*, data_url: str = "", name: str = "") -> tuple[bytes, str, str]:
    raw_data_url = (data_url or "").strip()
    if not raw_data_url:
        return b"", "", ""

    match = REFERENCE_IMAGE_DATA_URL_RE.match(raw_data_url)
    if not match:
        raise ValueError("参考图仅支持 PNG、JPG、JPEG、WEBP")

    mime_type = match.group(1).lower().replace("jpg", "jpeg")
    encoded = "".join(match.group(3).split())
    try:
        content = base64.b64decode(encoded, validate=True)
    except (binascii.Error, ValueError) as exc:
        raise ValueError("参考图数据格式错误") from exc

    return _validate_reference_image_content(content=content, mime_type=mime_type, name=name)


def create_reference_upload_token(*, user_id: int, reference_image_name: str, reference_meta: dict) -> str:
    signer = TimestampSigner(salt=REFERENCE_UPLOAD_TOKEN_SALT)
    payload = json.dumps(
        {
            "user_id": user_id,
            "reference_image_name": reference_image_name,
            **reference_meta,
        },
        separators=(",", ":"),
        ensure_ascii=True,
    )
    return signer.sign(payload)


def resolve_reference_upload_token(*, token: str, user_id: int) -> dict:
    signer = TimestampSigner(salt=REFERENCE_UPLOAD_TOKEN_SALT)
    max_age = int(getattr(settings, "GENERATION_REFERENCE_UPLOAD_TOKEN_MAX_AGE_SECONDS", 1800) or 1800)
    try:
        payload = signer.unsign(token, max_age=max_age)
        data = json.loads(payload)
    except (BadSignature, SignatureExpired, json.JSONDecodeError) as exc:
        raise ValueError("参考图上传凭证已失效，请重新上传") from exc
    if int(data.get("user_id") or 0) != int(user_id):
        raise PermissionError("参考图上传凭证无效")
    return data


@transaction.atomic
def get_or_create_topic(*, user, topic_id: int | None, prompt: str) -> GenerationTopic:
    if topic_id:
        return GenerationTopic.objects.select_for_update().get(id=topic_id, user=user)
    return GenerationTopic.objects.create(user=user, title=build_topic_title(prompt))


@transaction.atomic
def submit_generation_task(
    *,
    user,
    prompt: str,
    negative_prompt: str = "",
    style_code: str = "general",
    size_code: str = "1024x1024",
    topic_id: int | None = None,
    client_request_id: str = "",
    client_ip: str | None = None,
    generation_mode: str = GenerateTask.Mode.TEXT_TO_IMAGE,
    reference_image_data_url: str = "",
    reference_image_name: str = "",
    reference_upload_token: str = "",
):
    prompt = (prompt or "").strip()
    negative_prompt = (negative_prompt or "").strip()
    client_request_id = (client_request_id or "").strip()[:64]
    style_code = style_code or "general"
    size_code = size_code or "1024x1024"
    generation_mode = generation_mode or GenerateTask.Mode.TEXT_TO_IMAGE
    reference_image_name = (reference_image_name or "").strip()[:255]
    reference_upload_token = (reference_upload_token or "").strip()
    if len(prompt) > 4000:
        raise ValueError("Prompt 过长")
    if len(negative_prompt) > 2000:
        raise ValueError("反向提示词过长")
    if size_code not in ALLOWED_SIZES:
        raise ValueError("不支持的尺寸")
    if style_code not in ALLOWED_STYLES:
        raise ValueError("不支持的风格")
    if generation_mode not in ALLOWED_GENERATION_MODES:
        raise ValueError("不支持的生成模式")
    reference_source_bucket_name = ""
    reference_source_object_key = ""
    if reference_upload_token:
        token_payload = resolve_reference_upload_token(token=reference_upload_token, user_id=user.id)
        normalized_reference_content = b""
        normalized_reference_mime_type = str(token_payload.get("reference_image_mime_type") or "")
        normalized_reference_name = str(token_payload.get("reference_image_name") or reference_image_name)
        reference_source_bucket_name = str(token_payload.get("reference_bucket_name") or settings.MINIO_BUCKET)
        reference_source_object_key = str(token_payload.get("reference_object_key") or "")
    else:
        normalized_reference_content, normalized_reference_mime_type, normalized_reference_name = normalize_reference_image(
            data_url=reference_image_data_url,
            name=reference_image_name,
        )
    if generation_mode == GenerateTask.Mode.IMAGE_TO_IMAGE and not normalized_reference_content:
        if not reference_source_object_key:
            raise ValueError("图生图模式需要上传参考图")
    if generation_mode == GenerateTask.Mode.TEXT_TO_IMAGE:
        normalized_reference_content = b""
        normalized_reference_mime_type = ""
        normalized_reference_name = ""
        reference_source_bucket_name = ""
        reference_source_object_key = ""
    QuotaAccount.objects.select_for_update().get_or_create(user=user)
    if client_request_id:
        existing = GenerateTask.objects.filter(user=user, client_request_id=client_request_id).first()
        if existing:
            return existing, False
    ensure_prompt_safe(prompt, user=user, ip=client_ip)
    ensure_task_rate_limit(user, ip=client_ip)
    today = timezone.localdate()
    daily_count = GenerateTask.objects.filter(user=user, created_at__date=today).count()
    if daily_count >= settings.DAILY_USER_TASK_LIMIT:
        raise ValueError("已达到今日任务上限")
    pending_count = GenerateTask.objects.filter(
        user=user,
        status__in=[GenerateTask.Status.QUEUED, GenerateTask.Status.RUNNING],
    ).count()
    if pending_count >= settings.MAX_PENDING_TASKS:
        raise ValueError("未完成任务过多，请稍后再试")
    channel = get_default_channel()
    task_no = generate_task_no()
    topic = get_or_create_topic(user=user, topic_id=topic_id, prompt=prompt)
    freeze_quota_for_task(user=user, task_no=task_no, amount=1)
    task = GenerateTask.objects.create(
        user=user,
        topic=topic,
        channel=channel,
        task_no=task_no,
        progress_percent=8,
        generation_mode=generation_mode,
        prompt=prompt,
        negative_prompt=negative_prompt,
        style_code=style_code,
        size_code=size_code,
        reference_image_name=normalized_reference_name,
        reference_image_data_url="",
        client_request_id=client_request_id,
        client_ip=client_ip,
    )
    if reference_source_object_key:
        normalized_reference_content = read_storage_object(
            bucket_name=reference_source_bucket_name,
            object_key=reference_source_object_key,
        )
        normalized_reference_content, normalized_reference_mime_type, normalized_reference_name = _validate_reference_image_content(
            content=normalized_reference_content,
            mime_type=normalized_reference_mime_type,
            name=normalized_reference_name,
        )
    if normalized_reference_content:
        reference_meta = store_reference_image(
            user_id=user.id,
            task_no=task_no,
            content=normalized_reference_content,
            mime_type=normalized_reference_mime_type,
        )
        for field, value in reference_meta.items():
            setattr(task, field, value)
        task.save(update_fields=[*reference_meta.keys(), "updated_at"])
    if reference_source_object_key:
        delete_storage_object(bucket_name=reference_source_bucket_name, object_key=reference_source_object_key)
    topic.last_task_at = timezone.now()
    topic.save(update_fields=["last_task_at", "updated_at"])
    return task, True


def _task_lease_timeout_seconds(task: GenerateTask) -> int:
    return max(_stalled_task_timeout_seconds(task), 180)


@transaction.atomic
def mark_task_running(task_no: str | None = None) -> GenerateTask | None:
    now = timezone.now()
    queryset = GenerateTask.objects.select_for_update().select_related("user", "channel").filter(is_deleted=False)
    if task_no:
        queryset = queryset.filter(task_no=task_no).filter(
            Q(status=GenerateTask.Status.QUEUED)
            | Q(status=GenerateTask.Status.RUNNING, lease_expires_at__isnull=True)
            | Q(status=GenerateTask.Status.RUNNING, lease_expires_at__lte=now)
        )
    else:
        queryset = queryset.filter(
            Q(status=GenerateTask.Status.QUEUED)
            | Q(status=GenerateTask.Status.RUNNING, lease_expires_at__lte=now)
        ).order_by("submitted_at", "id")
    task = queryset.first()
    if not task:
        return None
    task.status = GenerateTask.Status.RUNNING
    task.progress_percent = max(task.progress_percent, 25)
    task.started_at = task.started_at or now
    task.finished_at = None
    task.lease_expires_at = now + timedelta(seconds=_task_lease_timeout_seconds(task))
    task.execution_attempts += 1
    task.save(
        update_fields=[
            "status",
            "progress_percent",
            "started_at",
            "finished_at",
            "lease_expires_at",
            "execution_attempts",
            "updated_at",
        ]
    )
    return task


@transaction.atomic
def requeue_task_after_worker_loss(task_no: str) -> GenerateTask:
    task = GenerateTask.objects.select_for_update().get(task_no=task_no)
    if task.status != GenerateTask.Status.RUNNING:
        return task
    task.status = GenerateTask.Status.QUEUED
    task.progress_percent = max(min(task.progress_percent, 20), 8)
    task.lease_expires_at = None
    task.save(update_fields=["status", "progress_percent", "lease_expires_at", "updated_at"])
    return task


@transaction.atomic
def requeue_failed_task(task_no: str) -> GenerateTask | None:
    task = GenerateTask.objects.select_for_update().select_related("user").filter(task_no=task_no, is_deleted=False).first()
    if not task or task.status != GenerateTask.Status.FAILED:
        return task
    freeze_quota_for_task(user=task.user, task_no=task.task_no, amount=task.quota_cost)
    task.status = GenerateTask.Status.QUEUED
    task.progress_percent = max(min(task.progress_percent, 20), 8)
    task.error_code = ""
    task.error_message = ""
    task.provider_request_id = ""
    task.provider_response_json = {}
    task.finished_at = None
    task.lease_expires_at = None
    task.quota_finalized = False
    task.save(
        update_fields=[
            "status",
            "progress_percent",
            "error_code",
            "error_message",
            "provider_request_id",
            "provider_response_json",
            "finished_at",
            "lease_expires_at",
            "quota_finalized",
            "updated_at",
        ]
    )
    return task


@transaction.atomic
def extend_task_lease(task_no: str) -> GenerateTask | None:
    task = (
        GenerateTask.objects.select_for_update()
        .select_related("channel")
        .filter(task_no=task_no, status=GenerateTask.Status.RUNNING, is_deleted=False)
        .first()
    )
    if not task:
        return None
    task.lease_expires_at = timezone.now() + timedelta(seconds=_task_lease_timeout_seconds(task))
    task.save(update_fields=["lease_expires_at", "updated_at"])
    return task


def build_public_error_message(task: GenerateTask) -> str:
    if task.status == GenerateTask.Status.FAILED:
        return PUBLIC_FAILURE_MESSAGE
    if task.status == GenerateTask.Status.CANCELED:
        return "任务已取消"
    return task.error_message


def _stalled_task_timeout_seconds(task: GenerateTask) -> int:
    channel_timeout = int(getattr(getattr(task, "channel", None), "timeout_seconds", 60) or 60)
    poll_timeout = int(getattr(settings, "YUNMENG_POLL_TIMEOUT_SECONDS", 0) or 0)
    running_timeout = max(channel_timeout + 120, poll_timeout + 60, 180)
    if task.generation_mode == GenerateTask.Mode.IMAGE_TO_IMAGE:
        running_timeout = max(
            running_timeout,
            int(getattr(settings, "OPENAI_IMAGE_EDIT_TIMEOUT_SECONDS", 420) or 420) + 60,
        )
    return running_timeout


def reconcile_task_runtime_state(task: GenerateTask) -> GenerateTask:
    if task.status == GenerateTask.Status.QUEUED:
        return task
    if task.status != GenerateTask.Status.RUNNING:
        return task
    reference_time = task.started_at or task.created_at or task.submitted_at
    if not reference_time:
        return task
    now = timezone.now()
    if task.lease_expires_at and task.lease_expires_at <= now:
        if (now - reference_time).total_seconds() < _stalled_task_timeout_seconds(task):
            return requeue_task_after_worker_loss(task.task_no)
    if (now - reference_time).total_seconds() < _stalled_task_timeout_seconds(task):
        return task
    return mark_task_failed(
        task_no=task.task_no,
        error_code="task_stalled",
        error_message="任务执行中断，已自动结束，请重新提交。",
        raw_response={
            "stalled": True,
            "previous_status": task.status,
            "reference_time": reference_time.isoformat(),
        },
    )


def reconcile_recent_tasks(tasks: list[GenerateTask]) -> list[GenerateTask]:
    return [reconcile_task_runtime_state(task) for task in tasks]


def is_task_abort_requested(task_no: str) -> bool:
    state = GenerateTask.objects.filter(task_no=task_no).values("status", "is_deleted").first()
    if not state:
        return True
    return bool(state["is_deleted"] or state["status"] == GenerateTask.Status.CANCELED)


@transaction.atomic
def mark_task_failed(
    *,
    task_no: str,
    error_code: str,
    error_message: str,
    provider_request_id: str = "",
    raw_response: dict | None = None,
) -> GenerateTask:
    task = GenerateTask.objects.select_for_update().select_related("user").get(task_no=task_no)
    if task.status == GenerateTask.Status.SUCCEEDED:
        return task
    if task.status == GenerateTask.Status.CANCELED and task.quota_finalized:
        return task
    if not task.quota_finalized:
        release_frozen_quota(user=task.user, task_no=task.task_no, amount=task.quota_cost)
        task.quota_finalized = True
    task.status = GenerateTask.Status.FAILED
    task.progress_percent = 100
    task.error_code = error_code
    task.error_message = error_message[:255]
    if provider_request_id:
        task.provider_request_id = provider_request_id[:128]
    task.provider_response_json = {
        "failure": raw_response or {},
        "error_code": error_code,
        "error_message": error_message,
    }
    task.finished_at = timezone.now()
    task.lease_expires_at = None
    task.save(
        update_fields=[
            "status",
            "progress_percent",
            "error_code",
            "error_message",
            "provider_request_id",
            "provider_response_json",
            "finished_at",
            "lease_expires_at",
            "quota_finalized",
            "updated_at",
        ]
    )
    log_callback_event(
        event_type="generation_failed",
        source="generator",
        payload={
            "task_no": task.task_no,
            "error_code": error_code,
            "error_message": error_message,
            "provider_request_id": provider_request_id,
            "raw_response": raw_response or {},
        },
        success=False,
        remark=error_message[:255],
    )
    return task


@transaction.atomic
def mark_task_succeeded(*, task_no: str, provider_request_id: str, provider_response_json: dict, stored_images: list[dict]) -> GenerateTask:
    task = GenerateTask.objects.select_for_update().select_related("user").get(task_no=task_no)
    if task.status == GenerateTask.Status.SUCCEEDED:
        return task
    created_images = []
    for meta in stored_images:
        created_images.append(GeneratedImage.objects.create(task=task, user=task.user, **meta))
    if not task.quota_finalized:
        consume_frozen_quota(user=task.user, task_no=task.task_no, amount=task.quota_cost)
        task.quota_finalized = True
    task.status = GenerateTask.Status.SUCCEEDED
    task.progress_percent = 100
    task.provider_request_id = provider_request_id
    task.provider_response_json = provider_response_json
    task.finished_at = timezone.now()
    task.lease_expires_at = None
    task.save(
        update_fields=[
            "status",
            "progress_percent",
            "provider_request_id",
            "provider_response_json",
            "finished_at",
            "lease_expires_at",
            "quota_finalized",
            "updated_at",
        ]
    )
    if task.topic and created_images:
        task.topic.cover_image_id = created_images[0].id
        task.topic.last_task_at = timezone.now()
        task.topic.save(update_fields=["cover_image_id", "last_task_at", "updated_at"])
    return task


@transaction.atomic
def cancel_task(*, user, task_no: str) -> GenerateTask:
    task = GenerateTask.objects.select_for_update().get(task_no=task_no, user=user, is_deleted=False)
    if task.status in [GenerateTask.Status.SUCCEEDED, GenerateTask.Status.FAILED, GenerateTask.Status.CANCELED]:
        return task
    task.status = GenerateTask.Status.CANCELED
    task.progress_percent = 100
    task.finished_at = timezone.now()
    task.lease_expires_at = None
    if not task.quota_finalized:
        release_frozen_quota(user=user, task_no=task.task_no, amount=task.quota_cost, remark="用户取消任务")
        task.quota_finalized = True
    task.save(update_fields=["status", "progress_percent", "finished_at", "lease_expires_at", "quota_finalized", "updated_at"])
    return task


@transaction.atomic
def delete_task_history(*, user, task_no: str) -> GenerateTask:
    task = GenerateTask.objects.select_for_update().get(task_no=task_no, user=user, is_deleted=False)
    if task.status in [GenerateTask.Status.QUEUED, GenerateTask.Status.RUNNING]:
        task.status = GenerateTask.Status.CANCELED
        task.progress_percent = 100
        task.finished_at = timezone.now()
        task.lease_expires_at = None
        if not task.quota_finalized:
            release_frozen_quota(user=user, task_no=task.task_no, amount=task.quota_cost, remark="用户删除运行中任务")
            task.quota_finalized = True
        task.save(update_fields=["status", "progress_percent", "finished_at", "lease_expires_at", "quota_finalized", "updated_at"])
    task.is_deleted = True
    task.deleted_at = timezone.now()
    task.save(update_fields=["is_deleted", "deleted_at", "updated_at"])
    if task.reference_object_key:
        delete_storage_object(bucket_name=task.reference_bucket_name or settings.MINIO_BUCKET, object_key=task.reference_object_key)
    task.images.filter(status=GeneratedImage.Status.AVAILABLE).update(
        status=GeneratedImage.Status.DELETED,
        deleted_at=task.deleted_at,
        updated_at=task.deleted_at,
    )
    return task


def serialize_topic(topic: GenerationTopic) -> dict:
    return {
        "id": topic.id,
        "title": topic.title,
        "cover_image_id": topic.cover_image_id,
        "last_task_at": topic.last_task_at.isoformat() if topic.last_task_at else None,
    }


def serialize_task(task: GenerateTask, *, user=None, include_urls: bool = False, include_reference: bool = False) -> dict:
    images = getattr(task, "images_cache", None)
    if images is None:
        images = list(task.images.all()) if hasattr(task, "images") else []
    serialized_images = []
    for image in images:
        item = {
            "id": image.id,
            "status": image.status,
            "width": image.width,
            "height": image.height,
            "expires_at": image.expires_at.isoformat() if image.expires_at else None,
        }
        if include_urls and user and image.status == GeneratedImage.Status.AVAILABLE:
            item["preview_url"] = issue_download_url(image=image, user=user, inline=True, preview=True)
            item["download_url"] = issue_download_url(image=image, user=user)
        serialized_images.append(item)
    return {
        "task_no": task.task_no,
        "status": task.status,
        "progress_percent": task.progress_percent,
        "generation_mode": task.generation_mode,
        "prompt": task.prompt,
        "negative_prompt": task.negative_prompt,
        "style_code": task.style_code,
        "size_code": task.size_code,
        "reference_image_name": task.reference_image_name,
        "reference_image_attached": bool(task.reference_object_key or task.reference_image_data_url),
        "reference_image_data_url": task.reference_image_data_url if include_reference else "",
        "quota_cost": task.quota_cost,
        "error_code": task.error_code,
        "error_message": build_public_error_message(task) if task.error_message else "",
        "topic_id": task.topic_id,
        "submitted_at": task.submitted_at.isoformat() if task.submitted_at else None,
        "started_at": task.started_at.isoformat() if task.started_at else None,
        "finished_at": task.finished_at.isoformat() if task.finished_at else None,
        "images": serialized_images,
    }
