#!/bin/sh
set -e

python scripts/wait_for_db.py

if [ "${GENERATION_TASKS_RUN_INLINE:-0}" = "1" ]; then
  echo "GENERATION_TASKS_RUN_INLINE=1，跳过独立 worker 轮询进程"
  exec sleep infinity
fi

exec python manage.py run_generation_worker
