from __future__ import annotations

import json
import secrets
from pathlib import Path

import django
from cryptography.fernet import Fernet


def parse_env(path: Path) -> dict[str, str]:
    data: dict[str, str] = {}
    if not path.exists():
        return data
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        data[key.strip()] = value.strip()
    return data


def main() -> None:
    django.setup()

    from apps.core.siteconfig import SiteConfig
    from apps.generator.models import ImageChannel

    root = Path("部署/上线包/secrets")
    root.mkdir(parents=True, exist_ok=True)

    siteconfigs = [
        {
            "key": item.key,
            "value": item.value,
            "is_secret": item.is_secret,
            "category": item.category,
            "description": item.description,
        }
        for item in SiteConfig.objects.order_by("key")
    ]
    (root / "siteconfig.full.json").write_text(
        json.dumps(siteconfigs, ensure_ascii=False, indent=2),
        encoding="utf-8",
    )

    channels = [
        {
            "name": channel.name,
            "channel_code": channel.channel_code,
            "channel_type": channel.channel_type,
            "provider_name": channel.provider_name,
            "base_url": channel.base_url,
            "api_path": channel.api_path,
            "api_key": channel.api_key,
            "model_name": channel.model_name,
            "timeout_seconds": channel.timeout_seconds,
            "max_retries": channel.max_retries,
            "is_active": channel.is_active,
            "priority": channel.priority,
        }
        for channel in ImageChannel.objects.order_by("priority", "id")
    ]
    (root / "image_channels.full.json").write_text(
        json.dumps(channels, ensure_ascii=False, indent=2),
        encoding="utf-8",
    )

    cur = parse_env(Path(".env"))
    prod = {
        "DJANGO_SETTINGS_MODULE": "config.settings.prod",
        "SECRET_KEY": secrets.token_urlsafe(64),
        "DEBUG": "0",
        "ALLOW_INSECURE_PROD": "0",
        "PUBLIC_ORIGIN": "https://CHANGE_ME_DOMAIN",
        "HTTP_PORT": "8000",
        "TIME_ZONE": cur.get("TIME_ZONE", "Asia/Shanghai"),
        "LANGUAGE_CODE": cur.get("LANGUAGE_CODE", "zh-hans"),
        "POSTGRES_DB": cur.get("POSTGRES_DB", "mirror_studio"),
        "POSTGRES_USER": cur.get("POSTGRES_USER", "mirror_studio"),
        "POSTGRES_PASSWORD": secrets.token_urlsafe(24),
        "AUTO_APPLY_MIGRATIONS": "1",
        "AUTO_SEED_SITECONFIG": "1",
        "AUTO_INIT_APP_DATA": "1",
        "AUTO_COLLECTSTATIC": "1",
        "ADMIN_USERNAME": cur.get("ADMIN_USERNAME", "admin"),
        "ADMIN_PASSWORD": secrets.token_urlsafe(18),
        "ADMIN_EMAIL": cur.get("ADMIN_EMAIL", "admin@example.com"),
        "MOCK_PROVIDER_ENABLED": "0",
        "DEFAULT_CHANNEL_CODE": cur.get("DEFAULT_CHANNEL_CODE", "default"),
        "PROVIDER_NAME": cur.get("PROVIDER_NAME", "默认图片渠道"),
        "PROVIDER_VENDOR": cur.get("PROVIDER_VENDOR", "OpenAI-Compatible"),
        "PROVIDER_BASE_URL": cur.get("PROVIDER_BASE_URL", "https://your-image-provider.example.com"),
        "PROVIDER_API_PATH": cur.get("PROVIDER_API_PATH", "/v1/images/generations"),
        "PROVIDER_MODEL_NAME": cur.get("PROVIDER_MODEL_NAME", "gpt-image-2"),
        "PROVIDER_API_KEY": cur.get("PROVIDER_API_KEY", ""),
        "PROVIDER_TIMEOUT_SECONDS": cur.get("PROVIDER_TIMEOUT_SECONDS", "300"),
        "PROVIDER_MAX_RETRIES": cur.get("PROVIDER_MAX_RETRIES", "1"),
        "LLM_CHANNEL_CODE": cur.get("LLM_CHANNEL_CODE", "default-llm"),
        "LLM_PROVIDER_NAME": cur.get("LLM_PROVIDER_NAME", "默认 LLM 渠道"),
        "LLM_PROVIDER_VENDOR": cur.get("LLM_PROVIDER_VENDOR", "OpenAI-Compatible"),
        "LLM_BASE_URL": cur.get("LLM_BASE_URL", cur.get("PROVIDER_BASE_URL", "https://your-llm-provider.example.com")),
        "LLM_API_PATH": cur.get("LLM_API_PATH", "/v1/chat/completions"),
        "LLM_MODEL_NAME": cur.get("LLM_MODEL_NAME", "gpt-5.4"),
        "LLM_API_KEY": cur.get("LLM_API_KEY", ""),
        "LLM_TIMEOUT_SECONDS": cur.get("LLM_TIMEOUT_SECONDS", "600"),
        "LLM_MAX_RETRIES": cur.get("LLM_MAX_RETRIES", "3"),
        "EMAIL_BACKEND": cur.get("EMAIL_BACKEND", "django.core.mail.backends.smtp.EmailBackend"),
        "EMAIL_HOST": cur.get("EMAIL_HOST", "smtp.qq.com"),
        "EMAIL_PORT": cur.get("EMAIL_PORT", "465"),
        "EMAIL_USE_SSL": cur.get("EMAIL_USE_SSL", "1"),
        "EMAIL_USE_TLS": cur.get("EMAIL_USE_TLS", "0"),
        "EMAIL_HOST_USER": cur.get("EMAIL_HOST_USER", ""),
        "EMAIL_HOST_PASSWORD": cur.get("EMAIL_HOST_PASSWORD", ""),
        "DEFAULT_FROM_EMAIL": cur.get("DEFAULT_FROM_EMAIL", "绘镜 Studio <admin@example.com>"),
        "DEFAULT_DAILY_FREE_QUOTA": cur.get("DEFAULT_DAILY_FREE_QUOTA", "3"),
        "DAILY_USER_TASK_LIMIT": cur.get("DAILY_USER_TASK_LIMIT", "50"),
        "MAX_PENDING_TASKS": cur.get("MAX_PENDING_TASKS", "3"),
        "IMAGE_RETENTION_DAYS": cur.get("IMAGE_RETENTION_DAYS", "30"),
        "RESUMABLE_UPLOAD_CHUNK_SIZE": cur.get("RESUMABLE_UPLOAD_CHUNK_SIZE", "1048576"),
        "NOVELVIZ_MAX_EPUB_UPLOAD_BYTES": cur.get("NOVELVIZ_MAX_EPUB_UPLOAD_BYTES", "83886080"),
        "NOVELVIZ_MAX_SCENES": cur.get("NOVELVIZ_MAX_SCENES", "10"),
        "MINIO_USE_LOCAL_FS": "1",
        "MINIO_LOCAL_ROOT": "media/minio",
        "MINIO_BUCKET": cur.get("MINIO_BUCKET", "generated-images"),
        "FIELD_CRYPT_KEY": Fernet.generate_key().decode(),
        "USE_X_FORWARDED_HOST": "1",
        "TRUST_X_FORWARDED_FOR": cur.get("TRUST_X_FORWARDED_FOR", "0"),
        "SESSION_COOKIE_SECURE": "1",
        "CSRF_COOKIE_SECURE": "1",
        "SECURE_SSL_REDIRECT": "1",
        "SECURE_HSTS_SECONDS": "31536000",
        "GENERATION_TASKS_RUN_INLINE": cur.get("GENERATION_TASKS_RUN_INLINE", "0"),
        "GENERATION_WORKER_POLL_SECONDS": cur.get("GENERATION_WORKER_POLL_SECONDS", "2"),
        "GUNICORN_WORKERS": cur.get("GUNICORN_WORKERS", "2"),
        "GUNICORN_THREADS": cur.get("GUNICORN_THREADS", "4"),
        "GUNICORN_WORKER_CLASS": cur.get("GUNICORN_WORKER_CLASS", "gthread"),
        "GUNICORN_TIMEOUT": cur.get("GUNICORN_TIMEOUT", "180"),
    }
    prod["DATABASE_URL"] = (
        f"postgresql://{prod['POSTGRES_USER']}:{prod['POSTGRES_PASSWORD']}"
        f"@postgres:5432/{prod['POSTGRES_DB']}"
    )

    lines = [
        "# 已填入随机生产基础密钥；上线前只需要替换 CHANGE_ME_DOMAIN。",
        "# 该文件含真实/可用密钥，不要提交 Git。",
        "",
    ]
    lines.extend(f"{key}={value}" for key, value in prod.items())
    (root / ".env.production.ready").write_text("\n".join(lines) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
