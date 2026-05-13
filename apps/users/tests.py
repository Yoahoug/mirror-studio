import json
from unittest.mock import patch

from django.contrib.auth import get_user_model
from django.core.cache import cache
from django.core.exceptions import ValidationError
from django.test import TestCase, override_settings

from .email_verification import validate_register_email_code

User = get_user_model()


@override_settings(
    CACHE_BACKEND="django.core.cache.backends.locmem.LocMemCache",
    CACHE_LOCATION="users-test-cache",
    EMAIL_VERIFICATION_ENABLED=True,
    EMAIL_VERIFICATION_CODE_LENGTH=6,
    EMAIL_VERIFICATION_TTL_SECONDS=600,
    EMAIL_VERIFICATION_RESEND_SECONDS=60,
    EMAIL_VERIFICATION_DAILY_LIMIT_PER_EMAIL=10,
    EMAIL_VERIFICATION_DAILY_LIMIT_PER_IP=10,
    TENCENTCLOUD_SECRET_ID="test-secret-id",
    TENCENTCLOUD_SECRET_KEY="test-secret-key",
    TENCENTCLOUD_SES_REGION="ap-hongkong",
    TENCENTCLOUD_SES_FROM_EMAIL="no-reply@notify.example.com",
    TENCENTCLOUD_SES_FROM_ALIAS="绘镜 Studio",
    TENCENTCLOUD_SES_REPLY_TO="hello@example.com",
    TENCENTCLOUD_SES_VERIFICATION_TEMPLATE_ID=1001,
    TENCENTCLOUD_SES_VERIFICATION_SUBJECT="绘镜 Studio 邮箱验证码",
)
class RegisterEmailVerificationTests(TestCase):
    def setUp(self):
        cache.clear()
        User.objects.create_user(username="existing-user", email="existing@example.com", password="StrongPass123!@#")

    def _send_code(self, email: str, username: str = "new-user"):
        with patch(
            "apps.users.email_verification.generate_email_verification_code",
            return_value="123456",
        ), patch(
            "apps.users.email_verification.TencentSesEmailSender.send_register_verification_email",
            return_value="ses-message-id",
        ):
            return self.client.post(
                "/api/auth/register/email-code",
                data=json.dumps({"email": email, "username": username}),
                content_type="application/json",
                secure=True,
            )

    def test_send_register_email_code(self):
        response = self._send_code("new@example.com")

        self.assertEqual(response.status_code, 200)
        body = response.json()
        self.assertEqual(body["code"], 0)
        self.assertEqual(body["data"]["expire_seconds"], 600)
        self.assertEqual(body["data"]["resend_seconds"], 60)
        validate_register_email_code("new@example.com", "123456")

    def test_send_register_email_code_has_cooldown(self):
        first = self._send_code("new@example.com")
        second = self._send_code("new@example.com")

        self.assertEqual(first.status_code, 200)
        self.assertEqual(second.status_code, 429)
        self.assertIn("发送过于频繁", second.json()["message"])

    def test_send_register_email_code_rejects_duplicate_username(self):
        response = self._send_code("unique@example.com", username="existing-user")

        self.assertEqual(response.status_code, 400)
        self.assertIn("账号已存在", response.json()["message"])

    def test_register_check_can_report_duplicate_username(self):
        response = self.client.post(
            "/api/auth/register/check",
            data=json.dumps({"username": "existing-user"}),
            content_type="application/json",
            secure=True,
        )

        self.assertEqual(response.status_code, 200)
        body = response.json()
        self.assertEqual(body["code"], 0)
        self.assertFalse(body["data"]["username"]["available"])
        self.assertEqual(body["data"]["username"]["message"], "账号已存在")

    def test_register_page_requires_valid_email_code_and_consumes_it(self):
        self._send_code("new@example.com")

        response = self.client.post(
            "/register",
            {
                "username": "new-user",
                "email": "new@example.com",
                "email_verification_code": "123456",
                "password": "StrongPass123!@#",
                "confirm_password": "StrongPass123!@#",
            },
            secure=True,
        )

        self.assertEqual(response.status_code, 302)
        self.assertEqual(response["Location"], "/workspace")
        self.assertTrue(User.objects.filter(username="new-user", email="new@example.com").exists())

        with self.assertRaises(ValidationError):
            validate_register_email_code("new@example.com", "123456")


@override_settings(
    CACHES={"default": {"BACKEND": "django.core.cache.backends.locmem.LocMemCache"}},
    LOGIN_CAPTCHA_ENABLED=True,
)
class LoginSecurityTests(TestCase):
    def setUp(self):
        cache.clear()
        self.user = User.objects.create_user(username="login-user", password="StrongPass123!@#")

    def test_login_rejects_external_next_url(self):
        self.client.get("/login?next=https://evil.example/path", secure=True)
        captcha_code = self.client.session["captcha_code"]

        response = self.client.post(
            "/login?next=https://evil.example/path",
            {
                "username": self.user.username,
                "password": "StrongPass123!@#",
                "captcha_code": captcha_code,
            },
            secure=True,
        )

        self.assertEqual(response.status_code, 302)
        self.assertEqual(response["Location"], "/workspace")

    def test_login_api_rejects_invalid_json(self):
        response = self.client.post(
            "/api/auth/login",
            data="{bad-json",
            content_type="application/json",
            secure=True,
        )

        self.assertEqual(response.status_code, 400)
        self.assertIn("JSON", response.json()["message"])
