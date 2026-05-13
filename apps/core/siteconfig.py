from __future__ import annotations

import logging
from typing import Any

from django.conf import settings
from django.core.cache import cache
from django.db import models

from apps.core.models import TimeStampedModel
from apps.core.security import decrypt_text, encrypt_text

logger = logging.getLogger(__name__)

CACHE_PREFIX = "siteconfig:"
CACHE_TTL = 60


class SiteConfig(TimeStampedModel):
    CATEGORY_GENERAL = "general"
    CATEGORY_SITE = "site"
    CATEGORY_EMAIL = "email"
    CATEGORY_RISK = "risk"

    CATEGORY_CHOICES = [
        (CATEGORY_GENERAL, "通用"),
        (CATEGORY_SITE, "站点"),
        (CATEGORY_EMAIL, "邮件验证"),
        (CATEGORY_RISK, "风控"),
    ]

    key = models.CharField("配置键", max_length=128, unique=True, db_index=True)
    value_encrypted = models.TextField("加密值", blank=True, default="")
    is_secret = models.BooleanField("敏感项", default=False)
    description = models.CharField("描述", max_length=255, blank=True, default="")
    category = models.CharField("分类", max_length=32, choices=CATEGORY_CHOICES, default=CATEGORY_GENERAL)

    class Meta:
        ordering = ["category", "key"]
        verbose_name = "系统配置"
        verbose_name_plural = "系统配置"

    def __str__(self) -> str:
        return self.key

    @property
    def value(self) -> str:
        if self.is_secret:
            return decrypt_text(self.value_encrypted)
        return self.value_encrypted

    @value.setter
    def value(self, raw: str) -> None:
        if self.is_secret:
            self.value_encrypted = encrypt_text(raw)
        else:
            self.value_encrypted = raw

    @property
    def masked_value(self) -> str:
        if not self.is_secret:
            return self.value_encrypted
        plain = self.value
        if not plain:
            return ""
        if len(plain) <= 8:
            return "******"
        return plain[:3] + "******" + plain[-3:]


CONFIG_KEYS: dict[str, dict[str, Any]] = {
    "site.name": {
        "category": SiteConfig.CATEGORY_SITE,
        "is_secret": False,
        "description": "站点名称",
    },
    "site.brand_name": {
        "category": SiteConfig.CATEGORY_SITE,
        "is_secret": False,
        "description": "品牌中文名",
    },
    "site.brand_english_name": {
        "category": SiteConfig.CATEGORY_SITE,
        "is_secret": False,
        "description": "品牌英文名",
    },
    "site.brand_tagline": {
        "category": SiteConfig.CATEGORY_SITE,
        "is_secret": False,
        "description": "品牌标语",
    },
    "site.register_enabled": {
        "category": SiteConfig.CATEGORY_SITE,
        "is_secret": False,
        "description": "是否开放注册 (1/0)",
    },
    "site.login_captcha_enabled": {
        "category": SiteConfig.CATEGORY_SITE,
        "is_secret": False,
        "description": "是否启用登录验证码 (1/0)",
    },
    "site.image_retention_days": {
        "category": SiteConfig.CATEGORY_SITE,
        "is_secret": False,
        "description": "图片保留天数",
    },
    "site.daily_user_task_limit": {
        "category": SiteConfig.CATEGORY_SITE,
        "is_secret": False,
        "description": "每日每用户任务上限",
    },
    "site.max_pending_tasks": {
        "category": SiteConfig.CATEGORY_SITE,
        "is_secret": False,
        "description": "最大并行任务数",
    },
    "site.default_channel_code": {
        "category": SiteConfig.CATEGORY_SITE,
        "is_secret": False,
        "description": "默认生图渠道编码",
    },
    "site.default_daily_free_quota": {
        "category": SiteConfig.CATEGORY_SITE,
        "is_secret": False,
        "description": "默认每日免费次数",
    },
    "site.workspace_release_version": {
        "category": SiteConfig.CATEGORY_SITE,
        "is_secret": False,
        "description": "工作台更新版本号",
    },
    "site.workspace_release_title": {
        "category": SiteConfig.CATEGORY_SITE,
        "is_secret": False,
        "description": "工作台更新标题",
    },
    "site.workspace_release_notes": {
        "category": SiteConfig.CATEGORY_SITE,
        "is_secret": False,
        "description": "工作台更新日志，按换行分条",
    },
    "site.workspace_release_published_at": {
        "category": SiteConfig.CATEGORY_SITE,
        "is_secret": False,
        "description": "工作台更新发布时间 ISO 字符串",
    },
    "site.release_push_key": {
        "category": SiteConfig.CATEGORY_GENERAL,
        "is_secret": True,
        "description": "内部更新推送接口 Access Key",
    },
    "site.release_push_secret": {
        "category": SiteConfig.CATEGORY_GENERAL,
        "is_secret": True,
        "description": "内部更新推送接口签名密钥",
    },
    "email.verification_enabled": {
        "category": SiteConfig.CATEGORY_EMAIL,
        "is_secret": False,
        "description": "是否启用邮箱验证 (1/0)",
    },
    "email.verification_code_length": {
        "category": SiteConfig.CATEGORY_EMAIL,
        "is_secret": False,
        "description": "验证码长度",
    },
    "email.verification_ttl_seconds": {
        "category": SiteConfig.CATEGORY_EMAIL,
        "is_secret": False,
        "description": "验证码有效期(秒)",
    },
    "email.verification_resend_seconds": {
        "category": SiteConfig.CATEGORY_EMAIL,
        "is_secret": False,
        "description": "验证码重发间隔(秒)",
    },
    "email.daily_limit_per_email": {
        "category": SiteConfig.CATEGORY_EMAIL,
        "is_secret": False,
        "description": "每邮箱每日发送上限",
    },
    "email.daily_limit_per_ip": {
        "category": SiteConfig.CATEGORY_EMAIL,
        "is_secret": False,
        "description": "每 IP 每日发送上限",
    },
    "risk.login_fail_limit": {
        "category": SiteConfig.CATEGORY_RISK,
        "is_secret": False,
        "description": "登录失败锁定阈值",
    },
    "risk.login_lock_minutes": {
        "category": SiteConfig.CATEGORY_RISK,
        "is_secret": False,
        "description": "登录锁定分钟数",
    },
    "risk.task_window_seconds": {
        "category": SiteConfig.CATEGORY_RISK,
        "is_secret": False,
        "description": "任务频率窗口(秒)",
    },
    "risk.task_limit_per_window": {
        "category": SiteConfig.CATEGORY_RISK,
        "is_secret": False,
        "description": "窗口内任务上限",
    },
}


def _cache_key(key: str) -> str:
    return f"{CACHE_PREFIX}{key}"


def get_config(key: str, default: str | None = None) -> str | None:
    cached = cache.get(_cache_key(key))
    if cached is not None:
        return cached if cached != "__NONE__" else default

    try:
        obj = SiteConfig.objects.get(key=key)
        val = obj.value
        if val != "":
            cache.set(_cache_key(key), val, CACHE_TTL)
            return val
    except SiteConfig.DoesNotExist:
        pass
    except Exception:
        logger.warning("SiteConfig lookup failed for key=%s", key, exc_info=True)

    settings_attr = key.replace(".", "_").upper()
    settings_val = getattr(settings, settings_attr, None)
    if settings_val is not None:
        val = str(settings_val)
        cache.set(_cache_key(key), val, CACHE_TTL)
        return val

    cache.set(_cache_key(key), "__NONE__", CACHE_TTL)
    return default


def get_config_bool(key: str, default: bool = False) -> bool:
    val = get_config(key)
    if val is None:
        return default
    return val.lower() in {"1", "true", "yes", "on"}


def get_config_int(key: str, default: int = 0) -> int:
    val = get_config(key)
    if val is None:
        return default
    try:
        return int(val)
    except (TypeError, ValueError):
        return default


def set_config(
    key: str,
    value: str,
    *,
    is_secret: bool | None = None,
    description: str | None = None,
    category: str | None = None,
) -> SiteConfig:
    meta = CONFIG_KEYS.get(key, {})
    obj, _ = SiteConfig.objects.get_or_create(
        key=key,
        defaults={
            "is_secret": meta.get("is_secret", False) if is_secret is None else is_secret,
            "description": description or meta.get("description", ""),
            "category": category or meta.get("category", SiteConfig.CATEGORY_GENERAL),
        },
    )
    obj.is_secret = meta.get("is_secret", False) if is_secret is None else is_secret
    obj.value = value
    if description is not None:
        obj.description = description
    if category is not None:
        obj.category = category
    obj.save()
    cache.delete(_cache_key(key))
    return obj


def get_configs_by_category(category: str) -> list[SiteConfig]:
    return list(SiteConfig.objects.filter(category=category).order_by("key"))


def bulk_set_configs(items: list[dict]) -> int:
    count = 0
    for item in items:
        set_config(
            item["key"],
            str(item["value"]),
            is_secret=item.get("is_secret"),
            description=item.get("description"),
            category=item.get("category"),
        )
        count += 1
    return count


def clear_config_cache(key: str | None = None) -> None:
    if key:
        cache.delete(_cache_key(key))
        return
    for config_key in CONFIG_KEYS:
        cache.delete(_cache_key(config_key))
