from __future__ import annotations

import os
from pathlib import Path
from urllib.parse import urlparse

import dj_database_url

BASE_DIR = Path(__file__).resolve().parents[2]


def _load_dotenv(path: Path) -> None:
    if not path.exists():
        return
    for raw_line in path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[7:].strip()
        if "=" not in line:
            continue
        key, value = line.split("=", 1)
        key = key.strip()
        value = value.strip()
        if value[:1] == value[-1:] and value[:1] in {'"', "'"}:
            value = value[1:-1]
        os.environ.setdefault(key, value)


_load_dotenv(BASE_DIR / ".env")


def env(name: str, default: str | None = None) -> str | None:
    return os.getenv(name, default)


def env_bool(name: str, default: bool = False) -> bool:
    value = os.getenv(name)
    if value is None:
        return default
    return value.lower() in {"1", "true", "yes", "on"}


def env_int(name: str, default: int) -> int:
    value = os.getenv(name)
    return int(value) if value not in {None, ""} else default


def env_list(name: str, default: list[str] | None = None) -> list[str]:
    raw = os.getenv(name)
    if not raw:
        return default or []
    return [item.strip() for item in raw.split(",") if item.strip()]


def _normalize_origin(value: str | None) -> str:
    raw = (value or "").strip().rstrip("/")
    parsed = urlparse(raw)
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        return ""
    return f"{parsed.scheme}://{parsed.netloc}"


def _hostname_from_origin(origin: str) -> str:
    return (urlparse(origin).hostname or "").strip()


def _default_allowed_hosts(origin: str) -> list[str]:
    hosts = ["127.0.0.1", "localhost", "testserver"]
    hostname = _hostname_from_origin(origin)
    if hostname and hostname not in hosts:
        hosts.insert(0, hostname)
    return hosts


def _default_csrf_trusted_origins(origin: str) -> list[str]:
    return [origin] if origin else []


SECRET_KEY = env("SECRET_KEY", "replace-me")
DEBUG = env_bool("DEBUG", False)
APP_PORT = env_int("APP_PORT", 8000)
PUBLIC_ORIGIN = _normalize_origin(env("PUBLIC_ORIGIN") or env("SITE_DOMAIN") or f"http://127.0.0.1:{APP_PORT}")
SITE_DOMAIN = PUBLIC_ORIGIN or f"http://127.0.0.1:{APP_PORT}"
ALLOWED_HOSTS = env_list("ALLOWED_HOSTS", _default_allowed_hosts(SITE_DOMAIN))
CSRF_TRUSTED_ORIGINS = env_list("CSRF_TRUSTED_ORIGINS", _default_csrf_trusted_origins(SITE_DOMAIN))
TIME_ZONE = env("TIME_ZONE", "Asia/Shanghai")
LANGUAGE_CODE = env("LANGUAGE_CODE", "zh-hans")
USE_I18N = True
USE_TZ = True
DEFAULT_AUTO_FIELD = "django.db.models.BigAutoField"

SITE_NAME = env("SITE_NAME", "绘镜 Studio")
BRAND_NAME = env("BRAND_NAME", "绘镜")
BRAND_ENGLISH_NAME = env("BRAND_ENGLISH_NAME", "MIRROR STUDIO")
BRAND_TAGLINE = env("BRAND_TAGLINE", "把灵感写成图像，把作品留在自己的空间里")
WORKSPACE_VITE_DEV_URL = ((env("WORKSPACE_VITE_DEV_URL", "") or "").strip() if DEBUG else "")
CONSOLE_VITE_DEV_URL = ((env("CONSOLE_VITE_DEV_URL", "") or "").strip() if DEBUG else "")
WORKSPACE_RELEASE_VERSION = env("WORKSPACE_RELEASE_VERSION", "v1.1") or "v1.1"
WORKSPACE_RELEASE_TITLE = env("WORKSPACE_RELEASE_TITLE", "绘镜工作台 1.1") or "绘镜工作台 1.1"
WORKSPACE_RELEASE_NOTES = env(
    "WORKSPACE_RELEASE_NOTES",
    "通知中心切换为真实更新与任务动态\n历史记录支持真实状态筛选\n用户面板死入口改为可达账户与安全页面",
) or "通知中心切换为真实更新与任务动态\n历史记录支持真实状态筛选\n用户面板死入口改为可达账户与安全页面"
WORKSPACE_RELEASE_PUBLISHED_AT = env("WORKSPACE_RELEASE_PUBLISHED_AT", "2026-03-14") or "2026-03-14"

REGISTER_ENABLED = env_bool("REGISTER_ENABLED", True)
LOGIN_CAPTCHA_ENABLED = env_bool("LOGIN_CAPTCHA_ENABLED", True)
EMAIL_VERIFICATION_ENABLED = env_bool("EMAIL_VERIFICATION_ENABLED", True)
EMAIL_VERIFICATION_CODE_LENGTH = env_int("EMAIL_VERIFICATION_CODE_LENGTH", 6)
EMAIL_VERIFICATION_TTL_SECONDS = env_int("EMAIL_VERIFICATION_TTL_SECONDS", 600)
EMAIL_VERIFICATION_RESEND_SECONDS = env_int("EMAIL_VERIFICATION_RESEND_SECONDS", 60)
EMAIL_VERIFICATION_DAILY_LIMIT_PER_EMAIL = env_int("EMAIL_VERIFICATION_DAILY_LIMIT_PER_EMAIL", 10)
EMAIL_VERIFICATION_DAILY_LIMIT_PER_IP = env_int("EMAIL_VERIFICATION_DAILY_LIMIT_PER_IP", 30)
DEFAULT_DAILY_FREE_QUOTA = env_int("DEFAULT_DAILY_FREE_QUOTA", 3)
IMAGE_RETENTION_DAYS = env_int("IMAGE_RETENTION_DAYS", 30)
DAILY_USER_TASK_LIMIT = env_int("DAILY_USER_TASK_LIMIT", 50)
MAX_PENDING_TASKS = env_int("MAX_PENDING_TASKS", 3)
RISK_LOGIN_FAIL_LIMIT = env_int("RISK_LOGIN_FAIL_LIMIT", 5)
RISK_LOGIN_LOCK_MINUTES = env_int("RISK_LOGIN_LOCK_MINUTES", 15)
RISK_TASK_WINDOW_SECONDS = env_int("RISK_TASK_WINDOW_SECONDS", 60)
RISK_TASK_LIMIT_PER_WINDOW = env_int("RISK_TASK_LIMIT_PER_WINDOW", 10)
CONSOLE_HMAC_ENABLED = env_bool("CONSOLE_HMAC_ENABLED", False)
CONSOLE_API_RATE_LIMIT = env_int("CONSOLE_API_RATE_LIMIT", 60)
TRUST_X_FORWARDED_FOR = env_bool("TRUST_X_FORWARDED_FOR", False)
REMOTE_IMAGE_ALLOWED_HOSTS = env_list("REMOTE_IMAGE_ALLOWED_HOSTS", [])
MAX_REMOTE_IMAGE_BYTES = env_int("MAX_REMOTE_IMAGE_BYTES", 25 * 1024 * 1024)
MAX_REFERENCE_IMAGE_BYTES = env_int("MAX_REFERENCE_IMAGE_BYTES", 10 * 1024 * 1024)
FILE_UPLOAD_MAX_MEMORY_SIZE = env_int(
    "FILE_UPLOAD_MAX_MEMORY_SIZE",
    max(MAX_REFERENCE_IMAGE_BYTES, 16 * 1024 * 1024),
)
DATA_UPLOAD_MAX_MEMORY_SIZE = env_int(
    "DATA_UPLOAD_MAX_MEMORY_SIZE",
    max(MAX_REFERENCE_IMAGE_BYTES * 3, 30 * 1024 * 1024),
)
RESUMABLE_UPLOAD_CHUNK_SIZE = env_int("RESUMABLE_UPLOAD_CHUNK_SIZE", 256 * 1024)
RESUMABLE_UPLOAD_SESSION_TTL_SECONDS = env_int("RESUMABLE_UPLOAD_SESSION_TTL_SECONDS", 24 * 60 * 60)
RELEASE_PUSH_KEY = env("RELEASE_PUSH_KEY", "") or ""
RELEASE_PUSH_SECRET = env("RELEASE_PUSH_SECRET", "") or ""
RELEASE_PUSH_MAX_AGE_SECONDS = env_int("RELEASE_PUSH_MAX_AGE_SECONDS", 300)

DEFAULT_CHANNEL_CODE = env("DEFAULT_CHANNEL_CODE", "default") or "default"
PROVIDER_NAME = env("PROVIDER_NAME", "默认图片渠道") or "默认图片渠道"
PROVIDER_VENDOR = env("PROVIDER_VENDOR", "OpenAI-Compatible") or "OpenAI-Compatible"
PROVIDER_BASE_URL = env("PROVIDER_BASE_URL", env("YUNMENG_BASE_URL", "http://127.0.0.1:8080")) or ""
PROVIDER_API_PATH = env("PROVIDER_API_PATH", "/v1/images/generations") or "/v1/images/generations"
PROVIDER_MODEL_NAME = env("PROVIDER_MODEL_NAME", env("YUNMENG_MODEL_NAME", "gpt-image-2")) or "gpt-image-2"
PROVIDER_API_KEY = env("PROVIDER_API_KEY", env("YUNMENG_PLATFORM_KEY", "")) or ""
PROVIDER_TIMEOUT_SECONDS = env_int("PROVIDER_TIMEOUT_SECONDS", env_int("YUNMENG_POLL_TIMEOUT_SECONDS", 300))
PROVIDER_MAX_RETRIES = env_int("PROVIDER_MAX_RETRIES", 1)
YUNMENG_PLATFORM_KEY = env("YUNMENG_PLATFORM_KEY", PROVIDER_API_KEY) or ""
YUNMENG_BASE_URL = env("YUNMENG_BASE_URL", PROVIDER_BASE_URL or "https://ymapi.wancores.com") or ""
YUNMENG_SUBMIT_PATH = env("YUNMENG_SUBMIT_PATH", "/api/public/image/draw/submit") or ""
YUNMENG_RESULT_PATH = env("YUNMENG_RESULT_PATH", "/api/public/image/draw/result") or ""
YUNMENG_BALANCE_PATH = env("YUNMENG_BALANCE_PATH", "/api/public/image/balance") or ""
YUNMENG_MODEL_NAME = env("YUNMENG_MODEL_NAME", PROVIDER_MODEL_NAME) or ""
YUNMENG_POLL_TIMEOUT_SECONDS = env_int("YUNMENG_POLL_TIMEOUT_SECONDS", PROVIDER_TIMEOUT_SECONDS)
OPENAI_IMAGE_EDIT_TIMEOUT_SECONDS = env_int("OPENAI_IMAGE_EDIT_TIMEOUT_SECONDS", 420)
MOCK_PROVIDER_ENABLED = env_bool("MOCK_PROVIDER_ENABLED", True)
GENERATION_TASKS_RUN_INLINE = env_bool("GENERATION_TASKS_RUN_INLINE", False)
GENERATION_WORKER_POLL_SECONDS = env_int("GENERATION_WORKER_POLL_SECONDS", 2)

LLM_CHANNEL_CODE = env("LLM_CHANNEL_CODE", "default-llm") or "default-llm"
LLM_PROVIDER_NAME = env("LLM_PROVIDER_NAME", "默认 LLM 渠道") or "默认 LLM 渠道"
LLM_PROVIDER_VENDOR = env("LLM_PROVIDER_VENDOR", "OpenAI-Compatible") or "OpenAI-Compatible"
LLM_BASE_URL = env("LLM_BASE_URL", env("NOVELVIZ_API_BASE_URL", PROVIDER_BASE_URL)) or ""
LLM_API_PATH = env("LLM_API_PATH", "/v1/chat/completions") or "/v1/chat/completions"
LLM_MODEL_NAME = env("LLM_MODEL_NAME", env("NOVELVIZ_TEXT_MODEL", "gpt-5.4")) or "gpt-5.4"
LLM_API_KEY = env("LLM_API_KEY", env("NOVELVIZ_TEXT_API_KEY", "")) or ""
LLM_TIMEOUT_SECONDS = env_int("LLM_TIMEOUT_SECONDS", env_int("NOVELVIZ_TEXT_READ_TIMEOUT_SECONDS", 600))
LLM_MAX_RETRIES = env_int("LLM_MAX_RETRIES", env_int("NOVELVIZ_TEXT_MAX_RETRIES", 3))
LLM_PRIORITY = env_int("LLM_PRIORITY", 1)

INSTALLED_APPS = [
    "django.contrib.admin",
    "django.contrib.auth",
    "django.contrib.contenttypes",
    "django.contrib.sessions",
    "django.contrib.messages",
    "django.contrib.staticfiles",
    "django.contrib.humanize",
    "apps.core",
    "apps.users",
    "apps.billing",
    "apps.generator",
    "apps.mediahub",
    "apps.novelviz",
    "apps.console",
    "apps.risk",
    "apps.audit",
]

MIDDLEWARE = [
    "django.middleware.security.SecurityMiddleware",
    "whitenoise.middleware.WhiteNoiseMiddleware",
    "django.contrib.sessions.middleware.SessionMiddleware",
    "django.middleware.common.CommonMiddleware",
    "django.middleware.csrf.CsrfViewMiddleware",
    "django.contrib.auth.middleware.AuthenticationMiddleware",
    "django.contrib.messages.middleware.MessageMiddleware",
    "django.middleware.clickjacking.XFrameOptionsMiddleware",
    "apps.core.middleware.RequestContextMiddleware",
    "apps.risk.middleware.IPBlockMiddleware",
    "apps.console.middleware.ConsoleSecurityMiddleware",
]

ROOT_URLCONF = "config.urls"
WSGI_APPLICATION = "config.wsgi.application"
ASGI_APPLICATION = "config.asgi.application"

TEMPLATES = [
    {
        "BACKEND": "django.template.backends.django.DjangoTemplates",
        "DIRS": [BASE_DIR / "templates"],
        "APP_DIRS": True,
        "OPTIONS": {
            "context_processors": [
                "django.template.context_processors.debug",
                "django.template.context_processors.request",
                "django.contrib.auth.context_processors.auth",
                "django.contrib.messages.context_processors.messages",
                "apps.core.context_processors.site_context",
            ],
        },
    }
]

DATABASES = {
    "default": dj_database_url.parse(
        env("DATABASE_URL", "sqlite:///db.sqlite3"),
        conn_max_age=600,
    )
}

CACHES = {
    "default": {
        "BACKEND": env("CACHE_BACKEND", "django.core.cache.backends.locmem.LocMemCache"),
        "LOCATION": env("CACHE_LOCATION", "mirror-studio-cache"),
    }
}

SESSION_COOKIE_HTTPONLY = True
CSRF_COOKIE_HTTPONLY = False
CSRF_COOKIE_SAMESITE = "Lax"
SESSION_COOKIE_SAMESITE = "Lax"

STATIC_URL = "/static/"
STATIC_ROOT = BASE_DIR / "staticfiles"
STATICFILES_DIRS = [BASE_DIR / "static"]
STATICFILES_STORAGE = "whitenoise.storage.CompressedManifestStaticFilesStorage"
WHITENOISE_MAX_AGE = env_int("WHITENOISE_MAX_AGE", 30 * 24 * 60 * 60)
MEDIA_ROOT = BASE_DIR / "media"
MEDIA_URL = "/media/"

AUTH_USER_MODEL = "users.User"
LOGIN_URL = "/workspace?auth=login"
LOGIN_REDIRECT_URL = "/workspace"
LOGOUT_REDIRECT_URL = "/"

PASSWORD_HASHERS = [
    "django.contrib.auth.hashers.Argon2PasswordHasher",
    "django.contrib.auth.hashers.PBKDF2PasswordHasher",
]

AUTH_PASSWORD_VALIDATORS = [
    {"NAME": "django.contrib.auth.password_validation.UserAttributeSimilarityValidator"},
    {"NAME": "django.contrib.auth.password_validation.MinimumLengthValidator", "OPTIONS": {"min_length": 8}},
    {"NAME": "django.contrib.auth.password_validation.CommonPasswordValidator"},
    {"NAME": "django.contrib.auth.password_validation.NumericPasswordValidator"},
]

EMAIL_BACKEND = env("EMAIL_BACKEND", "django.core.mail.backends.smtp.EmailBackend")
EMAIL_HOST = env("EMAIL_HOST", "") or ""
EMAIL_PORT = env_int("EMAIL_PORT", 465)
EMAIL_HOST_USER = env("EMAIL_HOST_USER", "") or ""
EMAIL_HOST_PASSWORD = env("EMAIL_HOST_PASSWORD", "") or ""
EMAIL_USE_TLS = env_bool("EMAIL_USE_TLS", False)
EMAIL_USE_SSL = env_bool("EMAIL_USE_SSL", True)
EMAIL_TIMEOUT = env_int("EMAIL_TIMEOUT", 15)
DEFAULT_FROM_EMAIL = env("DEFAULT_FROM_EMAIL", EMAIL_HOST_USER or "noreply@example.com") or "noreply@example.com"
EMAIL_PROVIDER = env("EMAIL_PROVIDER", "smtp") or "smtp"
TENCENTCLOUD_SECRET_ID = env("TENCENTCLOUD_SECRET_ID", "") or ""
TENCENTCLOUD_SECRET_KEY = env("TENCENTCLOUD_SECRET_KEY", "") or ""
TENCENTCLOUD_SES_REGION = env("TENCENTCLOUD_SES_REGION", "ap-hongkong") or "ap-hongkong"
TENCENTCLOUD_SES_FROM_EMAIL = env("TENCENTCLOUD_SES_FROM_EMAIL", "") or ""
TENCENTCLOUD_SES_FROM_ALIAS = env("TENCENTCLOUD_SES_FROM_ALIAS", BRAND_NAME) or BRAND_NAME
TENCENTCLOUD_SES_REPLY_TO = env("TENCENTCLOUD_SES_REPLY_TO", "") or ""
TENCENTCLOUD_SES_VERIFICATION_TEMPLATE_ID = env_int("TENCENTCLOUD_SES_VERIFICATION_TEMPLATE_ID", 0)
TENCENTCLOUD_SES_VERIFICATION_SUBJECT = env("TENCENTCLOUD_SES_VERIFICATION_SUBJECT", f"{SITE_NAME} 邮箱验证码") or f"{SITE_NAME} 邮箱验证码"

MINIO_ENDPOINT = env("MINIO_ENDPOINT", "127.0.0.1:9000") or ""
MINIO_ACCESS_KEY = env("MINIO_ACCESS_KEY", "minioadmin") or ""
MINIO_SECRET_KEY = env("MINIO_SECRET_KEY", "minioadmin123") or ""
MINIO_SECURE = env_bool("MINIO_SECURE", False)
MINIO_BUCKET = env("MINIO_BUCKET", "generated-images") or "generated-images"
MINIO_REGION = env("MINIO_REGION", "us-east-1") or "us-east-1"
MINIO_USE_LOCAL_FS = env_bool("MINIO_USE_LOCAL_FS", True)
MINIO_LOCAL_ROOT = BASE_DIR / (env("MINIO_LOCAL_ROOT", "media/minio") or "media/minio")

NOVELVIZ_BOOK_DIR = BASE_DIR / (env("NOVELVIZ_BOOK_DIR", "novel") or "novel")
NOVELVIZ_UPLOAD_DIR = env("NOVELVIZ_UPLOAD_DIR", "novelviz/uploads") or "novelviz/uploads"
NOVELVIZ_MAX_EPUB_UPLOAD_BYTES = env_int("NOVELVIZ_MAX_EPUB_UPLOAD_BYTES", 80 * 1024 * 1024)
NOVELVIZ_API_BASE_URL = env("NOVELVIZ_API_BASE_URL", LLM_BASE_URL or PROVIDER_BASE_URL) or ""
NOVELVIZ_TEXT_MODEL = env("NOVELVIZ_TEXT_MODEL", LLM_MODEL_NAME) or LLM_MODEL_NAME
NOVELVIZ_TEXT_API_PATH = env("NOVELVIZ_TEXT_API_PATH", LLM_API_PATH) or LLM_API_PATH
NOVELVIZ_TEXT_API_KEY = env("NOVELVIZ_TEXT_API_KEY", LLM_API_KEY) or ""
NOVELVIZ_TEXT_CONNECT_TIMEOUT_SECONDS = env_int("NOVELVIZ_TEXT_CONNECT_TIMEOUT_SECONDS", 30)
NOVELVIZ_TEXT_READ_TIMEOUT_SECONDS = env_int("NOVELVIZ_TEXT_READ_TIMEOUT_SECONDS", LLM_TIMEOUT_SECONDS)
NOVELVIZ_TEXT_MAX_RETRIES = env_int("NOVELVIZ_TEXT_MAX_RETRIES", LLM_MAX_RETRIES)
NOVELVIZ_IMAGE_MODEL = env("NOVELVIZ_IMAGE_MODEL", PROVIDER_MODEL_NAME) or PROVIDER_MODEL_NAME
NOVELVIZ_IMAGE_API_PATH = env("NOVELVIZ_IMAGE_API_PATH", PROVIDER_API_PATH) or PROVIDER_API_PATH
NOVELVIZ_IMAGE_API_KEY = env("NOVELVIZ_IMAGE_API_KEY", PROVIDER_API_KEY) or ""
NOVELVIZ_IMAGE_SIZE = env("NOVELVIZ_IMAGE_SIZE", "1024x1024") or "1024x1024"
NOVELVIZ_IMAGE_QUALITY = env("NOVELVIZ_IMAGE_QUALITY", "high") or "high"
NOVELVIZ_IMAGE_TIMEOUT_SECONDS = env_int("NOVELVIZ_IMAGE_TIMEOUT_SECONDS", PROVIDER_TIMEOUT_SECONDS)
NOVELVIZ_IMAGE_MAX_RETRIES = env_int("NOVELVIZ_IMAGE_MAX_RETRIES", PROVIDER_MAX_RETRIES)
NOVELVIZ_IMAGE_CONCURRENCY = env_int("NOVELVIZ_IMAGE_CONCURRENCY", 1)
NOVELVIZ_MAX_SCENES = env_int("NOVELVIZ_MAX_SCENES", 10)
NOVELVIZ_QUOTA_COST_PER_BOOK = env_int("NOVELVIZ_QUOTA_COST_PER_BOOK", 0)
NOVELVIZ_RUN_INLINE = env_bool("NOVELVIZ_RUN_INLINE", False)
NOVELVIZ_SSE_POLL_SECONDS = env_int("NOVELVIZ_SSE_POLL_SECONDS", 1)
NOVELVIZ_STALL_REQUEUE_SECONDS = env_int("NOVELVIZ_STALL_REQUEUE_SECONDS", 60)
NOVELVIZ_USE_SUBPROCESS = env_bool("NOVELVIZ_USE_SUBPROCESS", True)

FIELD_CRYPT_KEY = env("FIELD_CRYPT_KEY", "") or ""

LOGGING = {
    "version": 1,
    "disable_existing_loggers": False,
    "formatters": {
        "verbose": {
            "format": "%(asctime)s %(levelname)s %(name)s [%(request_id)s] %(message)s",
        }
    },
    "handlers": {
        "console": {
            "class": "logging.StreamHandler",
            "formatter": "verbose",
            "filters": ["request_context"],
        }
    },
    "filters": {
        "request_context": {
            "()": "apps.core.middleware.RequestContextLogFilter",
        }
    },
    "loggers": {
        "": {
            "handlers": ["console"],
            "level": "INFO",
        }
    },
}
