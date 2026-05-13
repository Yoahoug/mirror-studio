from __future__ import annotations

from django.core.paginator import Paginator
from django.db.models import Q
from django.http import HttpRequest
from django.utils import timezone
from django.views import View

from apps.billing.services import admin_update_quota_settings, ensure_quota_account
from apps.core.api import InvalidPayloadError, api_error, api_success, parse_request_payload
from apps.core.siteconfig import SiteConfig, bulk_set_configs, get_configs_by_category, set_config
from apps.generator.models import GenerateTask, ImageChannel
from apps.mediahub.models import GeneratedImage
from apps.users.models import User

from .serializers import (
    serialize_config_list,
    serialize_failure_log,
    serialize_image_channel,
    serialize_image_channel_list,
    serialize_task,
    serialize_user,
    serialize_user_detail,
)


def _can_access_console(user) -> bool:
    return bool(
        user.is_authenticated
        and getattr(user, "is_operator", False)
        and getattr(user, "is_active_status", False)
    )


class ConsoleAPIMixin:
    def dispatch(self, request, *args, **kwargs):
        if not request.user.is_authenticated:
            return api_error("未登录", code=401000, status=401)
        if not getattr(request.user, "is_active_status", False):
            return api_error("当前账号状态不可用", code=403001, status=403)
        if not _can_access_console(request.user):
            return api_error("权限不足", code=403000, status=403)
        try:
            return super().dispatch(request, *args, **kwargs)
        except InvalidPayloadError as exc:
            return api_error(str(exc), code=400001)


def _parse_json_body(request: HttpRequest) -> dict:
    return parse_request_payload(request)


def _paginate(queryset, request, per_page: int = 20) -> tuple[list, dict]:
    try:
        page = max(int(request.GET.get("page", 1)), 1)
    except (TypeError, ValueError):
        page = 1
    try:
        per_page = int(request.GET.get("per_page", per_page))
    except (TypeError, ValueError):
        per_page = per_page
    per_page = min(max(per_page, 1), 100)
    paginator = Paginator(queryset, per_page)
    page_obj = paginator.get_page(page)
    return list(page_obj.object_list), {
        "page": page_obj.number,
        "per_page": per_page,
        "total": paginator.count,
        "total_pages": paginator.num_pages,
        "has_next": page_obj.has_next(),
        "has_previous": page_obj.has_previous(),
    }


class ConsoleDashboardAPI(ConsoleAPIMixin, View):
    def get(self, request):
        today = timezone.localdate()
        today_tasks = GenerateTask.objects.filter(created_at__date=today)
        recent_failures = (
            GenerateTask.objects.filter(status=GenerateTask.Status.FAILED)
            .select_related("user", "channel")
            .order_by("-finished_at", "-created_at")[:10]
        )
        data = {
            "today": {
                "new_users": User.objects.filter(date_joined__date=today).count(),
                "tasks": today_tasks.count(),
                "failed_tasks": today_tasks.filter(status=GenerateTask.Status.FAILED).count(),
                "succeeded_tasks": today_tasks.filter(status=GenerateTask.Status.SUCCEEDED).count(),
            },
            "totals": {
                "users": User.objects.count(),
                "images": GeneratedImage.objects.count(),
                "tasks": GenerateTask.objects.count(),
                "active_channels": ImageChannel.objects.filter(is_active=True).count(),
            },
            "latest_tasks": [
                serialize_task(task)
                for task in GenerateTask.objects.select_related("user", "channel").order_by("-created_at")[:10]
            ],
            "latest_users": [
                serialize_user(user)
                for user in User.objects.select_related("quota_account").order_by("-date_joined")[:10]
            ],
            "recent_failures": [serialize_failure_log(item) for item in recent_failures],
        }
        return api_success(data)


class ConsoleConfigListAPI(ConsoleAPIMixin, View):
    def get(self, request):
        category = request.GET.get("category")
        configs = get_configs_by_category(category) if category else SiteConfig.objects.all().order_by("category", "key")
        categories = [{"code": code, "label": label} for code, label in SiteConfig.CATEGORY_CHOICES]
        return api_success({"items": serialize_config_list(configs), "categories": categories})


class ConsoleConfigDetailAPI(ConsoleAPIMixin, View):
    def get(self, request, key: str):
        try:
            config = SiteConfig.objects.get(key=key)
        except SiteConfig.DoesNotExist:
            return api_error("配置项不存在", code=404000, status=404)
        return api_success(
            {
                "id": config.id,
                "key": config.key,
                "value": config.masked_value if config.is_secret else config.value,
                "is_secret": config.is_secret,
                "description": config.description,
                "category": config.category,
                "updated_at": config.updated_at.isoformat() if config.updated_at else None,
            }
        )

    def put(self, request, key: str):
        body = _parse_json_body(request)
        value = body.get("value")
        if value is None:
            return api_error("缺少 value", code=400001)
        config = set_config(
            key,
            str(value),
            is_secret=body.get("is_secret"),
            description=body.get("description"),
            category=body.get("category"),
        )
        return api_success({"key": config.key, "updated_at": config.updated_at.isoformat()})


class ConsoleConfigBatchUpdateAPI(ConsoleAPIMixin, View):
    def put(self, request):
        body = _parse_json_body(request)
        items = body.get("items")
        if not isinstance(items, list) or not items:
            return api_error("缺少 items 数组", code=400001)
        return api_success({"updated": bulk_set_configs(items)})


class ConsoleImageChannelListAPI(ConsoleAPIMixin, View):
    def get(self, request):
        channels = ImageChannel.objects.all().order_by("priority", "id")
        return api_success(serialize_image_channel_list(channels))

    def post(self, request):
        body = _parse_json_body(request)
        required = ["name", "channel_code", "base_url", "model_name"]
        for field in required:
            if not body.get(field):
                return api_error(f"缺少 {field}", code=400001)
        if body.get("channel_type") and body["channel_type"] not in ImageChannel.ChannelType.values:
            return api_error("不支持的模型类型", code=400001)
        if ImageChannel.objects.filter(channel_code=body["channel_code"]).exists():
            return api_error("渠道编码已存在", code=400002)

        channel = ImageChannel(
            name=body["name"],
            channel_code=body["channel_code"],
            channel_type=body.get("channel_type", ImageChannel.ChannelType.IMAGE),
            provider_name=body.get("provider_name", ""),
            base_url=body["base_url"],
            api_path=body.get("api_path", "/images/generations"),
            edit_api_path=body.get("edit_api_path", ""),
            model_name=body["model_name"],
            timeout_seconds=int(body.get("timeout_seconds", 60)),
            max_retries=int(body.get("max_retries", 3)),
            is_active=body.get("is_active", True),
            priority=int(body.get("priority", 1)),
        )
        if body.get("api_key"):
            channel.api_key = body["api_key"]
        channel.save()
        return api_success(serialize_image_channel(channel), status=201)


class ConsoleImageChannelDetailAPI(ConsoleAPIMixin, View):
    def get(self, request, pk: int):
        try:
            channel = ImageChannel.objects.get(pk=pk)
        except ImageChannel.DoesNotExist:
            return api_error("渠道不存在", code=404000, status=404)
        return api_success(serialize_image_channel(channel))

    def put(self, request, pk: int):
        try:
            channel = ImageChannel.objects.get(pk=pk)
        except ImageChannel.DoesNotExist:
            return api_error("渠道不存在", code=404000, status=404)

        body = _parse_json_body(request)
        update_fields = ["updated_at"]
        for field in ["name", "channel_type", "provider_name", "base_url", "api_path", "edit_api_path", "model_name", "is_active", "priority"]:
            if field in body:
                setattr(channel, field, body[field])
                update_fields.append(field)
        if "timeout_seconds" in body:
            channel.timeout_seconds = int(body["timeout_seconds"])
            update_fields.append("timeout_seconds")
        if "max_retries" in body:
            channel.max_retries = int(body["max_retries"])
            update_fields.append("max_retries")
        if body.get("api_key"):
            channel.api_key = body["api_key"]
            update_fields.append("api_key_encrypted")
        channel.save(update_fields=update_fields)
        return api_success(serialize_image_channel(channel))

    def delete(self, request, pk: int):
        try:
            channel = ImageChannel.objects.get(pk=pk)
        except ImageChannel.DoesNotExist:
            return api_error("渠道不存在", code=404000, status=404)
        if channel.tasks.exists():
            return api_error("该渠道有关联任务，无法删除，请先禁用", code=400003)
        channel.delete()
        return api_success({"deleted": True})


class ConsoleUserListAPI(ConsoleAPIMixin, View):
    def get(self, request):
        qs = User.objects.select_related("quota_account").all().order_by("-date_joined")
        keyword = request.GET.get("keyword", "").strip()
        if keyword:
            qs = qs.filter(Q(username__icontains=keyword) | Q(email__icontains=keyword) | Q(mobile__icontains=keyword))
        status = request.GET.get("status")
        if status:
            qs = qs.filter(status=status)
        role = request.GET.get("role")
        if role:
            qs = qs.filter(role=role)
        items, pagination = _paginate(qs, request)
        for user in items:
            if not getattr(user, "quota_account", None):
                ensure_quota_account(user)
        return api_success({"items": [serialize_user(user) for user in items], "pagination": pagination})


class ConsoleUserDetailAPI(ConsoleAPIMixin, View):
    def get(self, request, pk: int):
        try:
            user = User.objects.select_related("quota_account", "profile").get(pk=pk)
        except User.DoesNotExist:
            return api_error("用户不存在", code=404000, status=404)
        ensure_quota_account(user)
        user.refresh_from_db()
        return api_success(serialize_user_detail(user))


class ConsoleUserQuotaUpdateAPI(ConsoleAPIMixin, View):
    def post(self, request, pk: int):
        try:
            user = User.objects.get(pk=pk)
        except User.DoesNotExist:
            return api_error("用户不存在", code=404000, status=404)

        body = _parse_json_body(request)
        daily_quota = body.get("daily_quota")
        available_quota = body.get("available_quota")
        remark = (body.get("remark") or "").strip()

        if daily_quota is None and available_quota is None:
            return api_error("至少传入 daily_quota 或 available_quota", code=400001)
        try:
            account = admin_update_quota_settings(
                user=user,
                daily_quota=int(daily_quota) if daily_quota is not None else None,
                available_quota=int(available_quota) if available_quota is not None else None,
                remark=f"[管理员:{request.user.username}] {remark or '后台修改免费次数'}",
            )
        except (TypeError, ValueError) as exc:
            return api_error(str(exc), code=400002)
        return api_success(
            {
                "user_id": user.id,
                "daily_quota": account.daily_quota,
                "available_quota": account.available_quota,
                "frozen_quota": account.frozen_quota,
                "total_generated_count": account.total_generated_count,
                "last_reset_date": account.last_reset_date.isoformat() if account.last_reset_date else None,
            }
        )


class ConsoleTaskListAPI(ConsoleAPIMixin, View):
    def get(self, request):
        qs = GenerateTask.objects.select_related("user", "channel").order_by("-created_at")
        keyword = request.GET.get("keyword", "").strip()
        if keyword:
            qs = qs.filter(Q(task_no__icontains=keyword) | Q(user__username__icontains=keyword) | Q(prompt__icontains=keyword))
        status = request.GET.get("status")
        if status:
            qs = qs.filter(status=status)
        items, pagination = _paginate(qs, request)
        return api_success({"items": [serialize_task(task) for task in items], "pagination": pagination})
