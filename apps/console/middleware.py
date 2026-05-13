"""
管理端安全中间件 — 仅拦截 /console/ 路径。

功能：
1. 身份验证：is_operator 检查
2. HMAC 签名校验（可选，CONSOLE_HMAC_ENABLED=True 时启用）
3. 速率限制：每用户每分钟 N 次请求
4. 安全响应头
"""
from __future__ import annotations

import hashlib
import hmac
import json
import logging
import time

from django.conf import settings
from django.core.cache import cache
from django.http import JsonResponse

logger = logging.getLogger(__name__)

CONSOLE_PATH_PREFIX = "/console/"


def _increment_cache_counter(key: str, timeout: int) -> int:
    cache.add(key, 0, timeout=timeout)
    try:
        return cache.incr(key)
    except ValueError:
        cache.set(key, 1, timeout=timeout)
        return 1


class ConsoleSecurityMiddleware:

    def __init__(self, get_response):
        self.get_response = get_response

    def __call__(self, request):
        # 仅拦截 /console/api/ 路径，避免拦截控制台前端页面
        if not request.path.startswith("/console/api/"):
            return self.get_response(request)

        # 1. 身份验证
        if not request.user.is_authenticated:
            return _json_error("未登录", code=401000, status=401)

        if not getattr(request.user, "is_active_status", False):
            return _json_error("当前账号状态不可用", code=403001, status=403)

        if not getattr(request.user, "is_operator", False):
            return _json_error("权限不足", code=403000, status=403)

        # 2. 速率限制
        rate_limit = getattr(settings, "CONSOLE_API_RATE_LIMIT", 60)
        if rate_limit > 0:
            rate_key = f"console_rate:{request.user.pk}"
            current = _increment_cache_counter(rate_key, 60)
            if current > rate_limit:
                return _json_error("请求过于频繁，请稍后再试", code=429000, status=429)

        # 3. HMAC 签名校验（可选）
        hmac_enabled = getattr(settings, "CONSOLE_HMAC_ENABLED", False)
        if hmac_enabled and request.path.startswith("/console/api/"):
            error = self._verify_hmac(request)
            if error:
                return error

        # 执行视图
        response = self.get_response(request)

        # 4. 安全响应头
        response["X-Content-Type-Options"] = "nosniff"
        response["X-Frame-Options"] = "DENY"
        response["Cache-Control"] = "no-store, no-cache, must-revalidate, max-age=0"
        response["Pragma"] = "no-cache"
        response["X-Robots-Tag"] = "noindex, nofollow"

        return response

    def _verify_hmac(self, request) -> JsonResponse | None:
        """
        HMAC 签名校验。

        客户端需在请求头中携带：
        - X-Console-Timestamp: Unix 时间戳（秒）
        - X-Console-Signature: HMAC-SHA256 签名

        签名计算方式:
            message = f"{method}:{path}:{timestamp}:{body_sha256}"
            signature = HMAC-SHA256(SECRET_KEY, message).hexdigest()
        """
        timestamp_str = request.META.get("HTTP_X_CONSOLE_TIMESTAMP", "")
        signature = request.META.get("HTTP_X_CONSOLE_SIGNATURE", "")

        if not timestamp_str or not signature:
            return _json_error("缺少签名参数", code=401001, status=401)

        # 时间戳校验（防重放，5 分钟窗口）
        try:
            timestamp = int(timestamp_str)
        except (ValueError, TypeError):
            return _json_error("无效的时间戳", code=401002, status=401)

        now = int(time.time())
        if abs(now - timestamp) > 300:
            return _json_error("签名已过期", code=401003, status=401)

        # 计算签名
        body_bytes = request.body or b""
        body_hash = hashlib.sha256(body_bytes).hexdigest()
        message = f"{request.method}:{request.path}:{timestamp_str}:{body_hash}"

        secret = settings.SECRET_KEY
        if isinstance(secret, str):
            secret = secret.encode("utf-8")

        expected = hmac.new(secret, message.encode("utf-8"), hashlib.sha256).hexdigest()

        if not hmac.compare_digest(signature, expected):
            logger.warning(
                "HMAC 签名校验失败 user=%s path=%s",
                request.user.pk if request.user.is_authenticated else "anon",
                request.path,
            )
            return _json_error("签名校验失败", code=401004, status=401)

        return None


def _json_error(message: str, code: int, status: int) -> JsonResponse:
    return JsonResponse(
        {"code": code, "message": message, "data": None},
        status=status,
    )
