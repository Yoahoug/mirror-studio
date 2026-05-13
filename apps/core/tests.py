from __future__ import annotations

import hashlib
import hmac
import json
import time
from unittest.mock import Mock, patch

from django.contrib.auth import get_user_model
from django.test import TestCase, override_settings
from django.urls import reverse

from apps.core.siteconfig import get_config
from apps.generator.models import ImageChannel

User = get_user_model()


@override_settings(CACHES={"default": {"BACKEND": "django.core.cache.backends.locmem.LocMemCache"}})
class ChatAPIViewTests(TestCase):
    def setUp(self):
        self.user = User.objects.create_user(username="chat_user", password="Chat123!@#")
        self.client.login(username="chat_user", password="Chat123!@#")

    def _configure_chat(self):
        channel = ImageChannel.objects.create(
            name="测试对话模型",
            channel_code="chat_test",
            channel_type=ImageChannel.ChannelType.LLM,
            provider_name="测试对话方",
            base_url="https://llm.example.com/v1",
            api_path="/chat/completions",
            model_name="chat-model",
            is_active=True,
        )
        channel.api_key = "chat-secret"
        channel.save(update_fields=["api_key_encrypted", "updated_at"])
        return channel

    @patch("apps.core.views.requests.post")
    def test_chat_uses_selected_llm_channel(self, mock_post):
        channel = self._configure_chat()
        mock_response = Mock()
        mock_response.json.return_value = {"choices": [{"message": {"content": "配置生效"}}]}
        mock_response.raise_for_status.return_value = None
        mock_post.return_value = mock_response

        resp = self.client.post(
            reverse("api-chat"),
            data=json.dumps({
                "prompt": "你好",
                "channel_code": channel.channel_code,
                "history": [{"role": "model", "parts": [{"text": "历史回复"}]}],
            }),
            content_type="application/json",
            secure=True,
        )

        self.assertEqual(resp.status_code, 200)
        self.assertEqual(resp.json()["data"]["reply"], "配置生效")
        url = mock_post.call_args.args[0]
        headers = mock_post.call_args.kwargs["headers"]
        body = mock_post.call_args.kwargs["json"]
        self.assertEqual(url, "https://llm.example.com/v1/chat/completions")
        self.assertEqual(headers["Authorization"], "Bearer chat-secret")
        self.assertEqual(body["model"], "chat-model")
        self.assertEqual(body["messages"][1]["role"], "assistant")

    def test_chat_requires_available_llm_model(self):
        resp = self.client.post(
            reverse("api-chat"),
            data=json.dumps({"prompt": "你好"}),
            content_type="application/json",
            secure=True,
        )
        self.assertEqual(resp.status_code, 503)
        self.assertEqual(resp.json()["code"], 400503)

    def test_chat_models_only_returns_llm_channels(self):
        llm = self._configure_chat()
        ImageChannel.objects.create(
            name="云梦生图",
            channel_code="image_default",
            channel_type=ImageChannel.ChannelType.IMAGE,
            provider_name="云梦",
            base_url="https://image.example.com",
            api_path="/draw",
            model_name="image-model",
            is_active=True,
        )

        resp = self.client.get(reverse("api-chat-models"), secure=True)
        data = resp.json()["data"]
        self.assertEqual([item["channel_code"] for item in data], [llm.channel_code])


@override_settings(
    RELEASE_PUSH_KEY="push-key",
    RELEASE_PUSH_SECRET="push-secret",
    RELEASE_PUSH_MAX_AGE_SECONDS=300,
    CACHES={"default": {"BACKEND": "django.core.cache.backends.locmem.LocMemCache"}},
)
class InternalReleasePublishAPITests(TestCase):
    def _signed_headers(self, payload: dict, timestamp: int | None = None) -> tuple[str, dict]:
        body = json.dumps(payload, ensure_ascii=False)
        ts = str(timestamp or int(time.time()))
        signature = hmac.new(
            b"push-secret",
            ts.encode("utf-8") + b"." + body.encode("utf-8"),
            hashlib.sha256,
        ).hexdigest()
        return body, {
            "HTTP_X_RELEASE_KEY": "push-key",
            "HTTP_X_RELEASE_TIMESTAMP": ts,
            "HTTP_X_RELEASE_SIGNATURE": signature,
        }

    def test_publish_release_updates_site_config(self):
        body, headers = self._signed_headers(
            {
                "version": "v1.2.0",
                "title": "四月版本更新",
                "notes": ["新增更新日志弹窗", "新增图生图模式", "修复预览溢出"],
            }
        )

        resp = self.client.post(
            reverse("api-internal-release-publish"),
            data=body,
            content_type="application/json",
            secure=True,
            **headers,
        )

        self.assertEqual(resp.status_code, 200)
        self.assertEqual(get_config("site.workspace_release_version"), "v1.2.0")
        self.assertEqual(get_config("site.workspace_release_title"), "四月版本更新")
        self.assertIn("新增图生图模式", get_config("site.workspace_release_notes", ""))

    def test_publish_release_rejects_invalid_signature(self):
        resp = self.client.post(
            reverse("api-internal-release-publish"),
            data=json.dumps({"version": "v1.2.0", "notes": ["test"]}),
            content_type="application/json",
            secure=True,
            HTTP_X_RELEASE_KEY="push-key",
            HTTP_X_RELEASE_TIMESTAMP=str(int(time.time())),
            HTTP_X_RELEASE_SIGNATURE="bad-signature",
        )

        self.assertEqual(resp.status_code, 401)
        self.assertEqual(resp.json()["code"], 401005)
