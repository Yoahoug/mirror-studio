import json
import io
import base64
import importlib
from datetime import timedelta
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import patch

from django.test.utils import CaptureQueriesContext
from django.contrib.auth import get_user_model
from django.core.cache import cache
from django.apps import apps
from django.db import connection
from django.test import TestCase, override_settings
from django.urls import reverse
from django.utils import timezone

from apps.audit.models import CallbackEventLog
from apps.billing.models import QuotaAccount
from apps.novelviz.models import NovelVisualizationTask
from PIL import Image

from .models import GenerateTask, ImageChannel
from .services import reconcile_task_runtime_state, submit_generation_task
from .tasks import dispatch_generate_image_task, generate_image_task, run_generation_worker_once

User = get_user_model()


@override_settings(
    MINIO_USE_LOCAL_FS=True,
    CELERY_TASK_ALWAYS_EAGER=True,
    MOCK_PROVIDER_ENABLED=True,
    CACHES={"default": {"BACKEND": "django.core.cache.backends.locmem.LocMemCache"}},
)
class GeneratorFlowTests(TestCase):
    @staticmethod
    def make_png_bytes() -> bytes:
        image = Image.new("RGB", (64, 64), color=(47, 107, 255))
        buffer = io.BytesIO()
        image.save(buffer, format="PNG")
        return buffer.getvalue()

    @classmethod
    def make_png_data_url(cls) -> str:
        return "data:image/png;base64," + base64.b64encode(cls.make_png_bytes()).decode("ascii")

    def setUp(self):
        self.tempdir = TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.override = override_settings(MINIO_LOCAL_ROOT=Path(self.tempdir.name))
        self.override.enable()
        self.addCleanup(self.override.disable)
        cache.clear()
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

    def test_generate_success_consumes_frozen_quota_and_persists_image(self):
        task, _ = submit_generation_task(
            user=self.user,
            prompt="一只橘猫在咖啡馆看报纸",
            style_code="cinematic",
            size_code="1024x1024",
            client_ip="127.0.0.1",
        )

        generate_image_task(task.task_no)
        task.refresh_from_db()
        account = QuotaAccount.objects.get(user=self.user)

        self.assertEqual(task.status, GenerateTask.Status.SUCCEEDED)
        self.assertEqual(task.progress_percent, 100)
        self.assertEqual(task.images.count(), 1)
        self.assertEqual(account.available_quota, 0)
        self.assertEqual(account.frozen_quota, 0)
        self.assertEqual(account.total_consumed_quota, 1)

    def test_duplicate_client_request_id_returns_existing_task_without_extra_quota(self):
        task, created = submit_generation_task(
            user=self.user,
            prompt="同一个请求只创建一次",
            client_request_id="req-001",
            client_ip="127.0.0.1",
        )
        same_task, same_created = submit_generation_task(
            user=self.user,
            prompt="同一个请求只创建一次",
            client_request_id="req-001",
            client_ip="127.0.0.1",
        )
        account = QuotaAccount.objects.get(user=self.user)

        self.assertTrue(created)
        self.assertFalse(same_created)
        self.assertEqual(task.pk, same_task.pk)
        self.assertEqual(GenerateTask.objects.filter(user=self.user).count(), 1)
        self.assertEqual(account.available_quota, 0)
        self.assertEqual(account.frozen_quota, 1)

    def test_task_create_wait_returns_full_progress_payload(self):
        self.client.force_login(self.user)

        resp = self.client.post(
            reverse("api-generate-tasks"),
            data=json.dumps(
                {
                    "prompt": "一只橘猫在窗边晒太阳",
                    "style_code": "photography",
                    "size_code": "1536x864",
                    "quality_code": "low",
                    "wait_seconds": 2,
                }
            ),
            content_type="application/json",
            REMOTE_ADDR="127.0.0.1",
            secure=True,
        )

        self.assertEqual(resp.status_code, 200)
        data = resp.json()["data"]
        self.assertEqual(data["status"], GenerateTask.Status.SUCCEEDED)
        self.assertEqual(data["progress_percent"], 100)
        self.assertEqual(data["size_code"], "1536x864")
        self.assertEqual(data["quality_code"], "low")
        self.assertEqual(len(data["images"]), 1)
        self.assertIn("preview_url", data["images"][0])
        self.assertIn("download_url", data["images"][0])

    def test_submit_generation_task_accepts_2k_custom_size_and_quality(self):
        task, _ = submit_generation_task(
            user=self.user,
            prompt="2K 自定义比例海报",
            size_code="2048x1152",
            quality_code="high",
            client_ip="127.0.0.1",
        )

        self.assertEqual(task.size_code, "2048x1152")
        self.assertEqual(task.quality_code, "high")

    def test_submit_generation_task_rejects_invalid_size_and_quality(self):
        cases = [
            {"size_code": "1537x864", "message": "尺寸宽高必须是 16px 的倍数"},
            {"size_code": "2048x512", "message": "尺寸长短边比例不能超过 3:1"},
            {"size_code": "640x640", "message": "尺寸总像素过小，请提高分辨率"},
            {"size_code": "2160x1216", "message": "尺寸最大边不能超过 2048px"},
            {"quality_code": "ultra", "message": "不支持的质量"},
        ]

        for case in cases:
            with self.subTest(case=case):
                with self.assertRaisesMessage(ValueError, case["message"]):
                    submit_generation_task(
                        user=self.user,
                        prompt="非法参数",
                        size_code=case.get("size_code", "1024x1024"),
                        quality_code=case.get("quality_code", "auto"),
                        client_ip="127.0.0.1",
                    )

    @override_settings(CELERY_TASK_ALWAYS_EAGER=False)
    def test_dispatch_task_keeps_task_queued_for_worker_pull(self):
        task, _ = submit_generation_task(
            user=self.user,
            prompt="等待 worker 拉取",
            client_ip="127.0.0.1",
        )

        dispatch_generate_image_task(task.task_no)

        task.refresh_from_db()
        self.assertEqual(task.status, GenerateTask.Status.QUEUED)
        self.assertEqual(task.execution_attempts, 0)

    def test_run_generation_worker_once_pulls_queued_task(self):
        task, _ = submit_generation_task(
            user=self.user,
            prompt="worker 主动拉取任务",
            client_ip="127.0.0.1",
        )

        processed_task_no = run_generation_worker_once()

        task.refresh_from_db()
        self.assertEqual(processed_task_no, task.task_no)
        self.assertEqual(task.status, GenerateTask.Status.SUCCEEDED)
        self.assertEqual(task.execution_attempts, 1)

    def test_image_to_image_task_requires_reference_and_returns_reference_payload(self):
        self.client.force_login(self.user)

        resp = self.client.post(
            reverse("api-generate-tasks"),
            data=json.dumps(
                {
                    "prompt": "参考原图重绘成电商海报",
                    "generation_mode": "image_to_image",
                    "reference_image_name": "fruit.png",
                    "reference_image_data_url": self.make_png_data_url(),
                    "wait_seconds": 2,
                }
            ),
            content_type="application/json",
            REMOTE_ADDR="127.0.0.1",
            secure=True,
        )

        self.assertEqual(resp.status_code, 200)
        data = resp.json()["data"]
        self.assertEqual(data["generation_mode"], "image_to_image")
        self.assertEqual(data["reference_image_name"], "fruit.png")
        self.assertTrue(data["reference_image_attached"])
        self.assertEqual(data["reference_image_data_url"], "")

    def test_reference_upload_endpoint_returns_token(self):
        self.client.force_login(self.user)

        resp = self.client.post(
            reverse("api-generate-reference-images"),
            data=json.dumps(
                {
                    "reference_image_name": "fruit.png",
                    "reference_image_data_url": self.make_png_data_url(),
                }
            ),
            content_type="application/json",
            REMOTE_ADDR="127.0.0.1",
            secure=True,
        )

        self.assertEqual(resp.status_code, 200)
        data = resp.json()["data"]
        self.assertTrue(data["reference_upload_token"])
        self.assertEqual(data["reference_image_name"], "fruit.png")
        self.assertTrue(data["reference_image_attached"])

    def test_image_to_image_task_accepts_reference_upload_token(self):
        self.client.force_login(self.user)

        upload_resp = self.client.post(
            reverse("api-generate-reference-images"),
            data=json.dumps(
                {
                    "reference_image_name": "fruit.png",
                    "reference_image_data_url": self.make_png_data_url(),
                }
            ),
            content_type="application/json",
            REMOTE_ADDR="127.0.0.1",
            secure=True,
        )
        upload_data = upload_resp.json()["data"]

        resp = self.client.post(
            reverse("api-generate-tasks"),
            data=json.dumps(
                {
                    "prompt": "参考原图重绘成电商海报",
                    "generation_mode": "image_to_image",
                    "reference_image_name": upload_data["reference_image_name"],
                    "reference_upload_token": upload_data["reference_upload_token"],
                    "wait_seconds": 2,
                }
            ),
            content_type="application/json",
            REMOTE_ADDR="127.0.0.1",
            secure=True,
        )

        self.assertEqual(resp.status_code, 200)
        data = resp.json()["data"]
        self.assertEqual(data["generation_mode"], "image_to_image")
        self.assertEqual(data["reference_image_name"], "fruit.png")
        self.assertTrue(data["reference_image_attached"])

    def test_task_list_is_paginated_and_exposes_preview_urls(self):
        self.client.force_login(self.user)
        task, _ = submit_generation_task(
            user=self.user,
            prompt="分页历史任务",
            client_ip="127.0.0.1",
        )
        generate_image_task(task.task_no)

        resp = self.client.get(reverse("api-generate-tasks") + "?page=1&page_size=10", secure=True)

        self.assertEqual(resp.status_code, 200)
        data = resp.json()["data"]
        self.assertIn("items", data)
        self.assertIn("pagination", data)
        self.assertEqual(data["items"][0]["task_no"], task.task_no)
        self.assertIn("preview_url", data["items"][0]["images"][0])

    def test_workspace_history_defers_novel_task_logs(self):
        self.client.force_login(self.user)
        task, _ = submit_generation_task(
            user=self.user,
            prompt="统一历史里的普通生图",
            client_ip="127.0.0.1",
        )
        novel_task = NovelVisualizationTask.objects.create(
            user=self.user,
            book_filename="story.epub",
            status=NovelVisualizationTask.Status.FAILED,
            progress_percent=100,
            logs=[{"id": 1, "type": "task_failed", "payload": {"message": "large log"}, "created_at": "2026-01-01T00:00:00+00:00"}],
        )
        with CaptureQueriesContext(connection) as queries:
            response = self.client.get(reverse("api-workspace-history") + "?page=1&page_size=10", secure=True)

        self.assertEqual(response.status_code, 200)
        task_numbers = [item["task_no"] for item in response.json()["data"]["items"]]
        self.assertIn(task.task_no, task_numbers)
        self.assertIn(f"novelviz:{novel_task.id}", task_numbers)
        novel_task_selects = [
            query["sql"]
            for query in queries
            if 'FROM "novelviz_novelvisualizationtask"' in query["sql"]
            and "SELECT" in query["sql"].upper()
        ]
        self.assertTrue(novel_task_selects)
        self.assertFalse(any('"novelviz_novelvisualizationtask"."logs"' in sql for sql in novel_task_selects))

    def test_delete_history_hides_task_and_marks_image_deleted(self):
        self.client.force_login(self.user)
        task, _ = submit_generation_task(
            user=self.user,
            prompt="需要删除的历史",
            client_ip="127.0.0.1",
        )
        generate_image_task(task.task_no)

        resp = self.client.delete(reverse("api-generate-task-detail", args=[task.task_no]), secure=True)

        self.assertEqual(resp.status_code, 200)
        task.refresh_from_db()
        self.assertTrue(task.is_deleted)
        self.assertEqual(task.images.first().status, "deleted")

        list_resp = self.client.get(reverse("api-generate-tasks"), secure=True)
        self.assertEqual(list_resp.json()["data"]["items"], [])

    def test_delete_running_task_cancels_and_hides_task(self):
        self.client.force_login(self.user)
        task, _ = submit_generation_task(
            user=self.user,
            prompt="删除运行中任务",
            client_ip="127.0.0.1",
        )
        task.status = GenerateTask.Status.RUNNING
        task.started_at = timezone.now()
        task.save(update_fields=["status", "started_at", "updated_at"])

        resp = self.client.delete(reverse("api-generate-task-detail", args=[task.task_no]), secure=True)

        self.assertEqual(resp.status_code, 200)
        task.refresh_from_db()
        account = QuotaAccount.objects.get(user=self.user)
        self.assertEqual(task.status, GenerateTask.Status.CANCELED)
        self.assertTrue(task.is_deleted)
        self.assertEqual(account.available_quota, 1)
        self.assertEqual(account.frozen_quota, 0)

    def test_task_detail_auto_marks_stalled_running_task_failed(self):
        self.client.force_login(self.user)
        task, _ = submit_generation_task(
            user=self.user,
            prompt="卡住的运行任务",
            client_ip="127.0.0.1",
        )
        task.status = GenerateTask.Status.RUNNING
        task.started_at = timezone.now() - timedelta(minutes=10)
        task.save(update_fields=["status", "started_at", "updated_at"])

        resp = self.client.get(reverse("api-generate-task-detail", args=[task.task_no]), secure=True)

        self.assertEqual(resp.status_code, 200)
        self.assertEqual(resp.json()["data"]["status"], GenerateTask.Status.FAILED)
        task.refresh_from_db()
        self.assertEqual(task.error_code, "task_stalled")

    def test_reconcile_requeues_running_task_with_expired_lease(self):
        task, _ = submit_generation_task(
            user=self.user,
            prompt="租约过期后重新排队",
            client_ip="127.0.0.1",
        )
        task.status = GenerateTask.Status.RUNNING
        task.started_at = timezone.now() - timedelta(seconds=30)
        task.lease_expires_at = timezone.now() - timedelta(seconds=1)
        task.execution_attempts = 1
        task.save(update_fields=["status", "started_at", "lease_expires_at", "execution_attempts", "updated_at"])

        task = reconcile_task_runtime_state(task)

        self.assertEqual(task.status, GenerateTask.Status.QUEUED)
        self.assertIsNone(task.lease_expires_at)
        self.assertEqual(task.execution_attempts, 1)

    @override_settings(MOCK_PROVIDER_ENABLED=False, PROVIDER_API_KEY="")
    def test_generate_failure_releases_frozen_quota(self):
        self.channel.api_key_encrypted = ""
        self.channel.save(update_fields=["api_key_encrypted"])
        task, _ = submit_generation_task(
            user=self.user,
            prompt="失败任务",
            style_code="general",
            size_code="1024x1024",
            client_ip="127.0.0.1",
        )

        with self.assertRaises(Exception):
            generate_image_task(task.task_no)

        task.refresh_from_db()
        account = QuotaAccount.objects.get(user=self.user)
        self.assertEqual(task.status, GenerateTask.Status.FAILED)
        self.assertEqual(task.progress_percent, 100)
        self.assertEqual(account.available_quota, 1)
        self.assertEqual(account.frozen_quota, 0)
        self.assertEqual(task.error_message, "缺少 OpenAI 兼容渠道 API Key")
        self.assertEqual(task.provider_response_json["failure"]["exception_type"], "ValueError")
        self.assertEqual(task.provider_response_json["failure"]["attempts_used"], 1)

        callback_log = CallbackEventLog.objects.filter(event_type="generation_failed", success=False).latest("id")
        self.assertEqual(callback_log.payload_json["task_no"], task.task_no)
        self.assertEqual(callback_log.payload_json["error_message"], "缺少 OpenAI 兼容渠道 API Key")

    @override_settings(MOCK_PROVIDER_ENABLED=False, PROVIDER_API_KEY="")
    def test_failed_task_api_only_exposes_generic_error_to_workspace_user(self):
        self.channel.api_key_encrypted = ""
        self.channel.save(update_fields=["api_key_encrypted"])
        self.client.force_login(self.user)
        task, _ = submit_generation_task(
            user=self.user,
            prompt="失败任务对用户隐藏真实错误",
            style_code="general",
            size_code="1024x1024",
            client_ip="127.0.0.1",
        )

        with self.assertRaises(Exception):
            generate_image_task(task.task_no)

        detail_resp = self.client.get(reverse("api-generate-task-detail", args=[task.task_no]), secure=True)
        list_resp = self.client.get(reverse("api-generate-tasks"), secure=True)
        detail_data = detail_resp.json()["data"]
        list_item = list_resp.json()["data"]["items"][0]

        self.assertEqual(detail_data["status"], GenerateTask.Status.FAILED)
        self.assertEqual(detail_data["error_message"], "生成失败，请稍后重试。若多次失败请联系管理员排查上游渠道。")
        self.assertEqual(list_item["error_message"], "生成失败，请稍后重试。若多次失败请联系管理员排查上游渠道。")
        task.refresh_from_db()
        self.assertEqual(task.error_message, "缺少 OpenAI 兼容渠道 API Key")

    @override_settings(MOCK_PROVIDER_ENABLED=False)
    def test_retry_happens_inside_same_task_record(self):
        self.channel.api_key = "token"
        self.channel.max_retries = 3
        self.channel.save(update_fields=["api_key_encrypted", "max_retries", "updated_at"])
        task, _ = submit_generation_task(
            user=self.user,
            prompt="同一任务内部自动重试",
            style_code="general",
            size_code="1024x1024",
            client_ip="127.0.0.1",
        )

        class DummyResponse:
            def __init__(self, payload: dict, status_code: int):
                self.payload = payload
                self.status_code = status_code
                self.headers = {}

            def json(self):
                return self.payload

        with patch(
            "apps.generator.providers.requests.post",
            side_effect=[
                DummyResponse({"error": {"message": "temporary 500"}}, 500),
                DummyResponse({"error": {"message": "temporary 502"}}, 502),
                DummyResponse({"created": "ok", "data": [{"b64_json": base64.b64encode(self.make_png_bytes()).decode("ascii")}]}, 200),
            ],
        ):
            generate_image_task(task.task_no)

        task.refresh_from_db()
        self.assertEqual(task.status, GenerateTask.Status.SUCCEEDED)
        self.assertEqual(task.images.count(), 1)
        self.assertEqual(task.provider_response_json["_meta"]["attempts_used"], 3)

    @override_settings(MOCK_PROVIDER_ENABLED=False)
    def test_openai_compatible_channel_can_decode_b64_and_store_image(self):
        self.channel.name = "OpenAI Compatible"
        self.channel.provider_name = "OpenAI-Compatible"
        self.channel.base_url = "http://10.0.0.1:8080"
        self.channel.api_path = "/v1/images/generations"
        self.channel.model_name = "gpt-image-2"
        self.channel.api_key = "compat-key"
        self.channel.save(
            update_fields=[
                "name",
                "provider_name",
                "base_url",
                "api_path",
                "model_name",
                "api_key_encrypted",
                "updated_at",
            ]
        )

        task, _ = submit_generation_task(
            user=self.user,
            prompt="一只蓝色小鸟站在茶杯上",
            style_code="general",
            size_code="2048x1152",
            quality_code="medium",
            client_ip="127.0.0.1",
        )

        png_b64 = "iVBORw0KGgoAAAANSUhEUgAAAEAAAABACAIAAAAlC+aJAAAAY0lEQVR4nO3PQQ0AIBDAsAP/nuGNAvZoFSzZOjNnyNi/A3gV0AqoFVAroFZAraBaAbUCagXUCqgVUCugVkCtgFoBtQJqBdQKqBVQK6BWQK2AWgG1AmoF1AqoFVAroFbwA5wBgrQMzDIAAAAASUVORK5CYII="

        class DummyResponse:
            status_code = 200
            headers = {"x-request-id": "req-openai-1"}

            def json(self):
                return {
                    "created": 1777220495,
                    "data": [{"b64_json": png_b64, "revised_prompt": "一只蓝色小鸟站在茶杯上"}],
                    "model": "gpt-image-2",
                    "size": "1024x1024",
                }

        with patch("apps.generator.providers.requests.post", return_value=DummyResponse()) as mock_post:
            generate_image_task(task.task_no)

        task.refresh_from_db()
        account = QuotaAccount.objects.get(user=self.user)
        request_payload = mock_post.call_args.kwargs["json"]

        self.assertEqual(task.status, GenerateTask.Status.SUCCEEDED)
        self.assertEqual(task.provider_request_id, "req-openai-1")
        self.assertEqual(request_payload["size"], "2048x1152")
        self.assertEqual(request_payload["quality"], "medium")
        self.assertEqual(task.images.count(), 1)
        self.assertEqual(task.provider_response_json["data"][0]["b64_json"].startswith("<omitted:"), True)
        self.assertEqual(account.available_quota, 0)
        self.assertEqual(account.frozen_quota, 0)

    @override_settings(MOCK_PROVIDER_ENABLED=False)
    def test_openai_compatible_channel_sends_reference_image_for_image_to_image(self):
        self.channel.name = "OpenAI Compatible"
        self.channel.provider_name = "OpenAI-Compatible"
        self.channel.base_url = "http://10.0.0.1:8080"
        self.channel.api_path = "/v1/images/generations"
        self.channel.model_name = "gpt-image-2"
        self.channel.api_key = "compat-key"
        self.channel.save(
            update_fields=[
                "name",
                "provider_name",
                "base_url",
                "api_path",
                "model_name",
                "api_key_encrypted",
                "updated_at",
            ]
        )

        task, _ = submit_generation_task(
            user=self.user,
            prompt="把原图重绘成海报",
            generation_mode="image_to_image",
            size_code="1536x864",
            quality_code="high",
            reference_image_name="ref.png",
            reference_image_data_url=self.make_png_data_url(),
            client_ip="127.0.0.1",
        )

        class DummyResponse:
            status_code = 200
            headers = {"x-request-id": "req-openai-2"}

            def json(self):
                return {
                    "created": 1777220495,
                    "data": [{"b64_json": "iVBORw0KGgoAAAANSUhEUgAAAEAAAABACAIAAAAlC+aJAAAAY0lEQVR4nO3PQQ0AIBDAsAP/nuGNAvZoFSzZOjNnyNi/A3gV0AqoFVAroFZAraBaAbUCagXUCqgVUCugVkCtgFoBtQJqBdQKqBVQK6BWQK2AWgG1AmoF1AqoFVAroFbwA5wBgrQMzDIAAAAASUVORK5CYII="}],
                }

        with patch("apps.generator.providers.requests.post", return_value=DummyResponse()) as mock_post:
            generate_image_task(task.task_no)

        called_url = mock_post.call_args.args[0]
        self.assertTrue(called_url.endswith("/v1/images/edits"))
        request_data = mock_post.call_args.kwargs["data"]
        request_files = mock_post.call_args.kwargs["files"]
        self.assertEqual(request_data["model"], "gpt-image-2")
        self.assertEqual(request_data["prompt"], "把原图重绘成海报")
        self.assertEqual(request_data["size"], "1536x864")
        self.assertEqual(request_data["quality"], "high")
        self.assertIn("image", request_files)
        uploaded_name, uploaded_bytes, uploaded_mime = request_files["image"]
        self.assertTrue(uploaded_name.startswith("ref"))
        self.assertGreater(len(uploaded_bytes), 0)
        self.assertIn(uploaded_mime, {"image/png", "image/jpeg"})


class GeneratorMigrationTests(TestCase):
    def test_migration_disables_legacy_yunmeng_channels(self):
        migration_module = importlib.import_module("apps.generator.migrations.0012_task_quality_and_disable_yunmeng")

        ImageChannel.objects.create(
            name="旧云梦",
            channel_code="legacy-yunmeng",
            channel_type="image",
            provider_name="云梦",
            base_url="https://ymapi.wancores.com",
            api_path="/api/public/image/draw/submit",
            model_name="gpt-image-2",
            is_active=True,
        )
        ImageChannel.objects.create(
            name="OpenAI 中转",
            channel_code="openai-compatible",
            channel_type="image",
            provider_name="OpenAI-Compatible",
            base_url="https://image-gateway.example.test",
            api_path="/v1/images/generations",
            model_name="gpt-image-2",
            is_active=True,
        )

        migration_module.disable_legacy_yunmeng_channels(apps, None)

        self.assertFalse(ImageChannel.objects.get(channel_code="legacy-yunmeng").is_active)
        self.assertTrue(ImageChannel.objects.get(channel_code="openai-compatible").is_active)
