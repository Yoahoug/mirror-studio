from __future__ import annotations

from django.db import models
from django.db.models import Q
from django.utils import timezone

from apps.core.models import TimeStampedModel
from apps.core.security import decrypt_text, encrypt_text


class ImageChannel(TimeStampedModel):
    class ChannelType(models.TextChoices):
        IMAGE = "image", "生图模型"
        LLM = "llm", "LLM模型"

    # Which request dialect / response shape the upstream speaks. OpenAI-compatible
    # is the default (size + b64_json or url, multipart edits). SiliconFlow Qwen
    # image models need `image=<base64>` on /images/generations and `image_size`,
    # and return a remote `url` that must be fetched. Keeping this configurable lets
    # an operator point a channel at either without code changes.
    class ProviderVendor(models.TextChoices):
        OPENAI = "openai", "OpenAI 兼容"
        SILICONFLOW = "siliconflow", "硅基流动"

    name = models.CharField("渠道名称", max_length=64)
    channel_code = models.CharField("渠道编码", max_length=32, unique=True)
    channel_type = models.CharField("模型类型", max_length=16, choices=ChannelType.choices, default=ChannelType.IMAGE)
    provider_vendor = models.CharField(
        "对接方言", max_length=24, choices=ProviderVendor.choices, default=ProviderVendor.OPENAI
    )
    provider_name = models.CharField("对接方", max_length=64, blank=True, default="")
    base_url = models.URLField("基础地址")
    api_path = models.CharField("接口路径", max_length=128, default="/images/generations")
    edit_api_path = models.CharField("图生图接口路径", max_length=128, blank=True, default="")
    api_key_encrypted = models.TextField("加密密钥", blank=True)
    model_name = models.CharField("模型名", max_length=64)
    timeout_seconds = models.PositiveIntegerField("超时秒数", default=60)
    max_retries = models.PositiveIntegerField("最大重试", default=1)
    is_active = models.BooleanField("启用", default=True)
    priority = models.PositiveIntegerField("优先级", default=1)

    class Meta:
        ordering = ["priority", "id"]
        verbose_name = "对接方模型"
        verbose_name_plural = "对接方模型"

    @property
    def api_key(self) -> str:
        return decrypt_text(self.api_key_encrypted)

    @api_key.setter
    def api_key(self, value: str) -> None:
        self.api_key_encrypted = encrypt_text(value)

    def __str__(self) -> str:
        return self.name


class GenerationTopic(TimeStampedModel):
    user = models.ForeignKey("users.User", on_delete=models.CASCADE, related_name="generation_topics", verbose_name="用户")
    title = models.CharField("标题", max_length=128)
    cover_image_id = models.BigIntegerField("封面图片 ID", blank=True, null=True)
    last_task_at = models.DateTimeField("最后任务时间", default=timezone.now)
    is_archived = models.BooleanField("归档", default=False)

    class Meta:
        ordering = ["-last_task_at", "-id"]
        verbose_name = "创作主题"
        verbose_name_plural = "创作主题"

    def __str__(self) -> str:
        return self.title


class GenerateTask(TimeStampedModel):
    class Mode(models.TextChoices):
        TEXT_TO_IMAGE = "text_to_image", "文生图"
        IMAGE_TO_IMAGE = "image_to_image", "图生图"

    class Status(models.TextChoices):
        QUEUED = "queued", "排队中"
        RUNNING = "running", "执行中"
        SUCCEEDED = "succeeded", "成功"
        FAILED = "failed", "失败"
        CANCELED = "canceled", "取消"

    user = models.ForeignKey("users.User", on_delete=models.CASCADE, related_name="generate_tasks", verbose_name="用户")
    topic = models.ForeignKey("generator.GenerationTopic", on_delete=models.SET_NULL, null=True, blank=True, related_name="tasks", verbose_name="主题")
    channel = models.ForeignKey("generator.ImageChannel", on_delete=models.PROTECT, related_name="tasks", verbose_name="渠道")
    task_no = models.CharField("任务号", max_length=32, unique=True)
    status = models.CharField("状态", max_length=16, choices=Status.choices, default=Status.QUEUED)
    progress_percent = models.PositiveSmallIntegerField("进度", default=0)
    generation_mode = models.CharField("生成模式", max_length=24, choices=Mode.choices, default=Mode.TEXT_TO_IMAGE)
    prompt = models.TextField("提示词")
    negative_prompt = models.TextField("反向提示词", blank=True)
    style_code = models.CharField("风格", max_length=32, default="general")
    size_code = models.CharField("尺寸", max_length=32, default="1024x1024")
    quality_code = models.CharField("质量", max_length=16, default="auto")
    reference_image_name = models.CharField("参考图文件名", max_length=255, blank=True)
    reference_image_data_url = models.TextField("参考图 Data URL", blank=True)
    reference_storage_provider = models.CharField("参考图存储提供方", max_length=32, blank=True, default="")
    reference_bucket_name = models.CharField("参考图桶名", max_length=128, blank=True, default="")
    reference_object_key = models.CharField("参考图对象 Key", max_length=255, blank=True, default="")
    reference_image_mime_type = models.CharField("参考图 MIME", max_length=64, blank=True, default="")
    reference_image_size = models.PositiveIntegerField("参考图大小", default=0)
    reference_image_width = models.PositiveIntegerField("参考图宽", default=0)
    reference_image_height = models.PositiveIntegerField("参考图高", default=0)
    reference_image_sha256 = models.CharField("参考图 SHA256", max_length=64, blank=True, default="")
    quota_cost = models.PositiveIntegerField("扣费次数", default=1)
    provider_request_id = models.CharField("上游请求 ID", max_length=128, blank=True)
    provider_response_json = models.JSONField("上游原始响应", default=dict, blank=True)
    error_code = models.CharField("错误码", max_length=64, blank=True)
    error_message = models.CharField("错误信息", max_length=255, blank=True)
    execution_attempts = models.PositiveIntegerField("执行尝试次数", default=0)
    submitted_at = models.DateTimeField("提交时间", auto_now_add=True)
    started_at = models.DateTimeField("开始时间", blank=True, null=True)
    finished_at = models.DateTimeField("完成时间", blank=True, null=True)
    lease_expires_at = models.DateTimeField("执行租约过期时间", blank=True, null=True)
    client_request_id = models.CharField("客户端请求 ID", max_length=64, blank=True)
    client_ip = models.GenericIPAddressField("客户端 IP", blank=True, null=True)
    quota_finalized = models.BooleanField("次数是否已最终结算", default=False)
    is_deleted = models.BooleanField("用户已删除", default=False)
    deleted_at = models.DateTimeField("删除时间", blank=True, null=True)

    class Meta:
        ordering = ["-created_at", "-id"]
        indexes = [
            models.Index(fields=["user", "-created_at"]),
            models.Index(fields=["user", "status", "created_at"]),
            models.Index(fields=["user", "is_deleted", "-created_at"]),
        ]
        constraints = [
            models.UniqueConstraint(
                fields=["user", "client_request_id"],
                condition=~Q(client_request_id=""),
                name="unique_nonblank_client_request_per_user",
            )
        ]
        verbose_name = "生图任务"
        verbose_name_plural = "生图任务"

    def __str__(self) -> str:
        return self.task_no
