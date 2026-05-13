from __future__ import annotations

import time

from django.conf import settings
from django.core.management.base import BaseCommand

from apps.generator.tasks import run_generation_worker_once


class Command(BaseCommand):
    help = "Run the generation task worker loop."

    def handle(self, *args, **options):
        poll_seconds = max(float(getattr(settings, "GENERATION_WORKER_POLL_SECONDS", 2) or 2), 0.2)
        self.stdout.write(self.style.SUCCESS(f"generation worker started (poll={poll_seconds:.1f}s)"))
        while True:
            processed_task_no = run_generation_worker_once()
            if processed_task_no:
                self.stdout.write(f"processed task {processed_task_no}")
                continue
            time.sleep(poll_seconds)
