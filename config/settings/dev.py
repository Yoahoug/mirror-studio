from .base import *  # noqa

DEBUG = True
CELERY_TASK_ALWAYS_EAGER = False
STATICFILES_STORAGE = "django.contrib.staticfiles.storage.StaticFilesStorage"

# Local development keeps using the project .env for DATABASE_URL, media,
# provider keys, and existing data, but must not force browser/API traffic to HTTPS.
SESSION_COOKIE_SECURE = False
CSRF_COOKIE_SECURE = False
SECURE_SSL_REDIRECT = False
SECURE_HSTS_SECONDS = 0
SECURE_HSTS_INCLUDE_SUBDOMAINS = False
SECURE_HSTS_PRELOAD = False

WORKSPACE_VITE_DEV_URL = (env("WORKSPACE_VITE_DEV_URL") or "http://127.0.0.1:3000").strip()
CONSOLE_VITE_DEV_URL = (env("CONSOLE_VITE_DEV_URL") or "http://127.0.0.1:3001").strip()
