from __future__ import annotations

import uuid
from datetime import timedelta

from django.conf import settings
from django.db import models
from django.utils import timezone

from apps.core.models import TimeStampedModel


def default_expire_at():
    return timezone.now() + timedelta(days=settings.IMAGE_RETENTION_DAYS)


def default_upload_expire_at():
    return timezone.now() + timedelta(
        seconds=getattr(settings, "RESUMABLE_UPLOAD_SESSION_TTL_SECONDS", 24 * 60 * 60)
    )


def generate_upload_id():
    return uuid.uuid4().hex


class GeneratedImage(TimeStampedModel):
    class Status(models.TextChoices):
        AVAILABLE = "available", "可用"
        EXPIRED = "expired", "已过期"
        DELETED = "deleted", "已删除"

    task = models.ForeignKey("generator.GenerateTask", on_delete=models.CASCADE, related_name="images", verbose_name="任务")
    user = models.ForeignKey("users.User", on_delete=models.CASCADE, related_name="generated_images", verbose_name="用户")
    image_index = models.PositiveIntegerField("序号", default=1)
    storage_provider = models.CharField("存储提供方", max_length=32, default="minio")
    bucket_name = models.CharField("桶名", max_length=128)
    object_key = models.CharField("对象 Key", max_length=255)
    mime_type = models.CharField("MIME", max_length=64, default="image/png")
    file_size = models.PositiveIntegerField("文件大小", default=0)
    width = models.PositiveIntegerField("宽", default=0)
    height = models.PositiveIntegerField("高", default=0)
    sha256 = models.CharField("SHA256", max_length=64)
    status = models.CharField("状态", max_length=16, choices=Status.choices, default=Status.AVAILABLE)
    expires_at = models.DateTimeField("过期时间", default=default_expire_at)
    deleted_at = models.DateTimeField("删除时间", blank=True, null=True)

    class Meta:
        ordering = ["-created_at", "-id"]
        indexes = [
            models.Index(fields=["user", "expires_at"]),
            models.Index(fields=["user", "-created_at"]),
        ]
        verbose_name = "历史图片"
        verbose_name_plural = "历史图片"


class UploadSession(TimeStampedModel):
    class Kind(models.TextChoices):
        GENERATION_REFERENCE_IMAGE = "generation_reference_image", "生图参考图"
        NOVEL_EPUB = "novel_epub", "小说 EPUB"

    class Status(models.TextChoices):
        UPLOADING = "uploading", "上传中"
        COMPLETED = "completed", "已完成"
        CANCELED = "canceled", "已取消"
        EXPIRED = "expired", "已过期"

    user = models.ForeignKey("users.User", on_delete=models.CASCADE, related_name="upload_sessions", verbose_name="用户")
    upload_id = models.CharField("上传会话 ID", max_length=32, unique=True, default=generate_upload_id)
    client_upload_id = models.CharField("客户端上传 ID", max_length=128, blank=True, default="")
    kind = models.CharField("上传类型", max_length=48, choices=Kind.choices)
    filename = models.CharField("文件名", max_length=255)
    content_type = models.CharField("Content-Type", max_length=128, blank=True, default="")
    total_size = models.PositiveBigIntegerField("文件总大小")
    chunk_size = models.PositiveIntegerField("分片大小", default=1024 * 1024)
    total_chunks = models.PositiveIntegerField("分片数量", default=0)
    uploaded_chunks = models.JSONField("已上传分片", default=list, blank=True)
    status = models.CharField("状态", max_length=16, choices=Status.choices, default=Status.UPLOADING)
    result_json = models.JSONField("完成结果", default=dict, blank=True)
    expires_at = models.DateTimeField("过期时间", default=default_upload_expire_at)
    completed_at = models.DateTimeField("完成时间", blank=True, null=True)

    class Meta:
        ordering = ["-created_at", "-id"]
        indexes = [
            models.Index(fields=["user", "kind", "client_upload_id"]),
            models.Index(fields=["status", "expires_at"]),
        ]
        verbose_name = "断点续传会话"
        verbose_name_plural = "断点续传会话"

    def __str__(self) -> str:
        return self.upload_id
