from __future__ import annotations

from django.core.management.base import BaseCommand

from apps.mediahub.services import cleanup_media_storage


class Command(BaseCommand):
    help = "Clean expired generated images, temporary references, upload chunks, and old soft-deleted image files."

    def add_arguments(self, parser):
        parser.add_argument("--limit", type=int, default=100, help="Maximum rows/objects to process for each cleanup category.")

    def handle(self, *args, **options):
        limit = max(1, int(options["limit"]))
        result = cleanup_media_storage(limit=limit)
        self.stdout.write(self.style.SUCCESS(f"cleanup_media_storage: {result}"))
