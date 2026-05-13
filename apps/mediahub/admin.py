from django.contrib import admin

from .models import GeneratedImage


@admin.register(GeneratedImage)
class GeneratedImageAdmin(admin.ModelAdmin):
    list_display = ("id", "task", "user", "status", "width", "height", "expires_at", "created_at")
    list_filter = ("status", "storage_provider")
    search_fields = ("task__task_no", "user__username", "object_key")

