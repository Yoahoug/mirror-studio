from __future__ import annotations

import hashlib
import math
import random
import string
from dataclasses import dataclass
from datetime import datetime, time, timedelta

from django.conf import settings
from django.core.cache import cache
from django.core.exceptions import ImproperlyConfigured, ValidationError
from django.core.mail import EmailMultiAlternatives
from django.utils import timezone
from django.utils.crypto import constant_time_compare

from apps.audit.services import log_callback_event
from apps.core.siteconfig import get_config_bool, get_config_int


class EmailVerificationRateLimitError(PermissionError):
    pass


class EmailVerificationSendError(RuntimeError):
    pass


def normalize_email(email: str) -> str:
    return (email or "").strip().lower()


def generate_email_verification_code(length: int | None = None) -> str:
    code_length = length or email_verification_code_length()
    return "".join(random.choice(string.digits) for _ in range(code_length))


def is_email_verification_enabled() -> bool:
    return get_config_bool("email.verification_enabled", settings.EMAIL_VERIFICATION_ENABLED)


def email_verification_code_length() -> int:
    return get_config_int("email.verification_code_length", settings.EMAIL_VERIFICATION_CODE_LENGTH)


def email_verification_ttl_seconds() -> int:
    return get_config_int("email.verification_ttl_seconds", settings.EMAIL_VERIFICATION_TTL_SECONDS)


def email_verification_resend_seconds() -> int:
    return get_config_int("email.verification_resend_seconds", settings.EMAIL_VERIFICATION_RESEND_SECONDS)


def email_verification_daily_limit_per_email() -> int:
    return get_config_int("email.daily_limit_per_email", settings.EMAIL_VERIFICATION_DAILY_LIMIT_PER_EMAIL)


def email_verification_daily_limit_per_ip() -> int:
    return get_config_int("email.daily_limit_per_ip", settings.EMAIL_VERIFICATION_DAILY_LIMIT_PER_IP)


def _code_key(email: str) -> str:
    return f"email-verification:code:{normalize_email(email)}"


def _cooldown_key(email: str) -> str:
    return f"email-verification:cooldown:{normalize_email(email)}"


def _daily_email_key(email: str, date_key: str) -> str:
    return f"email-verification:daily-email:{normalize_email(email)}:{date_key}"


def _daily_ip_key(ip: str, date_key: str) -> str:
    return f"email-verification:daily-ip:{ip}:{date_key}"


def _hash_code(code: str) -> str:
    return hashlib.sha256(code.encode("utf-8")).hexdigest()


def _increment_counter(key: str, ttl_seconds: int) -> int:
    cache.add(key, 0, timeout=ttl_seconds)
    try:
        return cache.incr(key)
    except ValueError:
        cache.set(key, 1, timeout=ttl_seconds)
        return 1


def _ensure_email_backend_configured() -> None:
    if settings.EMAIL_BACKEND == "django.core.mail.backends.console.EmailBackend":
        return
    missing = []
    if not settings.EMAIL_HOST:
        missing.append("EMAIL_HOST")
    if not settings.EMAIL_HOST_USER:
        missing.append("EMAIL_HOST_USER")
    if not settings.EMAIL_HOST_PASSWORD:
        missing.append("EMAIL_HOST_PASSWORD")
    if not settings.DEFAULT_FROM_EMAIL:
        missing.append("DEFAULT_FROM_EMAIL")
    if missing:
        raise ImproperlyConfigured(f"邮件发送配置不完整：缺少 {', '.join(missing)}")


def _using_tencent_ses() -> bool:
    provider = (getattr(settings, "EMAIL_PROVIDER", "") or "").strip().lower()
    if provider == "tencent_ses":
        return True
    return bool(
        getattr(settings, "TENCENTCLOUD_SECRET_ID", "")
        and getattr(settings, "TENCENTCLOUD_SECRET_KEY", "")
        and getattr(settings, "TENCENTCLOUD_SES_FROM_EMAIL", "")
        and getattr(settings, "TENCENTCLOUD_SES_VERIFICATION_TEMPLATE_ID", 0)
    )


def _check_send_limits(email: str, client_ip: str) -> None:
    if cache.get(_cooldown_key(email)):
        raise EmailVerificationRateLimitError(f"验证码发送过于频繁，请 {email_verification_resend_seconds()} 秒后再试")

    date_key = timezone.localdate().isoformat()
    email_daily_count = int(cache.get(_daily_email_key(email, date_key), 0) or 0)
    if email_daily_count >= email_verification_daily_limit_per_email():
        raise EmailVerificationRateLimitError("该邮箱今日获取验证码次数已达上限，请明天再试")

    if client_ip:
        ip_daily_count = int(cache.get(_daily_ip_key(client_ip, date_key), 0) or 0)
        if ip_daily_count >= email_verification_daily_limit_per_ip():
            raise EmailVerificationRateLimitError("当前 IP 今日获取验证码次数已达上限，请明天再试")


def _mark_send_limits(email: str, client_ip: str) -> None:
    date_key = timezone.localdate().isoformat()
    cache.set(_cooldown_key(email), True, email_verification_resend_seconds())
    seconds_to_tomorrow = max(
        math.ceil(
            (
                timezone.make_aware(datetime.combine(timezone.localdate() + timedelta(days=1), time.min))
                - timezone.now()
            ).total_seconds()
        ),
        60,
    )
    _increment_counter(_daily_email_key(email, date_key), seconds_to_tomorrow)
    if client_ip:
        _increment_counter(_daily_ip_key(client_ip, date_key), seconds_to_tomorrow)


@dataclass
class EmailVerificationIssueResult:
    expires_in: int
    resend_in: int


class TencentSesEmailSender:
    """兼容旧测试和旧调用方式，内部已改为标准 SMTP 发送。"""

    @classmethod
    def from_settings(cls) -> "TencentSesEmailSender":
        return cls()

    def send_register_verification_email(self, *, email: str, code: str, expire_minutes: int) -> str:
        if _using_tencent_ses():
            return self._send_via_tencent(email=email, code=code, expire_minutes=expire_minutes)
        _send_register_verification_email(email=email, code=code, expire_minutes=expire_minutes)
        return "smtp"

    def _send_via_tencent(self, *, email: str, code: str, expire_minutes: int) -> str:
        try:
            from tencentcloud.common import credential
            from tencentcloud.common.exception.tencent_cloud_sdk_exception import TencentCloudSDKException
            from tencentcloud.ses.v20201002 import models, ses_client
        except Exception as exc:
            raise ImproperlyConfigured("缺少 tencentcloud-sdk-python 依赖") from exc

        try:
            cred = credential.Credential(
                settings.TENCENTCLOUD_SECRET_ID,
                settings.TENCENTCLOUD_SECRET_KEY,
            )
            client = ses_client.SesClient(cred, settings.TENCENTCLOUD_SES_REGION)
            request = models.SendEmailRequest()
            request.FromEmailAddress = settings.TENCENTCLOUD_SES_FROM_EMAIL
            request.Destination = [email]
            if settings.TENCENTCLOUD_SES_REPLY_TO:
                request.ReplyToAddresses = [settings.TENCENTCLOUD_SES_REPLY_TO]
            request.Subject = settings.TENCENTCLOUD_SES_VERIFICATION_SUBJECT
            if settings.TENCENTCLOUD_SES_FROM_ALIAS:
                request.FromEmailAddress = f"{settings.TENCENTCLOUD_SES_FROM_ALIAS} <{settings.TENCENTCLOUD_SES_FROM_EMAIL}>"

            template = models.Template()
            template.TemplateID = int(settings.TENCENTCLOUD_SES_VERIFICATION_TEMPLATE_ID)
            template.TemplateData = (
                '{"code":"%s","expire_minutes":"%s"}' % (code, expire_minutes)
            )
            request.Template = template
            response = client.SendEmail(request)
            return getattr(response, "MessageId", "") or "tencent_ses"
        except TencentCloudSDKException as exc:
            raise EmailVerificationSendError("腾讯云邮件发送失败") from exc


def _send_register_verification_email(*, email: str, code: str, expire_minutes: int) -> None:
    _ensure_email_backend_configured()
    subject = f"{settings.SITE_NAME} 注册验证码"
    text_body = (
        f"你的验证码是：{code}\n\n"
        f"{expire_minutes} 分钟内有效。\n"
        "如果这不是你的操作，请忽略本邮件。"
    )
    html_body = (
        f"<p>你的验证码是：<strong style='font-size:20px;'>{code}</strong></p>"
        f"<p>{expire_minutes} 分钟内有效。</p>"
        "<p>如果这不是你的操作，请忽略本邮件。</p>"
    )
    message = EmailMultiAlternatives(
        subject=subject,
        body=text_body,
        from_email=settings.DEFAULT_FROM_EMAIL,
        to=[email],
    )
    message.attach_alternative(html_body, "text/html")
    try:
        message.send(fail_silently=False)
    except Exception as exc:
        raise EmailVerificationSendError("邮箱验证码发送失败，请检查 SMTP 配置") from exc


def issue_register_email_code(*, email: str, client_ip: str) -> EmailVerificationIssueResult:
    if not is_email_verification_enabled():
        raise ImproperlyConfigured("当前未启用邮箱验证码注册")

    email = normalize_email(email)
    _check_send_limits(email, client_ip)

    code = generate_email_verification_code()
    expire_seconds = email_verification_ttl_seconds()
    expire_minutes = max(math.ceil(expire_seconds / 60), 1)
    cache.set(
        _code_key(email),
        {
            "code_hash": _hash_code(code),
            "expires_at": timezone.now().timestamp() + expire_seconds,
        },
        timeout=expire_seconds,
    )
    _mark_send_limits(email, client_ip)
    sender = TencentSesEmailSender.from_settings()
    sender.send_register_verification_email(email=email, code=code, expire_minutes=expire_minutes)
    log_callback_event(
        event_type="register_email_code_sent",
        source="smtp",
        payload={"email": email},
        success=True,
    )
    return EmailVerificationIssueResult(
        expires_in=expire_seconds,
        resend_in=email_verification_resend_seconds(),
    )


def validate_register_email_code(email: str, code: str) -> None:
    if not is_email_verification_enabled():
        return
    payload = cache.get(_code_key(normalize_email(email))) or {}
    stored_hash = payload.get("code_hash", "")
    if not stored_hash:
        raise ValidationError("验证码不存在或已过期")
    if not constant_time_compare(stored_hash, _hash_code((code or "").strip())):
        raise ValidationError("邮箱验证码错误")


def consume_register_email_code(email: str, code: str) -> None:
    validate_register_email_code(email, code)
    cache.delete(_code_key(normalize_email(email)))
