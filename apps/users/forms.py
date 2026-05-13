from __future__ import annotations

from django import forms
from django.conf import settings
from django.contrib.auth import authenticate, get_user_model
from django.contrib.auth.password_validation import validate_password
from django.core.exceptions import ValidationError

from .email_verification import (
    is_email_verification_enabled,
    normalize_email,
    validate_register_email_code,
)

User = get_user_model()


class StyledFormMixin:
    def __init__(self, *args, **kwargs):
        self.request = kwargs.pop("request", None)
        super().__init__(*args, **kwargs)
        for field in self.fields.values():
            field.widget.attrs.setdefault(
                "class",
                "w-full rounded-xl border border-slate-200 px-3 py-2 text-slate-900",
            )


class CaptchaMixin(StyledFormMixin, forms.Form):
    captcha_code = forms.CharField(label="验证码", max_length=8)

    def clean_captcha_code(self):
        value = self.cleaned_data["captcha_code"].strip().upper()
        if not settings.LOGIN_CAPTCHA_ENABLED:
            return value
        expected = ""
        if self.request:
            expected = self.request.session.get("captcha_code", "")
        if not expected or value != expected:
            raise forms.ValidationError("验证码错误")
        return value


class LoginForm(CaptchaMixin, forms.Form):
    username = forms.CharField(label="账号", max_length=150)
    password = forms.CharField(label="密码", widget=forms.PasswordInput)

    def clean(self):
        cleaned = super().clean()
        user = authenticate(
            username=cleaned.get("username"),
            password=cleaned.get("password"),
        )
        if not user:
            raise ValidationError("账号或密码错误")
        if user.status != user.Status.ACTIVE:
            raise ValidationError("账号状态不可用")
        cleaned["user"] = user
        return cleaned


class RegisterEmailCodeForm(StyledFormMixin, forms.Form):
    username = forms.CharField(label="账号", max_length=150)
    email = forms.EmailField(label="邮箱")

    def clean_username(self):
        username = self.cleaned_data["username"].strip()
        if User.objects.filter(username=username).exists():
            raise forms.ValidationError("账号已存在")
        return username

    def clean_email(self):
        email = normalize_email(self.cleaned_data.get("email", ""))
        if User.objects.filter(email=email).exists():
            raise forms.ValidationError("邮箱已存在")
        return email


class RegisterAvailabilityForm(StyledFormMixin, forms.Form):
    username = forms.CharField(label="账号", max_length=150, required=False)
    email = forms.EmailField(label="邮箱", required=False)

    def clean_username(self):
        return self.cleaned_data.get("username", "").strip()

    def clean_email(self):
        return normalize_email(self.cleaned_data.get("email", ""))

    def clean(self):
        cleaned = super().clean()
        if not cleaned.get("username") and not cleaned.get("email"):
            raise ValidationError("至少提供账号或邮箱")
        return cleaned


class RegisterForm(StyledFormMixin, forms.Form):
    username = forms.CharField(label="账号", max_length=150)
    email = forms.EmailField(label="邮箱")
    email_verification_code = forms.CharField(label="邮箱验证码", max_length=8)
    password = forms.CharField(label="密码", widget=forms.PasswordInput)
    confirm_password = forms.CharField(label="确认密码", widget=forms.PasswordInput)

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.fields["email_verification_code"].required = is_email_verification_enabled()
        self.fields["email_verification_code"].widget.attrs.setdefault("inputmode", "numeric")

    def clean_username(self):
        username = self.cleaned_data["username"].strip()
        if User.objects.filter(username=username).exists():
            raise forms.ValidationError("账号已存在")
        return username

    def clean_email(self):
        email = normalize_email(self.cleaned_data.get("email", ""))
        if User.objects.filter(email=email).exists():
            raise forms.ValidationError("邮箱已存在")
        return email

    def clean(self):
        cleaned = super().clean()
        email = cleaned.get("email", "")
        email_verification_code = cleaned.get("email_verification_code", "")
        password = cleaned.get("password")
        confirm_password = cleaned.get("confirm_password")
        if email and email_verification_code:
            validate_register_email_code(email, email_verification_code)
        if password and password != confirm_password:
            raise ValidationError("两次密码输入不一致")
        if password:
            validate_password(password)
        return cleaned


class ResetPasswordForm(CaptchaMixin, forms.Form):
    username = forms.CharField(label="账号", max_length=150)
    email = forms.EmailField(label="邮箱", required=False)
    new_password = forms.CharField(label="新密码", widget=forms.PasswordInput)
    confirm_password = forms.CharField(label="确认新密码", widget=forms.PasswordInput)

    def clean(self):
        cleaned = super().clean()
        username = cleaned.get("username")
        email = normalize_email(cleaned.get("email", ""))
        password = cleaned.get("new_password")
        confirm = cleaned.get("confirm_password")
        if password and password != confirm:
            raise ValidationError("两次密码输入不一致")
        if password:
            validate_password(password)
        try:
            user = User.objects.get(username=username)
        except User.DoesNotExist as exc:
            raise ValidationError("用户不存在") from exc
        if email and user.email and user.email.lower() != email:
            raise ValidationError("邮箱不匹配")
        cleaned["user"] = user
        return cleaned
