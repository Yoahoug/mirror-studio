from __future__ import annotations

import logging
import threading
import time

from django.conf import settings
from django.core.management.base import BaseCommand

from apps.generator.tasks import run_generation_worker_once

logger = logging.getLogger(__name__)


class Command(BaseCommand):
    help = "Run the generation task worker loop (concurrent)."

    def handle(self, *args, **options):
        poll_seconds = max(float(getattr(settings, "GENERATION_WORKER_POLL_SECONDS", 2) or 2), 0.2)
        workers = max(int(getattr(settings, "GENERATION_WORKER_CONCURRENCY", 4) or 1), 1)
        self.stdout.write(
            self.style.SUCCESS(f"generation worker started (poll={poll_seconds:.1f}s concurrency={workers})")
        )

        def loop(worker_index: int) -> None:
            while True:
                try:
                    processed_task_no = run_generation_worker_once()
                    if processed_task_no:
                        self.stdout.write(f"worker[{worker_index}] processed task {processed_task_no}")
                    else:
                        time.sleep(poll_seconds)
                except Exception:
                    logger.exception("generation worker[%s] loop error, backing off", worker_index)
                    time.sleep(poll_seconds)

        threads = [threading.Thread(target=loop, args=(i,), daemon=True, name=f"gen-worker-{i}") for i in range(workers)]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()
