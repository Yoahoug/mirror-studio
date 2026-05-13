from __future__ import annotations

from django.db import models

from apps.core.models import TimeStampedModel


class AdminOperationLog(TimeStampedModel):
    admin_user = models.ForeignKey("users.User", on_delete=models.SET_NULL, null=True, blank=True, verbose_name="管理员")
    action = models.CharField("动作", max_length=64)
    target_type = models.CharField("目标类型", max_length=64)
    target_id = models.CharField("目标 ID", max_length=64, blank=True)
    detail_json = models.JSONField("详情", default=dict, blank=True)
    request_ip = models.GenericIPAddressField("请求 IP", blank=True, null=True)

    class Meta:
        verbose_name = "管理员操作日志"
        verbose_name_plural = "管理员操作日志"


class CallbackEventLog(TimeStampedModel):
    event_type = models.CharField("事件类型", max_length=64)
    source = models.CharField("来源", max_length=64)
    payload_json = models.JSONField("原始负载", default=dict, blank=True)
    success = models.BooleanField("是否成功", default=False)
    remark = models.CharField("备注", max_length=255, blank=True)

    class Meta:
        verbose_name = "回调事件日志"
        verbose_name_plural = "回调事件日志"

