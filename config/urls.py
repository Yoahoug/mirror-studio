import json
from pathlib import Path

from django.conf import settings
from django.conf.urls.static import static
from django.contrib.auth.mixins import LoginRequiredMixin, UserPassesTestMixin
from django.contrib.auth.views import redirect_to_login
from django.core.exceptions import PermissionDenied
from django.urls import include, path
from django.views.generic import TemplateView, RedirectView

from apps.core.views import ChatAPIView, ChatModelListAPIView, HealthCheckView, InternalReleasePublishAPIView
from apps.core.siteconfig import get_config
from apps.users.utils import generate_captcha_code, render_captcha_data_uri


def _workspace_asset_version() -> str:
    assets_dir = settings.BASE_DIR / "static" / "workspace" / "assets"
    try:
        mtimes = [
            path.stat().st_mtime_ns
            for path in Path(assets_dir).glob("workspace.*")
            if path.is_file()
        ]
    except OSError:
        mtimes = []
    if mtimes:
        return str(max(mtimes))
    return settings.WORKSPACE_RELEASE_VERSION


def _can_access_console(user) -> bool:
    return bool(
        user.is_authenticated
        and getattr(user, "is_operator", False)
        and getattr(user, "is_active_status", False)
    )


class WorkspaceView(TemplateView):
    """绘镜创作台 — 提供 React SPA 前端。"""
    template_name = "workspace/index.html"

    def get_context_data(self, **kwargs):
        ctx = super().get_context_data(**kwargs)
        ctx["BRAND_NAME"] = get_config("site.brand_name", "绘镜")
        ctx["IS_AUTHENTICATED"] = self.request.user.is_authenticated
        ctx["CAPTCHA_CODE"] = ""
        ctx["CAPTCHA_SVG"] = ""
        if not self.request.user.is_authenticated and settings.LOGIN_CAPTCHA_ENABLED:
            ctx["CAPTCHA_CODE"] = generate_captcha_code()
            ctx["CAPTCHA_SVG"] = render_captcha_data_uri(ctx["CAPTCHA_CODE"])
            self.request.session["captcha_code"] = ctx["CAPTCHA_CODE"]
        ctx["WORKSPACE_RELEASE_JSON"] = json.dumps(
            {
                "version": get_config("site.workspace_release_version", settings.WORKSPACE_RELEASE_VERSION) or settings.WORKSPACE_RELEASE_VERSION,
                "title": get_config("site.workspace_release_title", settings.WORKSPACE_RELEASE_TITLE) or settings.WORKSPACE_RELEASE_TITLE,
                "notes": get_config("site.workspace_release_notes", settings.WORKSPACE_RELEASE_NOTES) or settings.WORKSPACE_RELEASE_NOTES,
                "published_at": get_config("site.workspace_release_published_at", settings.WORKSPACE_RELEASE_PUBLISHED_AT) or settings.WORKSPACE_RELEASE_PUBLISHED_AT,
            },
            ensure_ascii=False,
        )
        # 仅在显式配置 Vite 开发地址时才走开发服务器，避免只启动 Django 时出现白屏。
        ctx["WORKSPACE_VITE_DEV_URL"] = settings.WORKSPACE_VITE_DEV_URL
        ctx["WORKSPACE_ASSET_VERSION"] = _workspace_asset_version()
        return ctx


class ConsoleView(LoginRequiredMixin, UserPassesTestMixin, TemplateView):
    """运营控制台 — 提供 React SPA 前端。"""
    template_name = "console/index.html"
    login_url = "/workspace?auth=login"

    def test_func(self):
        return _can_access_console(self.request.user)

    def handle_no_permission(self):
        if not self.request.user.is_authenticated:
            return redirect_to_login(self.request.get_full_path(), self.get_login_url())
        if not getattr(self.request.user, "is_active_status", False):
            raise PermissionDenied("当前账号状态不可用")
        raise PermissionDenied("当前账号没有管理员权限")

    def get_context_data(self, **kwargs):
        ctx = super().get_context_data(**kwargs)
        ctx["BRAND_NAME"] = get_config("site.brand_name", "绘镜")
        ctx["CONSOLE_VITE_DEV_URL"] = settings.CONSOLE_VITE_DEV_URL
        return ctx


urlpatterns = [
    path("admin", RedirectView.as_view(url="/console/", permanent=False), name="admin-console-entry-no-slash"),
    path("admin/", RedirectView.as_view(url="/console/", permanent=False), name="admin-console-entry"),
    path("healthz", HealthCheckView.as_view(), name="healthz"),
    path("api/chat/models", ChatModelListAPIView.as_view(), name="api-chat-models"),
    path("api/chat", ChatAPIView.as_view(), name="api-chat"),
    path("api/internal/release/publish", InternalReleasePublishAPIView.as_view(), name="api-internal-release-publish"),
    path("", RedirectView.as_view(url="/workspace", permanent=False), name="home"),
    path("workspace", WorkspaceView.as_view(), name="workspace"),
    path("console", ConsoleView.as_view(), name="console"),
    path("console/", ConsoleView.as_view(), name="console-slash"),
    path("", include("apps.users.urls")),
    path("", include("apps.generator.urls")),
    path("", include("apps.mediahub.urls")),
    path("", include("apps.novelviz.urls")),
    path("", include("apps.console.urls")),
]

if settings.DEBUG:
    urlpatterns += static(settings.MEDIA_URL, document_root=settings.MEDIA_ROOT)
