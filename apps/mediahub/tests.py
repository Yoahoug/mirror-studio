import io
import json
import os
import time
from datetime import timedelta
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import patch

from django.contrib.auth import get_user_model
from django.conf import settings
from django.test import TestCase, override_settings
from django.utils import timezone
from PIL import Image

from apps.generator.models import GenerateTask, GenerationTopic, ImageChannel
from apps.generator.services import submit_generation_task
from apps.novelviz.models import NovelSceneImage, NovelUploadedBook, NovelVisualizationTask
from apps.novelviz.services import reset_task_for_retry

from .models import GeneratedImage, UploadSession
from .services import expire_temporary_reference_images, expire_upload_sessions
from .tasks import expire_temporary_reference_images_task, expire_upload_sessions_task

User = get_user_model()


@override_settings(
    MINIO_USE_LOCAL_FS=True,
    CACHES={"default": {"BACKEND": "django.core.cache.backends.locmem.LocMemCache"}},
)
class MediaIsolationTests(TestCase):
    def setUp(self):
        self.tempdir = TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.override = override_settings(MINIO_LOCAL_ROOT=Path(self.tempdir.name))
        self.override.enable()
        self.addCleanup(self.override.disable)

        self.owner = User.objects.create_user(username="owner", email="owner@example.com", password="Owner123!@#")
        self.other = User.objects.create_user(username="other", email="other@example.com", password="Other123!@#")
        channel = ImageChannel.objects.create(
            name="默认渠道",
            channel_code="default",
            base_url="https://api.openai.com/v1",
            api_path="/images/generations",
            model_name="gpt-image-1",
            is_active=True,
        )
        topic = GenerationTopic.objects.create(user=self.owner, title="项目")
        task = GenerateTask.objects.create(
            user=self.owner,
            topic=topic,
            channel=channel,
            task_no="GTEST0001",
            status=GenerateTask.Status.SUCCEEDED,
            prompt="owner image",
        )
        self.image = GeneratedImage.objects.create(
            task=task,
            user=self.owner,
            image_index=1,
            storage_provider="local",
            bucket_name="generated-images",
            object_key="users/1/tasks/GTEST0001/original-1.png",
            mime_type="image/png",
            file_size=16,
            width=128,
            height=128,
            sha256="abc123",
            expires_at=timezone.now() + timedelta(days=1),
        )

    def test_other_user_cannot_fetch_download_url(self):
        self.client.force_login(self.other)
        response = self.client.post(f"/api/images/{self.image.id}/download-url", secure=True)
        self.assertEqual(response.status_code, 404)

    def test_download_filename_uses_prompt(self):
        target = Path(self.tempdir.name) / self.image.object_key
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(b"fake image bytes")
        self.client.force_login(self.owner)

        url_response = self.client.post(f"/api/images/{self.image.id}/download-url", secure=True)
        download_response = self.client.get(url_response.json()["data"]["url"], secure=True)

        self.assertEqual(download_response.status_code, 200)
        self.assertIn("owner%20image.png", download_response["Content-Disposition"])

    def test_download_url_preview_returns_resized_inline_image(self):
        image = Image.new("RGB", (2048, 1024), color=(48, 92, 210))
        buffer = io.BytesIO()
        image.save(buffer, format="PNG")

        target = Path(self.tempdir.name) / self.image.object_key
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(buffer.getvalue())
        self.client.force_login(self.owner)

        url_response = self.client.post(f"/api/images/{self.image.id}/download-url", secure=True)
        preview_url = url_response.json()["data"]["preview_url"]
        self.assertIn("preview=1", preview_url)

        preview_response = self.client.get(preview_url, secure=True)
        self.assertEqual(preview_response.status_code, 200)
        self.assertEqual(preview_response["Content-Type"], "image/jpeg")

        preview_image = Image.open(io.BytesIO(preview_response.content))
        self.assertLessEqual(max(preview_image.size), 768)

    def test_expire_temporary_reference_images_only_removes_expired_uploads(self):
        expired_temp = Path(self.tempdir.name) / f"users/{self.owner.id}/tasks/R123456/reference.png"
        expired_temp.parent.mkdir(parents=True, exist_ok=True)
        expired_temp.write_bytes(b"temporary reference")

        fresh_temp = Path(self.tempdir.name) / f"users/{self.owner.id}/tasks/R123457/reference.png"
        fresh_temp.parent.mkdir(parents=True, exist_ok=True)
        fresh_temp.write_bytes(b"fresh temporary reference")

        permanent_reference = Path(self.tempdir.name) / f"users/{self.owner.id}/tasks/GTEST0001/reference.png"
        permanent_reference.parent.mkdir(parents=True, exist_ok=True)
        permanent_reference.write_bytes(b"task reference")

        expired_timestamp = time.time() - 7200
        os.utime(expired_temp, (expired_timestamp, expired_timestamp))

        deleted = expire_temporary_reference_images(limit=10)

        self.assertEqual(deleted, 1)
        self.assertFalse(expired_temp.exists())
        self.assertTrue(fresh_temp.exists())
        self.assertTrue(permanent_reference.exists())

    def test_expire_temporary_reference_images_task_returns_deleted_count(self):
        expired_temp = Path(self.tempdir.name) / f"users/{self.owner.id}/tasks/R765432/reference.png"
        expired_temp.parent.mkdir(parents=True, exist_ok=True)
        expired_temp.write_bytes(b"temporary reference")

        expired_timestamp = time.time() - 7200
        os.utime(expired_temp, (expired_timestamp, expired_timestamp))

        self.assertEqual(expire_temporary_reference_images_task(), 1)
        self.assertFalse(expired_temp.exists())

    def test_fetch_remote_image_blocks_private_ip_targets(self):
        from .services import fetch_remote_image

        with self.assertRaisesMessage(ValueError, "远程图片地址禁止访问内网主机"):
            fetch_remote_image("http://127.0.0.1/private.png")

    @override_settings(REMOTE_IMAGE_ALLOWED_HOSTS=["assets.example.com"])
    def test_fetch_remote_image_allows_explicitly_configured_host(self):
        from .services import fetch_remote_image

        class DummyResponse:
            status_code = 200
            headers = {"Content-Type": "image/png"}
            is_redirect = False
            is_permanent_redirect = False

            def raise_for_status(self):
                return None

            def iter_content(self, chunk_size=0):
                yield b"png-bytes"

            def close(self):
                return None

            def __enter__(self):
                return self

            def __exit__(self, exc_type, exc, tb):
                return False

        with patch("apps.mediahub.services.requests.get", return_value=DummyResponse()) as mock_get:
            content, mime_type = fetch_remote_image("https://assets.example.com/result.png")

        self.assertEqual(content, b"png-bytes")
        self.assertEqual(mime_type, "image/png")
        self.assertEqual(mock_get.call_args.kwargs["allow_redirects"], False)


@override_settings(
    MINIO_USE_LOCAL_FS=True,
    CACHES={"default": {"BACKEND": "django.core.cache.backends.locmem.LocMemCache"}},
    RESUMABLE_UPLOAD_CHUNK_SIZE=8,
    MOCK_PROVIDER_ENABLED=True,
    NOVELVIZ_IMAGE_API_KEY="novel-image-key",
)
class ResumableUploadTests(TestCase):
    def setUp(self):
        self.tempdir = TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.override = override_settings(
            MEDIA_ROOT=Path(self.tempdir.name) / "media",
            MINIO_LOCAL_ROOT=Path(self.tempdir.name) / "minio",
        )
        self.override.enable()
        self.addCleanup(self.override.disable)

        self.user = User.objects.create_user(username="uploader", email="uploader@example.com", password="Uploader123!@#")
        self.other = User.objects.create_user(username="other-uploader", email="other-uploader@example.com", password="Uploader123!@#")
        self.channel = ImageChannel.objects.create(
            name="默认渠道",
            channel_code="default",
            base_url="https://api.openai.com/v1",
            api_path="/images/generations",
            model_name="gpt-image-1",
            is_active=True,
        )

    @staticmethod
    def make_png_bytes() -> bytes:
        image = Image.new("RGB", (16, 16), color=(80, 120, 220))
        buffer = io.BytesIO()
        image.save(buffer, format="PNG")
        return buffer.getvalue()

    def create_session(self, *, kind: str, filename: str, content_type: str, content: bytes, client_upload_id: str = "client-1"):
        self.client.force_login(self.user)
        response = self.client.post(
            "/api/uploads/sessions",
            data=json.dumps(
                {
                    "kind": kind,
                    "filename": filename,
                    "content_type": content_type,
                    "total_size": len(content),
                    "client_upload_id": client_upload_id,
                }
            ),
            content_type="application/json",
            secure=True,
        )
        self.assertEqual(response.status_code, 201)
        return response.json()["data"]

    def upload_all_chunks(self, session: dict, content: bytes):
        for index in range(session["total_chunks"]):
            start = index * session["chunk_size"]
            chunk = content[start : start + session["chunk_size"]]
            response = self.client.put(
                f"/api/uploads/sessions/{session['upload_id']}/chunks/{index}",
                data=chunk,
                content_type="application/octet-stream",
                secure=True,
            )
            self.assertEqual(response.status_code, 200)
        return self.client.post(f"/api/uploads/sessions/{session['upload_id']}/complete", secure=True)

    def test_create_session_and_resume_by_client_upload_id(self):
        content = self.make_png_bytes()
        first = self.create_session(
            kind=UploadSession.Kind.GENERATION_REFERENCE_IMAGE,
            filename="ref.png",
            content_type="image/png",
            content=content,
            client_upload_id="same-file",
        )
        second = self.create_session(
            kind=UploadSession.Kind.GENERATION_REFERENCE_IMAGE,
            filename="ref.png",
            content_type="image/png",
            content=content,
            client_upload_id="same-file",
        )

        self.assertEqual(first["upload_id"], second["upload_id"])
        self.assertEqual(second["chunk_size"], 8)

    def test_chunk_upload_is_idempotent_and_complete_reference_returns_token(self):
        content = self.make_png_bytes()
        session = self.create_session(
            kind=UploadSession.Kind.GENERATION_REFERENCE_IMAGE,
            filename="ref.png",
            content_type="image/png",
            content=content,
        )

        first_chunk = content[: session["chunk_size"]]
        first_response = self.client.put(
            f"/api/uploads/sessions/{session['upload_id']}/chunks/0",
            data=first_chunk,
            content_type="application/octet-stream",
            secure=True,
        )
        second_response = self.client.put(
            f"/api/uploads/sessions/{session['upload_id']}/chunks/0",
            data=first_chunk,
            content_type="application/octet-stream",
            secure=True,
        )

        self.assertEqual(first_response.status_code, 200)
        self.assertEqual(second_response.status_code, 200)
        complete_response = self.upload_all_chunks(session, content)
        self.assertEqual(complete_response.status_code, 200)
        result = complete_response.json()["data"]["result"]
        self.assertTrue(result["reference_upload_token"])
        self.assertEqual(result["reference_image_name"], "ref.png")

    def test_complete_with_missing_chunk_fails(self):
        content = self.make_png_bytes()
        session = self.create_session(
            kind=UploadSession.Kind.GENERATION_REFERENCE_IMAGE,
            filename="ref.png",
            content_type="image/png",
            content=content,
        )
        response = self.client.post(f"/api/uploads/sessions/{session['upload_id']}/complete", secure=True)

        self.assertEqual(response.status_code, 400)
        self.assertIn("分片不完整", response.json()["message"])
        self.assertEqual(UploadSession.objects.get(upload_id=session["upload_id"]).status, UploadSession.Status.UPLOADING)

    def test_other_user_cannot_access_upload_session(self):
        content = self.make_png_bytes()
        session = self.create_session(
            kind=UploadSession.Kind.GENERATION_REFERENCE_IMAGE,
            filename="ref.png",
            content_type="image/png",
            content=content,
        )
        self.client.force_login(self.other)

        response = self.client.get(f"/api/uploads/sessions/{session['upload_id']}", secure=True)

        self.assertEqual(response.status_code, 404)

    @override_settings(MAX_REFERENCE_IMAGE_BYTES=10)
    def test_oversized_reference_upload_is_rejected(self):
        self.client.force_login(self.user)
        response = self.client.post(
            "/api/uploads/sessions",
            data=json.dumps(
                {
                    "kind": UploadSession.Kind.GENERATION_REFERENCE_IMAGE,
                    "filename": "ref.png",
                    "content_type": "image/png",
                    "total_size": 11,
                    "client_upload_id": "too-big",
                }
            ),
            content_type="application/json",
            secure=True,
        )

        self.assertEqual(response.status_code, 400)

    def test_expire_upload_sessions_marks_session_and_removes_chunks(self):
        content = self.make_png_bytes()
        session_data = self.create_session(
            kind=UploadSession.Kind.GENERATION_REFERENCE_IMAGE,
            filename="ref.png",
            content_type="image/png",
            content=content,
        )
        self.client.put(
            f"/api/uploads/sessions/{session_data['upload_id']}/chunks/0",
            data=content[: session_data["chunk_size"]],
            content_type="application/octet-stream",
            secure=True,
        )
        session = UploadSession.objects.get(upload_id=session_data["upload_id"])
        session.expires_at = timezone.now() - timedelta(seconds=1)
        session.save(update_fields=["expires_at"])

        self.assertEqual(expire_upload_sessions(), 1)
        session.refresh_from_db()

        self.assertEqual(session.status, UploadSession.Status.EXPIRED)
        self.assertFalse((Path(settings.MEDIA_ROOT) / "uploads" / "resumable" / str(self.user.id) / session.upload_id).exists())
        self.assertEqual(expire_upload_sessions_task(), 0)

    def test_reference_upload_token_can_create_image_to_image_task(self):
        content = self.make_png_bytes()
        session = self.create_session(
            kind=UploadSession.Kind.GENERATION_REFERENCE_IMAGE,
            filename="ref.png",
            content_type="image/png",
            content=content,
        )
        complete_response = self.upload_all_chunks(session, content)
        result = complete_response.json()["data"]["result"]

        task, created = submit_generation_task(
            user=self.user,
            prompt="参考图重绘",
            generation_mode="image_to_image",
            reference_image_name=result["reference_image_name"],
            reference_upload_token=result["reference_upload_token"],
            client_ip="127.0.0.1",
        )

        self.assertTrue(created)
        self.assertEqual(task.generation_mode, GenerateTask.Mode.IMAGE_TO_IMAGE)
        self.assertTrue(task.reference_object_key)

    def test_epub_upload_completion_creates_book_and_task_can_use_it(self):
        content = b"PK\x03\x04fake-epub-content-for-upload"
        session = self.create_session(
            kind=UploadSession.Kind.NOVEL_EPUB,
            filename="story.epub",
            content_type="application/epub+zip",
            content=content,
            client_upload_id="novel-1",
        )

        complete_response = self.upload_all_chunks(session, content)

        self.assertEqual(complete_response.status_code, 200)
        book = complete_response.json()["data"]["result"]
        self.assertEqual(book["source"], "uploaded")
        self.assertEqual(book["filename"], "story.epub")
        self.assertTrue(NovelUploadedBook.objects.filter(id=book["uploaded_book_id"], user=self.user).exists())

        create_response = self.client.post(
            "/api/novelviz/tasks",
            data=json.dumps(
                {
                    "book_source": "uploaded",
                    "book_filename": book["filename"],
                    "uploaded_book_id": book["uploaded_book_id"],
                    "scene_mode": "targeted",
                    "target_plot": "主角第一次进入星港",
                }
            ),
            content_type="application/json",
            secure=True,
        )

        self.assertEqual(create_response.status_code, 201)
        self.assertEqual(NovelVisualizationTask.objects.get(id=create_response.json()["data"]["id"]).uploaded_book_id, book["uploaded_book_id"])

    def test_retry_existing_novel_task_reuses_scene_plan(self):
        task = NovelVisualizationTask.objects.create(
            user=self.user,
            book_filename="story.epub",
            book_source=NovelVisualizationTask.BookSource.UPLOADED,
            scene_mode=NovelVisualizationTask.SceneMode.TARGETED,
            target_plot="主角第一次进入星港",
            status=NovelVisualizationTask.Status.FAILED,
            progress_percent=100,
            error_message="全部场景图片生成失败",
            scenes_json=[
                {
                    "title": "星港初见",
                    "chapter": "第一章",
                    "story_significance": "主角进入新世界",
                    "excerpt": "舱门缓缓打开。",
                    "visual_prompt": "A cinematic star port opening in front of the hero.",
                }
            ],
            logs=[{"id": 1, "type": "scene_plan_ready", "payload": {"message": "已定位指定剧情镜头"}, "created_at": "2026-01-01T00:00:00+00:00"}],
            last_event_id=1,
        )
        scene = NovelSceneImage.objects.create(
            task=task,
            scene_number=1,
            title="星港初见",
            chapter="第一章",
            story_significance="主角进入新世界",
            excerpt="舱门缓缓打开。",
            visual_prompt="A cinematic star port opening in front of the hero.",
            status=NovelSceneImage.Status.FAILED,
            error_message="HTTP 502",
            attempts=1,
        )

        retried = reset_task_for_retry(task.id, self.user)
        scene.refresh_from_db()

        self.assertEqual(retried.status, NovelVisualizationTask.Status.QUEUED)
        self.assertEqual(retried.progress_percent, 36)
        self.assertEqual(retried.scenes_json[0]["title"], "星港初见")
        self.assertEqual(retried.last_event_id, 1)
        self.assertEqual(scene.status, NovelSceneImage.Status.PENDING)
        self.assertEqual(scene.error_message, "")
        self.assertEqual(scene.attempts, 0)
