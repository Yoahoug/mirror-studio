from __future__ import annotations

from django.core.management.base import BaseCommand, CommandError

from apps.novelviz.services import run_novel_visualization_task


class Command(BaseCommand):
    help = "Run one novel visualization task by id."

    def add_arguments(self, parser):
        parser.add_argument("task_id", type=int)

    def handle(self, *args, **options):
        task_id = int(options["task_id"])
        if task_id <= 0:
            raise CommandError("task_id must be positive")
        self.stdout.write(f"novel visualization task started: {task_id}")
        run_novel_visualization_task(task_id)
        self.stdout.write(self.style.SUCCESS(f"novel visualization task finished: {task_id}"))
