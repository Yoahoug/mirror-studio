from __future__ import annotations

import contextvars
import logging
import uuid

from django.conf import settings

request_id_ctx = contextvars.ContextVar("request_id", default="-")
client_ip_ctx = contextvars.ContextVar("client_ip", default="-")


def get_request_id() -> str:
    return request_id_ctx.get()


class RequestContextMiddleware:
    def __init__(self, get_response):
        self.get_response = get_response

    def __call__(self, request):
        request_id = request.headers.get("X-Request-Id") or uuid.uuid4().hex[:16]
        forwarded = request.META.get("HTTP_X_FORWARDED_FOR", "")
        if forwarded and settings.TRUST_X_FORWARDED_FOR:
            client_ip = forwarded.split(",")[0].strip()
        else:
            client_ip = request.META.get("REMOTE_ADDR", "-")
        token_id = request_id_ctx.set(request_id)
        token_ip = client_ip_ctx.set(client_ip)
        request.request_id = request_id
        request.client_ip = client_ip
        try:
            response = self.get_response(request)
            response["X-Request-Id"] = request_id
            return response
        finally:
            request_id_ctx.reset(token_id)
            client_ip_ctx.reset(token_ip)


class RequestContextLogFilter(logging.Filter):
    def filter(self, record: logging.LogRecord) -> bool:
        record.request_id = request_id_ctx.get()
        record.client_ip = client_ip_ctx.get()
        return True
