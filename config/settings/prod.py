from django.core.exceptions import ImproperlyConfigured

from .base import *  # noqa

DEBUG = False
WORKSPACE_VITE_DEV_URL = ""
CONSOLE_VITE_DEV_URL = ""
USE_X_FORWARDED_HOST = env_bool("USE_X_FORWARDED_HOST", True)
SECURE_PROXY_SSL_HEADER = ("HTTP_X_FORWARDED_PROTO", env("SECURE_PROXY_SSL_HEADER_VALUE", "https") or "https")
TRUST_X_FORWARDED_FOR = env_bool("TRUST_X_FORWARDED_FOR", False)
MOCK_PROVIDER_ENABLED = env_bool("MOCK_PROVIDER_ENABLED", False)
SESSION_COOKIE_SECURE = env_bool("SESSION_COOKIE_SECURE", True)
CSRF_COOKIE_SECURE = env_bool("CSRF_COOKIE_SECURE", True)
SECURE_SSL_REDIRECT = env_bool("SECURE_SSL_REDIRECT", True)
SECURE_HSTS_SECONDS = env_int("SECURE_HSTS_SECONDS", 31536000)
SECURE_HSTS_INCLUDE_SUBDOMAINS = env_bool("SECURE_HSTS_INCLUDE_SUBDOMAINS", True)
SECURE_HSTS_PRELOAD = env_bool("SECURE_HSTS_PRELOAD", False)

if not env_bool("ALLOW_INSECURE_PROD", False):
    if not SECRET_KEY or SECRET_KEY == "replace-me":
        raise ImproperlyConfigured("生产环境必须配置安全的 SECRET_KEY")
    if not PUBLIC_ORIGIN:
        raise ImproperlyConfigured("生产环境必须配置 PUBLIC_ORIGIN，例如 https://image.example.com")
    if MOCK_PROVIDER_ENABLED:
        raise ImproperlyConfigured("生产环境禁止启用 MOCK_PROVIDER_ENABLED")
