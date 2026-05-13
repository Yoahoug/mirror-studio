from django.contrib import admin

from .models import QuotaAccount, QuotaLedger


@admin.register(QuotaAccount)
class QuotaAccountAdmin(admin.ModelAdmin):
    list_display = ("user", "daily_quota", "available_quota", "frozen_quota", "last_reset_date", "updated_at")
    search_fields = ("user__username", "user__email")
    list_filter = ("last_reset_date",)


@admin.register(QuotaLedger)
class QuotaLedgerAdmin(admin.ModelAdmin):
    list_display = ("ledger_no", "user", "change_type", "direction", "amount", "after_available", "created_at")
    search_fields = ("ledger_no", "user__username", "related_task_no", "remark")
    list_filter = ("change_type", "direction")
