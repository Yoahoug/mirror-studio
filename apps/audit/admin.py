from django.contrib import admin

from .models import AdminOperationLog, CallbackEventLog


@admin.register(AdminOperationLog)
class AdminOperationLogAdmin(admin.ModelAdmin):
    list_display = ("action", "target_type", "target_id", "admin_user", "request_ip", "created_at")
    list_filter = ("action", "target_type")
    search_fields = ("target_id", "detail_json")


@admin.register(CallbackEventLog)
class CallbackEventLogAdmin(admin.ModelAdmin):
    list_display = ("event_type", "source", "success", "created_at")
    list_filter = ("event_type", "source", "success")
    search_fields = ("remark", "payload_json")

