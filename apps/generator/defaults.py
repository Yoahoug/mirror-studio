from __future__ import annotations

from django.conf import settings

from .models import ImageChannel


def ensure_default_image_channel() -> ImageChannel:
    """确保默认生图渠道为通用图片渠道，优先使用 OpenAI 兼容接口配置。"""
    channel_code = getattr(settings, "DEFAULT_CHANNEL_CODE", "default") or "default"
    channel, _ = ImageChannel.objects.update_or_create(
        channel_code=channel_code,
        defaults={
            "name": settings.PROVIDER_NAME,
            "channel_type": ImageChannel.ChannelType.IMAGE,
            "provider_name": settings.PROVIDER_VENDOR,
            "base_url": settings.PROVIDER_BASE_URL,
            "api_path": settings.PROVIDER_API_PATH,
            "model_name": settings.PROVIDER_MODEL_NAME,
            "timeout_seconds": int(settings.PROVIDER_TIMEOUT_SECONDS),
            "max_retries": int(settings.PROVIDER_MAX_RETRIES),
            "is_active": True,
            "priority": 1,
        },
    )
    if settings.PROVIDER_API_KEY:
        channel.api_key = settings.PROVIDER_API_KEY
        channel.save(update_fields=["api_key_encrypted", "updated_at"])
    return channel


def ensure_default_llm_channel() -> ImageChannel | None:
    """确保工作台和小说可视化可复用同一个 OpenAI 兼容 LLM 渠道。"""
    if not settings.LLM_API_KEY:
        return None
    channel, _ = ImageChannel.objects.update_or_create(
        channel_code=settings.LLM_CHANNEL_CODE,
        defaults={
            "name": settings.LLM_PROVIDER_NAME,
            "channel_type": ImageChannel.ChannelType.LLM,
            "provider_name": settings.LLM_PROVIDER_VENDOR,
            "base_url": settings.LLM_BASE_URL,
            "api_path": settings.LLM_API_PATH,
            "edit_api_path": "",
            "model_name": settings.LLM_MODEL_NAME,
            "timeout_seconds": int(settings.LLM_TIMEOUT_SECONDS),
            "max_retries": int(settings.LLM_MAX_RETRIES),
            "is_active": True,
            "priority": int(settings.LLM_PRIORITY),
        },
    )
    channel.api_key = settings.LLM_API_KEY
    channel.save(update_fields=["api_key_encrypted", "updated_at"])
    return channel
