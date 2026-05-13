from __future__ import annotations

from django.conf import settings
from django.core.cache import cache
from django.utils import timezone

from .models import IPAccessRule, RiskEvent, SensitiveWordRule


def _login_key(username: str, ip: str) -> str:
    return f"risk:login:{username}:{ip}"


def _task_key(user_id: int) -> str:
    return f"risk:task:{user_id}"


def _increment_cache_counter(key: str, timeout: int) -> int:
    cache.add(key, 0, timeout=timeout)
    try:
        return cache.incr(key)
    except ValueError:
        cache.set(key, 1, timeout=timeout)
        return 1


def ensure_ip_allowed(ip: str) -> None:
    if not ip:
        return
    rule = IPAccessRule.objects.filter(ip_address=ip, is_blocked=True).first()
    if rule and rule.is_effective:
        raise PermissionError("当前 IP 已被限制访问")


def ensure_login_allowed(username: str, ip: str) -> None:
    ensure_ip_allowed(ip)
    attempts = cache.get(_login_key(username, ip), 0)
    if attempts >= settings.RISK_LOGIN_FAIL_LIMIT:
        raise PermissionError(f"登录失败次数过多，请 {settings.RISK_LOGIN_LOCK_MINUTES} 分钟后再试")


def record_login_failure(username: str, ip: str) -> None:
    key = _login_key(username, ip)
    attempts = _increment_cache_counter(key, settings.RISK_LOGIN_LOCK_MINUTES * 60)
    RiskEvent.objects.create(
        event_type="login_failure",
        request_ip=ip or None,
        detail_json={"username": username, "attempts": attempts},
    )


def clear_login_failures(username: str, ip: str) -> None:
    cache.delete(_login_key(username, ip))


def ensure_prompt_safe(prompt: str, user=None, ip: str | None = None) -> None:
    stripped = (prompt or "").strip()
    if not stripped:
        raise PermissionError("Prompt 不能为空")
    for rule in SensitiveWordRule.objects.filter(is_active=True):
        matched = stripped == rule.keyword if rule.match_mode == rule.MatchMode.EXACT else rule.keyword in stripped
        if matched:
            RiskEvent.objects.create(
                event_type="prompt_blocked",
                user=user,
                request_ip=ip,
                detail_json={"keyword": rule.keyword},
            )
            raise PermissionError("Prompt 命中风控规则")


def ensure_task_rate_limit(user, ip: str | None = None) -> None:
    key = _task_key(user.id)
    count = _increment_cache_counter(key, settings.RISK_TASK_WINDOW_SECONDS)
    if count > settings.RISK_TASK_LIMIT_PER_WINDOW:
        RiskEvent.objects.create(
            event_type="task_rate_limited",
            user=user,
            request_ip=ip,
            detail_json={"window_seconds": settings.RISK_TASK_WINDOW_SECONDS},
        )
        raise PermissionError("提交过于频繁，请稍后再试")
