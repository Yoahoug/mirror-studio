from django.contrib import admin

from .models import GenerateTask, GenerationTopic, ImageChannel


@admin.register(ImageChannel)
class ImageChannelAdmin(admin.ModelAdmin):
    list_display = ("channel_code", "name", "channel_type", "provider_name", "model_name", "base_url", "is_active", "priority")
    list_filter = ("channel_type", "is_active")
    search_fields = ("channel_code", "name", "provider_name", "model_name")


@admin.register(GenerationTopic)
class GenerationTopicAdmin(admin.ModelAdmin):
    list_display = ("id", "title", "user", "last_task_at", "is_archived")
    list_filter = ("is_archived",)
    search_fields = ("title", "user__username")


@admin.register(GenerateTask)
class GenerateTaskAdmin(admin.ModelAdmin):
    list_display = ("task_no", "user", "status", "progress_percent", "style_code", "size_code", "quality_code", "channel", "created_at")
    list_filter = ("status", "style_code", "size_code", "quality_code", "channel")
    search_fields = ("task_no", "prompt", "user__username")
