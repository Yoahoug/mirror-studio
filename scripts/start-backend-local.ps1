$ErrorActionPreference = "Stop"

$Root = Resolve-Path (Join-Path $PSScriptRoot "..")
$Python = Join-Path $Root ".venv\Scripts\python.exe"

if (-not (Test-Path $Python)) {
  throw "未找到 .venv，请先运行：python -m venv .venv; .\.venv\Scripts\python.exe -m pip install -r requirements\dev.txt"
}

Set-Location $Root

$env:DJANGO_SETTINGS_MODULE = "config.settings.dev"
$env:DATABASE_URL = "sqlite:///db.sqlite3"
$env:CACHE_BACKEND = "django.core.cache.backends.locmem.LocMemCache"
$env:MINIO_USE_LOCAL_FS = "1"
$env:CELERY_TASK_ALWAYS_EAGER = "1"
$env:SITE_DOMAIN = "http://127.0.0.1:8000"
$env:ALLOWED_HOSTS = "localhost,127.0.0.1,testserver"
$env:CSRF_TRUSTED_ORIGINS = "http://127.0.0.1:8000,http://localhost:8000"

& $Python manage.py migrate
& $Python scripts/seed_siteconfig.py
& $Python manage.py runserver 127.0.0.1:8000
