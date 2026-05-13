from django.contrib import admin

from .models import IPAccessRule, RiskEvent, SensitiveWordRule


@admin.register(SensitiveWordRule)
class SensitiveWordRuleAdmin(admin.ModelAdmin):
    list_display = ("keyword", "match_mode", "is_active", "updated_at")
    list_filter = ("match_mode", "is_active")
    search_fields = ("keyword",)


@admin.register(IPAccessRule)
class IPAccessRuleAdmin(admin.ModelAdmin):
    list_display = ("ip_address", "is_blocked", "expires_at", "updated_at")
    list_filter = ("is_blocked",)
    search_fields = ("ip_address", "reason")


@admin.register(RiskEvent)
class RiskEventAdmin(admin.ModelAdmin):
    list_display = ("event_type", "user", "request_ip", "created_at")
    list_filter = ("event_type",)
    search_fields = ("request_ip", "detail_json")

