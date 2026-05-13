from __future__ import annotations

from django.contrib.auth.models import AbstractUser
from django.db import models

from apps.core.models import TimeStampedModel


class User(AbstractUser):
    class Role(models.TextChoices):
        USER = "user", "普通用户"
        ADMIN = "admin", "管理员"

    class Status(models.TextChoices):
        ACTIVE = "active", "正常"
        DISABLED = "disabled", "禁用"
        LOCKED = "locked", "锁定"

    email = models.EmailField("邮箱", blank=True, null=True, unique=True)
    mobile = models.CharField("手机号", max_length=20, blank=True, null=True, unique=True)
    role = models.CharField("角色", max_length=16, choices=Role.choices, default=Role.USER)
    status = models.CharField("状态", max_length=16, choices=Status.choices, default=Status.ACTIVE)
    last_login_ip = models.GenericIPAddressField("最近登录 IP", blank=True, null=True)
    last_login_at = models.DateTimeField("最近登录时间", blank=True, null=True)

    def save(self, *args, **kwargs):
        if self.email:
            self.email = self.email.lower()
        else:
            self.email = None
        if not self.mobile:
            self.mobile = None
        if self.is_superuser:
            self.role = self.Role.ADMIN
        super().save(*args, **kwargs)

    @property
    def is_operator(self) -> bool:
        return self.is_staff or self.role == self.Role.ADMIN

    @property
    def is_active_status(self) -> bool:
        return self.status == self.Status.ACTIVE


class UserProfile(TimeStampedModel):
    user = models.OneToOneField("users.User", on_delete=models.CASCADE, related_name="profile", verbose_name="用户")
    nickname = models.CharField("昵称", max_length=64, blank=True)
    avatar = models.URLField("头像", blank=True)
    register_ip = models.GenericIPAddressField("注册 IP", blank=True, null=True)
    register_source = models.CharField("注册来源", max_length=64, blank=True, default="website")
    remark = models.TextField("备注", blank=True)

    class Meta:
        verbose_name = "用户资料"
        verbose_name_plural = "用户资料"

    def __str__(self) -> str:
        return f"{self.user.username} 的资料"
