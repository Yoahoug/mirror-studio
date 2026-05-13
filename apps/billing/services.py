from __future__ import annotations

import secrets

from django.conf import settings
from django.db import transaction
from django.utils import timezone

from apps.core.siteconfig import get_config_int

from .models import QuotaAccount, QuotaLedger


def generate_ledger_no() -> str:
    return f"L{timezone.now():%Y%m%d%H%M%S}{secrets.token_hex(4).upper()}"


def get_default_daily_quota() -> int:
    return max(get_config_int("site.default_daily_free_quota", settings.DEFAULT_DAILY_FREE_QUOTA), 0)


def _append_ledger(
    *,
    account: QuotaAccount,
    change_type: str,
    direction: str,
    amount: int,
    before_available: int,
    before_frozen: int,
    remark: str = "",
    related_task_no: str = "",
) -> QuotaLedger:
    return QuotaLedger.objects.create(
        ledger_no=generate_ledger_no(),
        user=account.user,
        change_type=change_type,
        direction=direction,
        amount=amount,
        before_available=before_available,
        after_available=account.available_quota,
        before_frozen=before_frozen,
        after_frozen=account.frozen_quota,
        related_task_no=related_task_no,
        remark=remark,
    )


@transaction.atomic
def ensure_quota_account(user, *, lock: bool = False) -> QuotaAccount:
    default_daily_quota = get_default_daily_quota()
    manager = QuotaAccount.objects.select_for_update() if lock else QuotaAccount.objects
    account, created = manager.get_or_create(
        user=user,
        defaults={
            "daily_quota": default_daily_quota,
            "available_quota": default_daily_quota,
            "last_reset_date": timezone.localdate(),
        },
    )
    if created:
        return account
    if account.daily_quota < 0:
        account.daily_quota = 0
        account.save(update_fields=["daily_quota", "updated_at"])
    return refresh_daily_quota(account=account, lock=lock)


@transaction.atomic
def refresh_daily_quota(*, account: QuotaAccount, lock: bool = False) -> QuotaAccount:
    if lock:
        account = QuotaAccount.objects.select_for_update().get(pk=account.pk)
    today = timezone.localdate()
    if account.last_reset_date >= today:
        return account
    before_available = account.available_quota
    before_frozen = account.frozen_quota
    account.available_quota = account.daily_quota
    account.last_reset_date = today
    account.save(update_fields=["available_quota", "last_reset_date", "updated_at"])
    _append_ledger(
        account=account,
        change_type=QuotaLedger.ChangeType.RESET,
        direction=QuotaLedger.Direction.NONE,
        amount=account.daily_quota,
        before_available=before_available,
        before_frozen=before_frozen,
        remark=f"每日免费次数刷新为 {account.daily_quota}",
    )
    return account


def get_quota_summary(user) -> dict:
    account = ensure_quota_account(user)
    return {
        "daily_quota": account.daily_quota,
        "available_quota": account.available_quota,
        "frozen_quota": account.frozen_quota,
        "total_generated_count": account.total_generated_count,
        "last_reset_date": account.last_reset_date.isoformat() if account.last_reset_date else None,
    }


@transaction.atomic
def freeze_quota_for_task(*, user, task_no: str, amount: int = 1) -> None:
    account = ensure_quota_account(user, lock=True)
    if account.available_quota < amount:
        raise ValueError("今日免费次数已用完")
    before_available = account.available_quota
    before_frozen = account.frozen_quota
    account.available_quota -= amount
    account.frozen_quota += amount
    account.save(update_fields=["available_quota", "frozen_quota", "updated_at"])
    _append_ledger(
        account=account,
        change_type=QuotaLedger.ChangeType.FREEZE,
        direction=QuotaLedger.Direction.OUT,
        amount=amount,
        before_available=before_available,
        before_frozen=before_frozen,
        remark="任务提交冻结次数",
        related_task_no=task_no,
    )


@transaction.atomic
def consume_frozen_quota(*, user, task_no: str, amount: int = 1) -> None:
    account = ensure_quota_account(user, lock=True)
    if account.frozen_quota < amount:
        raise ValueError("冻结次数不足")
    before_available = account.available_quota
    before_frozen = account.frozen_quota
    account.frozen_quota -= amount
    account.total_generated_count += amount
    account.save(update_fields=["frozen_quota", "total_generated_count", "updated_at"])
    _append_ledger(
        account=account,
        change_type=QuotaLedger.ChangeType.CONSUME,
        direction=QuotaLedger.Direction.OUT,
        amount=amount,
        before_available=before_available,
        before_frozen=before_frozen,
        remark="任务成功扣减次数",
        related_task_no=task_no,
    )


@transaction.atomic
def release_frozen_quota(*, user, task_no: str, amount: int = 1, remark: str = "任务失败退回") -> None:
    account = ensure_quota_account(user, lock=True)
    if account.frozen_quota < amount:
        amount = max(account.frozen_quota, 0)
    if amount <= 0:
        return
    before_available = account.available_quota
    before_frozen = account.frozen_quota
    account.frozen_quota -= amount
    account.available_quota += amount
    account.save(update_fields=["available_quota", "frozen_quota", "updated_at"])
    _append_ledger(
        account=account,
        change_type=QuotaLedger.ChangeType.UNFREEZE,
        direction=QuotaLedger.Direction.IN,
        amount=amount,
        before_available=before_available,
        before_frozen=before_frozen,
        remark=remark,
        related_task_no=task_no,
    )


@transaction.atomic
def admin_adjust_quota(*, user, amount: int, remark: str) -> QuotaAccount:
    account = ensure_quota_account(user, lock=True)
    if account.available_quota + amount < 0:
        raise ValueError("当前剩余次数不能小于 0")
    before_available = account.available_quota
    before_frozen = account.frozen_quota
    account.available_quota += amount
    account.save(update_fields=["available_quota", "updated_at"])
    _append_ledger(
        account=account,
        change_type=QuotaLedger.ChangeType.ADMIN_ADJUST,
        direction=QuotaLedger.Direction.IN if amount >= 0 else QuotaLedger.Direction.OUT,
        amount=abs(amount),
        before_available=before_available,
        before_frozen=before_frozen,
        remark=remark,
    )
    return account


@transaction.atomic
def admin_update_quota_settings(
    *,
    user,
    daily_quota: int | None = None,
    available_quota: int | None = None,
    remark: str = "",
) -> QuotaAccount:
    account = ensure_quota_account(user, lock=True)
    before_available = account.available_quota
    before_frozen = account.frozen_quota
    changed = False

    if daily_quota is not None:
        if daily_quota < 0:
            raise ValueError("每日免费次数不能小于 0")
        account.daily_quota = daily_quota
        changed = True
        _append_ledger(
            account=account,
            change_type=QuotaLedger.ChangeType.DAILY_QUOTA_UPDATE,
            direction=QuotaLedger.Direction.NONE,
            amount=daily_quota,
            before_available=before_available,
            before_frozen=before_frozen,
            remark=remark or f"每日免费次数调整为 {daily_quota}",
        )

    if available_quota is not None:
        if available_quota < 0:
            raise ValueError("当前剩余次数不能小于 0")
        delta = available_quota - account.available_quota
        account.available_quota = available_quota
        changed = True
        if delta != 0:
            _append_ledger(
                account=account,
                change_type=QuotaLedger.ChangeType.ADMIN_ADJUST,
                direction=QuotaLedger.Direction.IN if delta > 0 else QuotaLedger.Direction.OUT,
                amount=abs(delta),
                before_available=before_available,
                before_frozen=before_frozen,
                remark=remark or "管理员直接设置当前剩余次数",
            )

    if changed:
        account.save(update_fields=["daily_quota", "available_quota", "updated_at"])
    return account
