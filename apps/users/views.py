from __future__ import annotations

from django.conf import settings
from django.contrib import messages
from django.contrib.auth import get_user_model, login, logout, update_session_auth_hash
from django.contrib.auth.mixins import LoginRequiredMixin
from django.contrib.auth.password_validation import validate_password
from django.core.exceptions import ImproperlyConfigured, ValidationError
from django.core.validators import URLValidator
from django.db import transaction
from django.http import HttpRequest
from django.shortcuts import redirect
from django.urls import reverse_lazy
from django.utils import timezone
from django.utils.http import url_has_allowed_host_and_scheme
from django.views import View
from django.views.generic import FormView, TemplateView

from apps.audit.services import log_callback_event
from apps.billing.services import get_quota_summary
from apps.core.api import InvalidPayloadError, api_error, api_success, parse_request_payload
from apps.generator.models import GenerateTask
from apps.generator.services import reconcile_recent_tasks
from apps.risk.services import (
    clear_login_failures,
    ensure_login_allowed,
    record_login_failure,
)

from .email_verification import (
    EmailVerificationRateLimitError,
    EmailVerificationSendError,
    consume_register_email_code,
    email_verification_resend_seconds,
    issue_register_email_code,
    is_email_verification_enabled,
)
from .forms import LoginForm, RegisterAvailabilityForm, RegisterEmailCodeForm, RegisterForm, ResetPasswordForm
from .models import UserProfile
from .utils import generate_captcha_code, render_captcha_data_uri

User = get_user_model()
avatar_url_validator = URLValidator(schemes=["http", "https"])


def _issue_captcha(request: HttpRequest) -> tuple[str, str]:
    code = generate_captcha_code()
    request.session["captcha_code"] = code
    return code, render_captcha_data_uri(code)


def _parse_payload(request: HttpRequest) -> dict:
    return parse_request_payload(request)


def _first_form_error(form) -> str:
    for errors in form.errors.values():
        if errors:
            return errors[0]
    return "请求参数有误"


class LoginPageView(FormView):
    template_name = "account/login.html"
    form_class = LoginForm
    success_url = reverse_lazy("workspace")

    def get_form_kwargs(self):
        kwargs = super().get_form_kwargs()
        kwargs["request"] = self.request
        return kwargs

    def get_success_url(self):
        next_url = self.request.POST.get("next") or self.request.GET.get("next")
        if next_url and url_has_allowed_host_and_scheme(
            next_url,
            allowed_hosts={self.request.get_host()},
            require_https=self.request.is_secure(),
        ):
            return next_url
        return str(self.success_url)

    def get_context_data(self, **kwargs):
        context = super().get_context_data(**kwargs)
        context["captcha_code"], context["captcha_svg"] = _issue_captcha(self.request)
        return context

    def form_valid(self, form):
        user = form.cleaned_data["user"]
        try:
            ensure_login_allowed(user.username, self.request.client_ip)
        except PermissionError as exc:
            form.add_error(None, str(exc))
            return self.form_invalid(form)
        login(self.request, user)
        user.last_login_at = timezone.now()
        user.last_login_ip = self.request.client_ip
        user.save(update_fields=["last_login_at", "last_login_ip"])
        clear_login_failures(user.username, self.request.client_ip)
        return super().form_valid(form)

    def form_invalid(self, form):
        username = self.request.POST.get("username", "")
        if username:
            record_login_failure(username, self.request.client_ip)
        return super().form_invalid(form)


class RegisterPageView(FormView):
    template_name = "account/register.html"
    form_class = RegisterForm
    success_url = reverse_lazy("workspace")

    def get_success_url(self):
        return str(self.success_url)

    def dispatch(self, request, *args, **kwargs):
        if not settings.REGISTER_ENABLED:
            messages.error(request, "当前站点已关闭注册")
            return redirect("home")
        return super().dispatch(request, *args, **kwargs)

    def get_form_kwargs(self):
        kwargs = super().get_form_kwargs()
        kwargs["request"] = self.request
        return kwargs

    def get_context_data(self, **kwargs):
        context = super().get_context_data(**kwargs)
        context["email_code_resend_seconds"] = email_verification_resend_seconds()
        context["email_verification_enabled"] = is_email_verification_enabled()
        return context

    def form_valid(self, form):
        try:
            with transaction.atomic():
                consume_register_email_code(
                    form.cleaned_data["email"],
                    form.cleaned_data["email_verification_code"],
                )
                user = User.objects.create_user(
                    username=form.cleaned_data["username"],
                    email=form.cleaned_data["email"],
                    password=form.cleaned_data["password"],
                )
                user.profile.register_ip = self.request.client_ip
                user.profile.save(update_fields=["register_ip"])
        except ValidationError as exc:
            form.add_error("email_verification_code", exc)
            return self.form_invalid(form)
        login(self.request, user)
        return super().form_valid(form)


class ForgotPasswordPageView(FormView):
    template_name = "account/forgot_password.html"
    form_class = ResetPasswordForm
    success_url = reverse_lazy("login")

    def get_form_kwargs(self):
        kwargs = super().get_form_kwargs()
        kwargs["request"] = self.request
        return kwargs

    def get_context_data(self, **kwargs):
        context = super().get_context_data(**kwargs)
        context["captcha_code"], context["captcha_svg"] = _issue_captcha(self.request)
        return context

    def form_valid(self, form):
        user = form.cleaned_data["user"]
        user.set_password(form.cleaned_data["new_password"])
        user.save(update_fields=["password"])
        messages.success(self.request, "密码已重置，请重新登录")
        return super().form_valid(form)


class AccountDashboardView(LoginRequiredMixin, TemplateView):
    template_name = "account/dashboard.html"

    def get_context_data(self, **kwargs):
        context = super().get_context_data(**kwargs)
        context["quota_summary"] = get_quota_summary(self.request.user)
        return context


class SecurityPageView(LoginRequiredMixin, TemplateView):
    template_name = "account/security.html"

    def get_context_data(self, **kwargs):
        context = super().get_context_data(**kwargs)
        context["captcha_code"], context["captcha_svg"] = _issue_captcha(self.request)
        return context


def _serialize_account_settings(user: User) -> dict:
    profile, _ = UserProfile.objects.get_or_create(user=user, defaults={"nickname": user.username})
    quota = get_quota_summary(user)
    nickname = profile.nickname or user.username
    avatar = profile.avatar.strip() if profile.avatar else ""
    return {
        "user": {
            "username": user.username,
            "nickname": nickname,
            "email": user.email or "",
            "avatar": avatar,
            "remark": profile.remark or "",
            "role": user.get_role_display() if hasattr(user, "get_role_display") else user.role,
            "status": user.get_status_display() if hasattr(user, "get_status_display") else user.status,
            "date_joined": user.date_joined.isoformat() if user.date_joined else None,
            "last_login_at": user.last_login_at.isoformat() if user.last_login_at else None,
            "last_login_ip": str(user.last_login_ip) if user.last_login_ip else "",
            "register_source": profile.register_source or "website",
        },
        "quota": quota,
    }


class AccountSettingsAPIView(LoginRequiredMixin, View):
    def get(self, request: HttpRequest):
        return api_success(_serialize_account_settings(request.user))

    def patch(self, request: HttpRequest):
        try:
            payload = _parse_payload(request)
        except InvalidPayloadError as exc:
            return api_error(str(exc), code=400001)

        profile, _ = UserProfile.objects.get_or_create(user=request.user, defaults={"nickname": request.user.username})
        nickname = str(payload.get("nickname") or "").strip()[:64]
        email = str(payload.get("email") or "").strip().lower()
        avatar = str(payload.get("avatar") or "").strip()
        remark = str(payload.get("remark") or "").strip()[:500]

        if not nickname:
            return api_error("昵称不能为空", code=400001)
        if email and User.objects.exclude(pk=request.user.pk).filter(email=email).exists():
            return api_error("邮箱已存在", code=400001)
        if avatar:
            try:
                avatar_url_validator(avatar)
            except ValidationError:
                return api_error("头像地址格式不正确", code=400001)

        request.user.email = email or None
        request.user.save(update_fields=["email"])
        profile.nickname = nickname
        profile.avatar = avatar
        profile.remark = remark
        profile.save(update_fields=["nickname", "avatar", "remark", "updated_at"])
        return api_success(_serialize_account_settings(request.user), message="资料已更新")


class AccountPasswordAPIView(LoginRequiredMixin, View):
    def post(self, request: HttpRequest):
        try:
            payload = _parse_payload(request)
        except InvalidPayloadError as exc:
            return api_error(str(exc), code=400001)

        current_password = str(payload.get("current_password") or "")
        new_password = str(payload.get("new_password") or "")
        confirm_password = str(payload.get("confirm_password") or "")

        if not current_password:
            return api_error("请输入当前密码", code=400001)
        if not request.user.check_password(current_password):
            return api_error("当前密码不正确", code=400001)
        if not new_password:
            return api_error("请输入新密码", code=400001)
        if new_password != confirm_password:
            return api_error("两次密码输入不一致", code=400001)

        try:
            validate_password(new_password, user=request.user)
        except ValidationError as exc:
            return api_error(exc.messages[0] if exc.messages else "新密码不符合要求", code=400001)

        request.user.set_password(new_password)
        request.user.save(update_fields=["password"])
        update_session_auth_hash(request, request.user)
        log_callback_event(
            event_type="password_change",
            source="users",
            payload={"username": request.user.username},
            success=True,
        )
        return api_success({}, message="密码已更新")


class AccountSummaryAPIView(LoginRequiredMixin, View):
    def get(self, request: HttpRequest):
        recent_tasks = list(
            GenerateTask.objects.filter(user=request.user).select_related("channel").order_by("-created_at")[:3]
        )
        recent_tasks = reconcile_recent_tasks(recent_tasks)
        profile, _ = UserProfile.objects.get_or_create(user=request.user, defaults={"nickname": request.user.username})
        return api_success(
            {
                "user": {
                    "username": request.user.username,
                    "nickname": profile.nickname or request.user.username,
                    "email": request.user.email or "",
                    "avatar": profile.avatar.strip() if profile.avatar else "",
                    "remark": profile.remark or "",
                },
                "quota": get_quota_summary(request.user),
                "recent_tasks": [
                    {
                        "task_no": task.task_no,
                        "status": task.status,
                        "created_at": task.created_at.isoformat() if task.created_at else None,
                    }
                    for task in recent_tasks
                ],
            }
        )


class RegisterAPIView(View):
    def post(self, request: HttpRequest):
        if not settings.REGISTER_ENABLED:
            return api_error("当前站点已关闭注册", code=400403, status=403)
        try:
            payload = _parse_payload(request)
        except InvalidPayloadError as exc:
            return api_error(str(exc), code=400001)
        form = RegisterForm(payload, request=request)
        if not form.is_valid():
            return api_error(form.errors.as_json(), code=400001)
        try:
            with transaction.atomic():
                consume_register_email_code(
                    form.cleaned_data["email"],
                    form.cleaned_data["email_verification_code"],
                )
                user = User.objects.create_user(
                    username=form.cleaned_data["username"],
                    email=form.cleaned_data["email"],
                    password=form.cleaned_data["password"],
                )
                user.profile.register_ip = request.client_ip
                user.profile.save(update_fields=["register_ip"])
        except ValidationError as exc:
            return api_error(str(exc), code=400001)
        login(request, user)
        return api_success({"username": user.username})


class RegisterAvailabilityAPIView(View):
    def post(self, request: HttpRequest):
        if not settings.REGISTER_ENABLED:
            return api_error("当前站点已关闭注册", code=400403, status=403)
        try:
            payload = _parse_payload(request)
        except InvalidPayloadError as exc:
            return api_error(str(exc), code=400001)

        form = RegisterAvailabilityForm(payload, request=request)
        if not form.is_valid():
            return api_error(_first_form_error(form), code=400001)

        data: dict[str, dict[str, str | bool]] = {}
        username = form.cleaned_data.get("username", "")
        email = form.cleaned_data.get("email", "")
        if username:
            exists = User.objects.filter(username=username).exists()
            data["username"] = {
                "available": not exists,
                "message": "账号已存在" if exists else "账号可用",
            }
        if email:
            exists = User.objects.filter(email=email).exists()
            data["email"] = {
                "available": not exists,
                "message": "邮箱已存在" if exists else "邮箱可用",
            }
        return api_success(data)


class RegisterEmailCodeAPIView(View):
    def post(self, request: HttpRequest):
        if not settings.REGISTER_ENABLED:
            return api_error("当前站点已关闭注册", code=400403, status=403)

        try:
            payload = _parse_payload(request)
        except InvalidPayloadError as exc:
            return api_error(str(exc), code=400001)
        form = RegisterEmailCodeForm(payload, request=request)
        if not form.is_valid():
            return api_error(_first_form_error(form), code=400001)

        try:
            result = issue_register_email_code(
                email=form.cleaned_data["email"],
                client_ip=request.client_ip,
            )
        except ImproperlyConfigured as exc:
            return api_error(str(exc), code=500001, status=500)
        except EmailVerificationRateLimitError as exc:
            return api_error(str(exc), code=400429, status=429)
        except EmailVerificationSendError as exc:
            return api_error(str(exc), code=500002, status=502)

        return api_success(
            {
                "expire_seconds": result.expires_in,
                "resend_seconds": result.resend_in,
            },
            message="验证码已发送，请留意邮箱",
        )


class LoginCaptchaAPIView(View):
    def get(self, request: HttpRequest):
        if not settings.LOGIN_CAPTCHA_ENABLED:
            return api_success({"captcha_code": "", "captcha_svg": ""})
        code, svg = _issue_captcha(request)
        return api_success({"captcha_code": code, "captcha_svg": svg})


class LoginAPIView(View):
    def post(self, request: HttpRequest):
        try:
            payload = _parse_payload(request)
        except InvalidPayloadError as exc:
            return api_error(str(exc), code=400001)
        username = payload.get("username", "")
        try:
            ensure_login_allowed(username, request.client_ip)
        except PermissionError as exc:
            return api_error(str(exc), code=400429, status=429)
        form = LoginForm(payload, request=request)
        if not form.is_valid():
            record_login_failure(username, request.client_ip)
            return api_error(form.errors.as_json(), code=400001)
        user = form.cleaned_data["user"]
        login(request, user)
        user.last_login_at = timezone.now()
        user.last_login_ip = request.client_ip
        user.save(update_fields=["last_login_at", "last_login_ip"])
        clear_login_failures(username, request.client_ip)
        return api_success({"username": user.username})


class LogoutAPIView(View):
    def post(self, request: HttpRequest):
        logout(request)
        if request.content_type and "application/json" in request.content_type:
            return api_success({})
        return redirect("/")


class ResetPasswordAPIView(View):
    def post(self, request: HttpRequest):
        try:
            payload = _parse_payload(request)
        except InvalidPayloadError as exc:
            return api_error(str(exc), code=400001)
        form = ResetPasswordForm(payload, request=request)
        if not form.is_valid():
            return api_error(form.errors.as_json(), code=400001)
        user = form.cleaned_data["user"]
        user.set_password(form.cleaned_data["new_password"])
        user.save(update_fields=["password"])
        log_callback_event(
            event_type="password_reset",
            source="users",
            payload={"username": user.username},
            success=True,
        )
        return api_success({})
