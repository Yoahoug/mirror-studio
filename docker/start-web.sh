#!/bin/sh
set -e

python scripts/wait_for_db.py

if [ "${AUTO_APPLY_MIGRATIONS:-1}" = "1" ]; then
  python manage.py migrate --noinput
fi

if [ "${AUTO_SEED_SITECONFIG:-1}" = "1" ]; then
  python scripts/seed_siteconfig.py
fi

if [ "${AUTO_INIT_APP_DATA:-0}" = "1" ]; then
  python scripts/seed_demo_data.py
fi

if [ "${AUTO_COLLECTSTATIC:-1}" = "1" ]; then
  python manage.py collectstatic --noinput
fi

exec gunicorn config.wsgi:application \
  --bind 0.0.0.0:8000 \
  --workers "${GUNICORN_WORKERS:-1}" \
  --worker-class "${GUNICORN_WORKER_CLASS:-gthread}" \
  --threads "${GUNICORN_THREADS:-4}" \
  --timeout "${GUNICORN_TIMEOUT:-180}" \
  --graceful-timeout "${GUNICORN_GRACEFUL_TIMEOUT:-30}" \
  --keep-alive "${GUNICORN_KEEPALIVE:-10}" \
  --max-requests "${GUNICORN_MAX_REQUESTS:-800}" \
  --max-requests-jitter "${GUNICORN_MAX_REQUESTS_JITTER:-80}"
