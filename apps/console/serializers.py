from __future__ import annotations


def serialize_config(config) -> dict:
    return {
        "id": config.id,
        "key": config.key,
        "value": config.masked_value if config.is_secret else config.value,
        "is_secret": config.is_secret,
        "description": config.description,
        "category": config.category,
        "updated_at": config.updated_at.isoformat() if config.updated_at else None,
    }


def serialize_config_list(configs) -> list[dict]:
    return [serialize_config(item) for item in configs]


def serialize_image_channel(channel) -> dict:
    return {
        "id": channel.id,
        "name": channel.name,
        "channel_code": channel.channel_code,
        "channel_type": channel.channel_type,
        "provider_name": channel.provider_name,
        "base_url": channel.base_url,
        "api_path": channel.api_path,
        "edit_api_path": channel.edit_api_path,
        "model_name": channel.model_name,
        "timeout_seconds": channel.timeout_seconds,
        "max_retries": channel.max_retries,
        "is_active": channel.is_active,
        "priority": channel.priority,
        "created_at": channel.created_at.isoformat() if channel.created_at else None,
        "updated_at": channel.updated_at.isoformat() if channel.updated_at else None,
    }


def serialize_image_channel_list(channels) -> list[dict]:
    return [serialize_image_channel(item) for item in channels]


def serialize_user(user) -> dict:
    try:
        quota = user.quota_account
    except Exception:
        quota = None
    return {
        "id": user.id,
        "username": user.username,
        "email": user.email or "",
        "mobile": user.mobile or "",
        "role": user.role,
        "status": user.status,
        "is_staff": user.is_staff,
        "date_joined": user.date_joined.isoformat() if user.date_joined else None,
        "last_login": user.last_login.isoformat() if user.last_login else None,
        "last_login_ip": str(user.last_login_ip) if user.last_login_ip else None,
        "quota": {
            "daily_quota": quota.daily_quota,
            "available_quota": quota.available_quota,
            "frozen_quota": quota.frozen_quota,
            "total_generated_count": quota.total_generated_count,
            "last_reset_date": quota.last_reset_date.isoformat() if quota.last_reset_date else None,
        }
        if quota
        else None,
    }


def serialize_user_detail(user) -> dict:
    base = serialize_user(user)
    try:
        profile = user.profile
    except Exception:
        profile = None
    base["profile"] = (
        {
            "nickname": profile.nickname,
            "avatar": profile.avatar,
            "register_ip": str(profile.register_ip) if profile.register_ip else None,
            "register_source": profile.register_source,
            "remark": profile.remark,
        }
        if profile
        else None
    )
    return base


def serialize_task(task) -> dict:
    return {
        "id": task.id,
        "task_no": task.task_no,
        "user_id": task.user_id,
        "username": task.user.username if getattr(task, "user", None) else "",
        "status": task.status,
        "prompt": task.prompt,
        "style_code": task.style_code,
        "size_code": task.size_code,
        "quality_code": task.quality_code,
        "channel_name": task.channel.name if getattr(task, "channel", None) else "",
        "quota_cost": task.quota_cost,
        "error_code": task.error_code,
        "error_message": task.error_message,
        "provider_request_id": task.provider_request_id,
        "provider_response_json": task.provider_response_json,
        "created_at": task.created_at.isoformat() if task.created_at else None,
        "finished_at": task.finished_at.isoformat() if task.finished_at else None,
    }


def serialize_failure_log(item) -> dict:
    if hasattr(item, "payload_json"):
        payload = item.payload_json or {}
        return {
            "id": item.id,
            "event_type": item.event_type,
            "source": item.source,
            "success": item.success,
            "remark": item.remark,
            "task_no": payload.get("task_no", ""),
            "error_code": payload.get("error_code", ""),
            "error_message": payload.get("error_message", ""),
            "provider_request_id": payload.get("provider_request_id", ""),
            "payload_json": payload.get("raw_response") or payload,
            "created_at": item.created_at.isoformat() if item.created_at else None,
        }

    failure_payload = item.provider_response_json or {}
    return {
        "id": item.id,
        "event_type": "generation_failed",
        "source": getattr(item.channel, "channel_code", "") or "generator",
        "success": False,
        "remark": item.error_message,
        "task_no": item.task_no,
        "error_code": item.error_code,
        "error_message": item.error_message,
        "provider_request_id": item.provider_request_id,
        "payload_json": failure_payload.get("failure") or failure_payload,
        "created_at": item.finished_at.isoformat() if item.finished_at else item.created_at.isoformat() if item.created_at else None,
    }
