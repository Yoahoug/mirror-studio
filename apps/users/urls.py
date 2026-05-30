from django.urls import path

from .views import (
    AccountDashboardView,
    AccountPasswordAPIView,
    AccountSettingsAPIView,
    AccountSummaryAPIView,
    ForgotPasswordPageView,
    CsrfAPIView,
    LoginPageView,
    LoginCaptchaAPIView,
    LoginAPIView,
    LogoutAPIView,
    RegisterPageView,
    RegisterAvailabilityAPIView,
    RegisterEmailCodeAPIView,
    RegisterAPIView,
    ResetPasswordAPIView,
    SecurityPageView,
)

urlpatterns = [
    path("login", LoginPageView.as_view(), name="login"),
    path("register", RegisterPageView.as_view(), name="register"),
    path("forgot-password", ForgotPasswordPageView.as_view(), name="forgot-password"),
    path("account", AccountDashboardView.as_view(), name="account"),
    path("account/security", SecurityPageView.as_view(), name="account-security"),
    path("api/account/summary", AccountSummaryAPIView.as_view(), name="api-account-summary"),
    path("api/account/settings", AccountSettingsAPIView.as_view(), name="api-account-settings"),
    path("api/account/password", AccountPasswordAPIView.as_view(), name="api-account-password"),
    path("api/auth/register/check", RegisterAvailabilityAPIView.as_view(), name="api-auth-register-check"),
    path("api/auth/register/email-code", RegisterEmailCodeAPIView.as_view(), name="api-auth-register-email-code"),
    path("api/auth/register", RegisterAPIView.as_view(), name="api-auth-register"),
    path("api/auth/csrf", CsrfAPIView.as_view(), name="api-auth-csrf"),
    path("api/auth/captcha", LoginCaptchaAPIView.as_view(), name="api-auth-captcha"),
    path("api/auth/login", LoginAPIView.as_view(), name="api-auth-login"),
    path("api/auth/logout", LogoutAPIView.as_view(), name="api-auth-logout"),
    path("api/auth/reset-password", ResetPasswordAPIView.as_view(), name="api-auth-reset-password"),
]
