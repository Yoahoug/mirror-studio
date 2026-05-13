from __future__ import annotations

from pathlib import Path
from tempfile import TemporaryDirectory

from django.contrib.auth import get_user_model
from django.test import TestCase, override_settings
from django.urls import reverse

from apps.billing.models import QuotaAccount
from apps.generator.models import ImageChannel
from apps.generator.services import submit_generation_task
from apps.generator.tasks import generate_image_task

User = get_user_model()


@override_settings(
    MINIO_USE_LOCAL_FS=True,
    MOCK_PROVIDER_ENABLED=False,
    PROVIDER_API_KEY="",
    CACHES={"default": {"BACKEND": "django.core.cache.backends.locmem.LocMemCache"}},
)
class ConsoleDashboardFailureTests(TestCase):
    def setUp(self):
        self.tempdir = TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.override = override_settings(MINIO_LOCAL_ROOT=Path(self.tempdir.name))
        self.override.enable()
        self.addCleanup(self.override.disable)

        self.admin = User.objects.create_user(
            username="operator",
            password="Operator123!@#",
            role=User.Role.ADMIN,
            is_staff=True,
        )
        self.user = User.objects.create_user(username="creator", password="Creator123!@#")
        self.channel = ImageChannel.objects.create(
            name="默认渠道",
            channel_code="default",
            base_url="https://api.openai.com/v1",
            api_path="/images/generations",
            model_name="gpt-image-1",
            is_active=True,
        )
        account = QuotaAccount.objects.get(user=self.user)
        account.available_quota = 1
        account.save(update_fields=["available_quota"])
        self.client.force_login(self.admin)

    def test_dashboard_recent_failures_reads_failed_tasks_with_raw_detail(self):
        task, _ = submit_generation_task(
            user=self.user,
            prompt="后台需要看到失败日志",
            style_code="general",
            size_code="1024x1024",
            client_ip="127.0.0.1",
        )

        with self.assertRaises(Exception):
            generate_image_task(task.task_no)

        response = self.client.get(reverse("console-api-dashboard"), secure=True)
        self.assertEqual(response.status_code, 200)
        data = response.json()["data"]
        self.assertEqual(data["today"]["failed_tasks"], 1)
        self.assertEqual(data["latest_tasks"][0]["task_no"], task.task_no)
        self.assertEqual(data["latest_tasks"][0]["error_message"], "缺少 OpenAI 兼容渠道 API Key")
        self.assertEqual(data["recent_failures"][0]["task_no"], task.task_no)
        self.assertEqual(data["recent_failures"][0]["error_message"], "缺少 OpenAI 兼容渠道 API Key")
        self.assertEqual(data["recent_failures"][0]["payload_json"]["exception_type"], "ValueError")

    def test_disabled_operator_cannot_access_console_page(self):
        self.admin.status = User.Status.DISABLED
        self.admin.save(update_fields=["status"])

        response = self.client.get(reverse("console"), secure=True)

        self.assertEqual(response.status_code, 403)

    def test_disabled_operator_cannot_access_console_api(self):
        self.admin.status = User.Status.DISABLED
        self.admin.save(update_fields=["status"])

        response = self.client.get(reverse("console-api-dashboard"), secure=True)

        self.assertEqual(response.status_code, 403)
        self.assertEqual(response.json()["code"], 403001)
