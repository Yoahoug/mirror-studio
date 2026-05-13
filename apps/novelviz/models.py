from __future__ import annotations

from django.db import models
from django.utils import timezone

from apps.core.models import TimeStampedModel


class NovelUploadedBook(TimeStampedModel):
    user = models.ForeignKey("users.User", on_delete=models.CASCADE, related_name="novel_uploaded_books", verbose_name="用户")
    display_name = models.CharField("显示文件名", max_length=255)
    storage_path = models.CharField("存储路径", max_length=500)
    file_size = models.PositiveIntegerField("文件大小", default=0)
    sha256 = models.CharField("SHA256", max_length=64, blank=True)

    class Meta:
        ordering = ["-created_at", "-id"]
        indexes = [
            models.Index(fields=["user", "-created_at"]),
        ]
        verbose_name = "小说上传 EPUB"
        verbose_name_plural = "小说上传 EPUB"

    def __str__(self) -> str:
        return self.display_name


class NovelVisualizationTask(TimeStampedModel):
    class Status(models.TextChoices):
        QUEUED = "queued", "排队中"
        RUNNING = "running", "执行中"
        SUCCEEDED = "succeeded", "成功"
        FAILED = "failed", "失败"
        CANCELED = "canceled", "取消"

    class BookSource(models.TextChoices):
        BUILTIN = "builtin", "项目内置"
        UPLOADED = "uploaded", "用户上传"

    class SceneMode(models.TextChoices):
        AUTO = "auto", "自动挑选场景"
        TARGETED = "targeted", "指定剧情单图"

    user = models.ForeignKey("users.User", on_delete=models.CASCADE, related_name="novel_visualization_tasks", verbose_name="用户")
    book_filename = models.CharField("EPUB 文件名", max_length=255)
    book_source = models.CharField("书籍来源", max_length=16, choices=BookSource.choices, default=BookSource.BUILTIN)
    uploaded_book = models.ForeignKey(
        NovelUploadedBook,
        on_delete=models.SET_NULL,
        related_name="tasks",
        verbose_name="上传 EPUB",
        blank=True,
        null=True,
    )
    llm_channel_code = models.CharField("LLM 渠道编码", max_length=64, blank=True)
    scene_mode = models.CharField("场景模式", max_length=16, choices=SceneMode.choices, default=SceneMode.AUTO)
    target_plot = models.TextField("指定剧情", blank=True)
    status = models.CharField("状态", max_length=16, choices=Status.choices, default=Status.QUEUED)
    progress_percent = models.PositiveSmallIntegerField("进度", default=0)
    logs = models.JSONField("日志", default=list, blank=True)
    scenes_json = models.JSONField("场景 JSON", default=list, blank=True)
    error_message = models.CharField("错误信息", max_length=255, blank=True)
    started_at = models.DateTimeField("开始时间", blank=True, null=True)
    finished_at = models.DateTimeField("完成时间", blank=True, null=True)
    last_event_id = models.PositiveIntegerField("最后事件 ID", default=0)

    class Meta:
        ordering = ["-created_at", "-id"]
        indexes = [
            models.Index(fields=["user", "-created_at"]),
            models.Index(fields=["user", "status", "-created_at"]),
        ]
        verbose_name = "小说可视化任务"
        verbose_name_plural = "小说可视化任务"

    def __str__(self) -> str:
        return f"{self.book_filename} #{self.pk}"


class NovelSceneImage(TimeStampedModel):
    class Status(models.TextChoices):
        PENDING = "pending", "待生成"
        RUNNING = "running", "生成中"
        SUCCEEDED = "succeeded", "成功"
        FAILED = "failed", "失败"

    task = models.ForeignKey(NovelVisualizationTask, on_delete=models.CASCADE, related_name="images", verbose_name="任务")
    scene_number = models.PositiveSmallIntegerField("场景编号")
    title = models.CharField("中文标题", max_length=160)
    chapter = models.CharField("所在章节", max_length=160, blank=True)
    story_significance = models.TextField("故事意义", blank=True)
    excerpt = models.TextField("原文摘录", blank=True)
    visual_prompt = models.TextField("英文视觉描述")
    status = models.CharField("状态", max_length=16, choices=Status.choices, default=Status.PENDING)
    error_message = models.CharField("错误信息", max_length=255, blank=True)
    image_path = models.CharField("图片路径", max_length=500, blank=True)
    image_url = models.CharField("图片 URL", max_length=600, blank=True)
    attempts = models.PositiveSmallIntegerField("尝试次数", default=0)
    finished_at = models.DateTimeField("完成时间", blank=True, null=True)

    class Meta:
        ordering = ["scene_number", "id"]
        indexes = [
            models.Index(fields=["task", "scene_number"]),
            models.Index(fields=["task", "status"]),
        ]
        constraints = [
            models.UniqueConstraint(fields=["task", "scene_number"], name="unique_novel_scene_number_per_task"),
        ]
        verbose_name = "小说场景图片"
        verbose_name_plural = "小说场景图片"

    def mark_finished(self) -> None:
        self.finished_at = timezone.now()
