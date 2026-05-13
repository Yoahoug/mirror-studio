#!/usr/bin/env python
from __future__ import annotations

import os
import sys
from pathlib import Path

BASE_DIR = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(BASE_DIR))
os.environ.setdefault("DJANGO_SETTINGS_MODULE", "config.settings.dev")

import django  # noqa: E402

django.setup()

from django.conf import settings  # noqa: E402

from apps.core.siteconfig import CONFIG_KEYS, SiteConfig, set_config  # noqa: E402

DEFAULT_VALUES: dict[str, str] = {
    "site.name": settings.SITE_NAME,
    "site.brand_name": settings.BRAND_NAME,
    "site.brand_english_name": settings.BRAND_ENGLISH_NAME,
    "site.brand_tagline": settings.BRAND_TAGLINE,
    "site.register_enabled": "1" if settings.REGISTER_ENABLED else "0",
    "site.login_captcha_enabled": "1" if settings.LOGIN_CAPTCHA_ENABLED else "0",
    "site.image_retention_days": str(settings.IMAGE_RETENTION_DAYS),
    "site.daily_user_task_limit": str(settings.DAILY_USER_TASK_LIMIT),
    "site.max_pending_tasks": str(settings.MAX_PENDING_TASKS),
    "site.default_channel_code": settings.DEFAULT_CHANNEL_CODE,
    "site.default_daily_free_quota": str(settings.DEFAULT_DAILY_FREE_QUOTA),
    "site.workspace_release_version": settings.WORKSPACE_RELEASE_VERSION,
    "site.workspace_release_title": settings.WORKSPACE_RELEASE_TITLE,
    "site.workspace_release_notes": settings.WORKSPACE_RELEASE_NOTES,
    "site.workspace_release_published_at": settings.WORKSPACE_RELEASE_PUBLISHED_AT,
    "site.release_push_key": settings.RELEASE_PUSH_KEY,
    "site.release_push_secret": settings.RELEASE_PUSH_SECRET,
    "email.verification_enabled": "1" if settings.EMAIL_VERIFICATION_ENABLED else "0",
    "email.verification_code_length": str(settings.EMAIL_VERIFICATION_CODE_LENGTH),
    "email.verification_ttl_seconds": str(settings.EMAIL_VERIFICATION_TTL_SECONDS),
    "email.verification_resend_seconds": str(settings.EMAIL_VERIFICATION_RESEND_SECONDS),
    "email.daily_limit_per_email": str(settings.EMAIL_VERIFICATION_DAILY_LIMIT_PER_EMAIL),
    "email.daily_limit_per_ip": str(settings.EMAIL_VERIFICATION_DAILY_LIMIT_PER_IP),
    "risk.login_fail_limit": str(settings.RISK_LOGIN_FAIL_LIMIT),
    "risk.login_lock_minutes": str(settings.RISK_LOGIN_LOCK_MINUTES),
    "risk.task_window_seconds": str(settings.RISK_TASK_WINDOW_SECONDS),
    "risk.task_limit_per_window": str(settings.RISK_TASK_LIMIT_PER_WINDOW),
}


def main() -> None:
    created = 0
    skipped = 0
    for config_key, meta in CONFIG_KEYS.items():
        existing = SiteConfig.objects.filter(key=config_key).first()
        if existing and existing.value_encrypted:
            skipped += 1
            continue
        set_config(
            config_key,
            DEFAULT_VALUES.get(config_key, ""),
            is_secret=meta.get("is_secret", False),
            description=meta.get("description", ""),
            category=meta.get("category", SiteConfig.CATEGORY_GENERAL),
        )
        created += 1
    print(f"SiteConfig 初始化完成：新增 {created} 项，跳过 {skipped} 项。")


if __name__ == "__main__":
    main()
