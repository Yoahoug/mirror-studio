from __future__ import annotations

from django.db import models
from django.utils import timezone

from apps.core.models import TimeStampedModel


def default_reset_date():
    return timezone.localdate()


class QuotaAccount(TimeStampedModel):
    user = models.OneToOneField("users.User", on_delete=models.CASCADE, related_name="quota_account", verbose_name="用户")
    daily_quota = models.PositiveIntegerField("每日免费次数", default=3)
    available_quota = models.IntegerField("当前剩余次数", default=3)
    frozen_quota = models.IntegerField("冻结次数", default=0)
    total_generated_count = models.PositiveIntegerField("累计成功生成次数", default=0)
    last_reset_date = models.DateField("上次刷新日期", default=default_reset_date)

    class Meta:
        verbose_name = "免费额度账户"
        verbose_name_plural = "免费额度账户"

    def __str__(self) -> str:
        return f"{self.user.username} 免费额度"

    @property
    def total_consumed_quota(self) -> int:
        return self.total_generated_count

    @property
    def total_recharged_quota(self) -> int:
        return self.daily_quota


class QuotaLedger(TimeStampedModel):
    class ChangeType(models.TextChoices):
        RESET = "reset", "每日刷新"
        FREEZE = "freeze", "冻结"
        CONSUME = "consume", "消耗"
        UNFREEZE = "unfreeze", "解冻"
        ADMIN_ADJUST = "admin_adjust", "管理员调整"
        DAILY_QUOTA_UPDATE = "daily_quota_update", "每日额度调整"

    class Direction(models.TextChoices):
        IN = "in", "增加"
        OUT = "out", "减少"
        NONE = "none", "无变化"

    ledger_no = models.CharField("流水号", max_length=32, unique=True)
    user = models.ForeignKey("users.User", on_delete=models.CASCADE, related_name="quota_ledgers", verbose_name="用户")
    change_type = models.CharField("变更类型", max_length=32, choices=ChangeType.choices)
    direction = models.CharField("方向", max_length=8, choices=Direction.choices, default=Direction.NONE)
    amount = models.IntegerField("变更数量", default=0)
    before_available = models.IntegerField("变更前剩余次数", default=0)
    after_available = models.IntegerField("变更后剩余次数", default=0)
    before_frozen = models.IntegerField("变更前冻结次数", default=0)
    after_frozen = models.IntegerField("变更后冻结次数", default=0)
    related_task_no = models.CharField("关联任务号", max_length=32, blank=True)
    remark = models.CharField("备注", max_length=255, blank=True)

    class Meta:
        ordering = ["-created_at", "-id"]
        indexes = [models.Index(fields=["user", "-created_at"])]
        verbose_name = "免费额度流水"
        verbose_name_plural = "免费额度流水"
