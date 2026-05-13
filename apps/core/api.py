from __future__ import annotations

import json

from django.http import JsonResponse


class InvalidPayloadError(ValueError):
    pass


def parse_request_payload(request) -> dict:
    if request.content_type and "application/json" in request.content_type:
        try:
            payload = json.loads(request.body or "{}")
        except (json.JSONDecodeError, UnicodeDecodeError) as exc:
            raise InvalidPayloadError("请求 JSON 格式错误") from exc
        if not isinstance(payload, dict):
            raise InvalidPayloadError("请求 JSON 必须是对象")
        return payload
    return request.POST.dict()


def api_success(data=None, message: str = "ok", status: int = 200) -> JsonResponse:
    return JsonResponse({"code": 0, "message": message, "data": data}, status=status)


def api_error(message: str, code: int = 400000, status: int = 400, data=None) -> JsonResponse:
    return JsonResponse({"code": code, "message": message, "data": data}, status=status)
