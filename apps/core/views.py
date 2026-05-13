from __future__ import annotations

import hashlib
import hmac
import json
import time

import requests
from django.conf import settings
from django.contrib.auth.mixins import LoginRequiredMixin
from django.http import HttpRequest
from django.http import JsonResponse
from django.utils import timezone
from django.views.generic import TemplateView, View

from apps.core.api import InvalidPayloadError, api_error, api_success, parse_request_payload
from apps.core.siteconfig import get_config, set_config
from apps.generator.models import ImageChannel


def _json_payload(request: HttpRequest) -> dict:
    return parse_request_payload(request)


def _message_text(item: dict) -> str:
    if item.get("content"):
        return str(item["content"])
    parts = item.get("parts") or []
    if isinstance(parts, list):
        return "\n".join(str(part.get("text", "")) for part in parts if isinstance(part, dict)).strip()
    return ""


def _payload_float(payload: dict, key: str, default: float) -> float:
    try:
        return float(payload.get(key, default))
    except (TypeError, ValueError):
        return default


class HomeView(TemplateView):
    template_name = "public/home.html"


class PricingView(TemplateView):
    template_name = "public/pricing.html"


class WorkspacePageView(LoginRequiredMixin, TemplateView):
    template_name = "workspace/index.html"


class HealthCheckView(View):
    def get(self, _request):
        return JsonResponse({"status": "ok"})


def _release_push_credentials() -> tuple[str, str]:
    key = (get_config("site.release_push_key", settings.RELEASE_PUSH_KEY) or "").strip()
    secret = (get_config("site.release_push_secret", settings.RELEASE_PUSH_SECRET) or "").strip()
    return key, secret


def _normalize_release_notes(notes) -> str:
    if isinstance(notes, str):
        lines = [line.strip() for line in notes.splitlines() if line.strip()]
        return "\n".join(lines)
    if isinstance(notes, list):
        lines = [str(item).strip() for item in notes if str(item).strip()]
        return "\n".join(lines)
    raise InvalidPayloadError("更新日志 notes 必须是字符串或数组")


class InternalReleasePublishAPIView(View):
    def post(self, request):
        expected_key, secret = _release_push_credentials()
        header_key = (request.META.get("HTTP_X_RELEASE_KEY", "") or "").strip()
        timestamp = (request.META.get("HTTP_X_RELEASE_TIMESTAMP", "") or "").strip()
        signature = (request.META.get("HTTP_X_RELEASE_SIGNATURE", "") or "").strip().lower()

        if not expected_key or not secret:
            return api_error("站点未配置发布推送凭证", code=500001, status=500)
        if not header_key or header_key != expected_key:
            return api_error("发布凭证无效", code=401001, status=401)
        if not timestamp or not signature:
            return api_error("缺少签名参数", code=401002, status=401)
        try:
            timestamp_value = int(timestamp)
        except (TypeError, ValueError):
            return api_error("时间戳无效", code=401003, status=401)

        max_age = max(int(getattr(settings, "RELEASE_PUSH_MAX_AGE_SECONDS", 300) or 300), 1)
        if abs(int(time.time()) - timestamp_value) > max_age:
            return api_error("签名已过期", code=401004, status=401)

        body_bytes = request.body or b"{}"
        secret_bytes = secret.encode("utf-8")
        expected_signature = hmac.new(
            secret_bytes,
            timestamp.encode("utf-8") + b"." + body_bytes,
            hashlib.sha256,
        ).hexdigest()
        if not hmac.compare_digest(signature, expected_signature):
            return api_error("签名校验失败", code=401005, status=401)

        try:
            payload = parse_request_payload(request)
        except InvalidPayloadError as exc:
            return api_error(str(exc), code=400001)

        version = str(payload.get("version") or "").strip()
        title = str(payload.get("title") or version).strip()
        published_at = str(payload.get("published_at") or timezone.localdate().isoformat()).strip()
        notes = _normalize_release_notes(payload.get("notes") or "")
        if not version:
            return api_error("缺少版本号", code=400001)
        if not notes:
            return api_error("缺少更新日志", code=400001)

        set_config("site.workspace_release_version", version)
        set_config("site.workspace_release_title", title or version)
        set_config("site.workspace_release_notes", notes)
        set_config("site.workspace_release_published_at", published_at)
        return api_success(
            {
                "version": version,
                "title": title or version,
                "notes": notes.splitlines(),
                "published_at": published_at,
            }
        )


def _serialize_chat_model(channel: ImageChannel) -> dict:
    return {
        "id": channel.id,
        "name": channel.name,
        "channel_code": channel.channel_code,
        "provider_name": channel.provider_name,
        "model_name": channel.model_name,
        "configured": bool(channel.api_key),
    }


class ChatModelListAPIView(LoginRequiredMixin, View):
    """工作台可选对话模型。只返回 LLM 类型，不混入生图模型。"""

    def get(self, _request):
        channels = ImageChannel.objects.filter(
            is_active=True,
            channel_type=ImageChannel.ChannelType.LLM,
        ).exclude(api_key_encrypted="").order_by("priority", "id")
        return api_success([_serialize_chat_model(channel) for channel in channels])


class ChatAPIView(LoginRequiredMixin, View):
    """对话 LLM 代理。独立于生图任务，只读取 LLM 类型的对接方配置。"""

    def post(self, request):
        try:
            payload = _json_payload(request)
        except InvalidPayloadError as exc:
            return api_error(str(exc), code=400001)
        prompt = str(payload.get("prompt") or "").strip()
        if not prompt:
            return api_error("请输入对话内容", code=400001)

        channel_code = str(payload.get("channel_code") or payload.get("model") or "").strip()
        channels = ImageChannel.objects.filter(
            is_active=True,
            channel_type=ImageChannel.ChannelType.LLM,
        ).exclude(api_key_encrypted="")
        channel = channels.filter(channel_code=channel_code).first() if channel_code else channels.order_by("priority", "id").first()
        if not channel:
            return api_error("未配置可用对话模型", code=400503, status=503)
        api_key = channel.api_key
        if not api_key:
            return api_error("对话模型缺少 API Key", code=400503, status=503)

        system_prompt = str(payload.get("system_prompt") or "你是绘镜的创作助手，回答应简洁、专业、友好。")
        messages = [{"role": "system", "content": system_prompt}]
        for item in (payload.get("history") or [])[-12:]:
            if not isinstance(item, dict):
                continue
            content = _message_text(item)
            if not content:
                continue
            role = "assistant" if item.get("role") == "model" else "user"
            messages.append({"role": role, "content": content})
        messages.append({"role": "user", "content": prompt})

        try:
            response = requests.post(
                f"{channel.base_url.rstrip('/')}/{channel.api_path.lstrip('/')}",
                headers={"Authorization": f"Bearer {api_key}", "Content-Type": "application/json"},
                json={
                    "model": channel.model_name,
                    "messages": messages,
                    "temperature": _payload_float(payload, "temperature", 0.7),
                },
                timeout=max(channel.timeout_seconds, 1),
            )
            response.raise_for_status()
            result = response.json()
            reply = (
                result.get("choices", [{}])[0]
                .get("message", {})
                .get("content", "")
            ) or result.get("choices", [{}])[0].get("text", "")
        except (requests.RequestException, ValueError, KeyError, IndexError, TypeError):
            return api_error("对话 LLM 响应失败", code=400502, status=502)

        if not reply:
            return api_error("对话 LLM 未返回内容", code=400502, status=502)
        return api_success({"reply": reply, "model": _serialize_chat_model(channel)})
