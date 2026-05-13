from __future__ import annotations

from django.db import models
from django.utils import timezone

from apps.core.models import TimeStampedModel


class SensitiveWordRule(TimeStampedModel):
    class MatchMode(models.TextChoices):
        CONTAINS = "contains", "包含"
        EXACT = "exact", "完全匹配"

    keyword = models.CharField("关键词", max_length=100, unique=True)
    match_mode = models.CharField("匹配方式", max_length=16, choices=MatchMode.choices, default=MatchMode.CONTAINS)
    is_active = models.BooleanField("启用", default=True)
    remark = models.CharField("备注", max_length=255, blank=True)

    class Meta:
        verbose_name = "敏感词规则"
        verbose_name_plural = "敏感词规则"

    def __str__(self) -> str:
        return self.keyword


class IPAccessRule(TimeStampedModel):
    ip_address = models.GenericIPAddressField("IP", unique=True)
    is_blocked = models.BooleanField("是否封禁", default=True)
    reason = models.CharField("原因", max_length=255, blank=True)
    expires_at = models.DateTimeField("过期时间", blank=True, null=True)

    class Meta:
        verbose_name = "IP 访问规则"
        verbose_name_plural = "IP 访问规则"

    @property
    def is_effective(self) -> bool:
        return self.is_blocked and (self.expires_at is None or self.expires_at > timezone.now())


class RiskEvent(TimeStampedModel):
    event_type = models.CharField("事件类型", max_length=64)
    user = models.ForeignKey("users.User", on_delete=models.SET_NULL, null=True, blank=True, verbose_name="用户")
    request_ip = models.GenericIPAddressField("请求 IP", blank=True, null=True)
    detail_json = models.JSONField("详情", default=dict, blank=True)

    class Meta:
        verbose_name = "风控事件"
        verbose_name_plural = "风控事件"

