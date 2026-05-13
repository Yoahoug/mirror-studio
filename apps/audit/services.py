from __future__ import annotations

from django.http import HttpRequest

from .models import AdminOperationLog, CallbackEventLog


def log_admin_operation(*, request: HttpRequest | None, admin_user, action: str, target_type: str, target_id: str = "", detail: dict | None = None) -> None:
    AdminOperationLog.objects.create(
        admin_user=admin_user,
        action=action,
        target_type=target_type,
        target_id=target_id,
        detail_json=detail or {},
        request_ip=getattr(request, "client_ip", None),
    )


def log_callback_event(*, event_type: str, source: str, payload: dict, success: bool, remark: str = "") -> CallbackEventLog:
    return CallbackEventLog.objects.create(
        event_type=event_type,
        source=source,
        payload_json=payload,
        success=success,
        remark=remark,
    )

