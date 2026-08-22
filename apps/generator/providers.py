from __future__ import annotations

import base64
import io
import logging
import textwrap
import threading
from dataclasses import dataclass
from pathlib import Path

import requests
from PIL import Image, ImageDraw, ImageOps
from django.conf import settings

from .models import GenerateTask, ImageChannel
from apps.mediahub.services import read_storage_object

logger = logging.getLogger(__name__)

# Thread-local keep-alive sessions. Each worker thread gets its own requests
# Session (requests.Session is not thread-safe), but every session shares the
# same underlying urllib3 connection pool settings, so a single thread reuses
# its connections across back-to-back generations and multiple worker threads
# can hit the upstream concurrently without connection churn or TTL handshakes.
_PROVIDER_POOL_CONNECTIONS = 16
_PROVIDER_POOL_MAXSIZE = 64

_thread_local = threading.local()


def _provider_session() -> requests.Session:
    session = getattr(_thread_local, "session", None)
    if session is None:
        session = requests.Session()
        adapter = requests.adapters.HTTPAdapter(
            pool_connections=_PROVIDER_POOL_CONNECTIONS,
            pool_maxsize=_PROVIDER_POOL_MAXSIZE,
            max_retries=0,
            pool_block=False,
        )
        session.mount("https://", adapter)
        session.mount("http://", adapter)
        _thread_local.session = session
    return session


@dataclass
class ProviderImage:
    mime_type: str
    content: bytes | None = None
    remote_url: str | None = None


@dataclass
class ProviderResult:
    request_id: str
    raw_response: dict
    images: list[ProviderImage]


class ProviderRequestError(ValueError):
    def __init__(
        self,
        message: str,
        *,
        raw_response: dict | None = None,
        request_id: str = "",
        status_code: int | None = None,
    ) -> None:
        super().__init__(message)
        self.raw_response = raw_response or {}
        self.request_id = request_id
        self.status_code = status_code


class BaseImageProviderAdapter:
    def generate(self, *, task) -> ProviderResult:
        raise NotImplementedError


def _join_url(base_url: str, api_path: str) -> str:
    return f"{base_url.rstrip('/')}/{api_path.lstrip('/')}"


def _extract_provider_message(data: dict, default: str) -> str:
    for key in ("message", "msg", "error", "errorMessage", "reason", "code"):
        value = data.get(key)
        if value:
            return str(value)
    return default


def _trim_openai_image_response(data: dict) -> dict:
    trimmed = dict(data)
    items = []
    for item in data.get("data") or []:
        if not isinstance(item, dict):
            continue
        entry = dict(item)
        if entry.get("b64_json"):
            entry["b64_json"] = f"<omitted:{len(entry['b64_json'])} chars>"
        items.append(entry)
    if items:
        trimmed["data"] = items
    return trimmed


def _decode_reference_image_data_url(data_url: str) -> tuple[bytes, str] | tuple[None, None]:
    raw = (data_url or "").strip()
    if not raw or "," not in raw:
        return None, None
    header, encoded = raw.split(",", 1)
    if ";base64" not in header:
        return None, None
    mime_type = header.split(":", 1)[-1].split(";", 1)[0].strip().lower() or "image/png"
    return base64.b64decode(encoded), mime_type


def _load_task_reference_image(task: GenerateTask) -> tuple[bytes | None, str | None]:
    if getattr(task, "reference_object_key", ""):
        content = read_storage_object(
            bucket_name=task.reference_bucket_name or settings.MINIO_BUCKET,
            object_key=task.reference_object_key,
        )
        mime_type = (task.reference_image_mime_type or "image/png").strip().lower() or "image/png"
        return content, mime_type
    return _decode_reference_image_data_url(task.reference_image_data_url)


def _encode_data_url(content: bytes, mime_type: str) -> str:
    return f"data:{mime_type};base64,{base64.b64encode(content).decode('ascii')}"


def _resize_image_to_limit(image: Image.Image, max_dimension: int) -> Image.Image:
    width, height = image.size
    longest_edge = max(width, height)
    if not longest_edge or longest_edge <= max_dimension:
        return image
    scale = max_dimension / float(longest_edge)
    resized = image.copy()
    resized.thumbnail((max(1, int(width * scale)), max(1, int(height * scale))), Image.Resampling.LANCZOS)
    return resized


def _save_png_with_limit(image: Image.Image, max_bytes: int, max_dimension: int) -> tuple[bytes, str, str]:
    # Resize once to the target dimension then encode; PNG is lossless so only the
    # dimensions (cheap rescale, not repeated full-size encodes) trade size.
    current = _resize_image_to_limit(image, max_dimension)
    for dimension_limit in [max_dimension, 1344, 1024, 768, 512]:
        buffer = io.BytesIO()
        current.save(buffer, format="PNG", optimize=True)
        payload = buffer.getvalue()
        if len(payload) <= max_bytes:
            return payload, "image/png", ".png"
        current = _resize_image_to_limit(current, dimension_limit)
    # Last iteration above is still too big; return the smallest produced.
    return payload, "image/png", ".png"


def _save_jpeg_with_limit(image: Image.Image, max_bytes: int, max_dimension: int) -> tuple[bytes, str, str]:
    # Fast path: single resize to the max dimension, then try quality levels (the
    # encode dominates cost, and re-scaling the already-limited image is wasteful).
    # Only step the dimension down if even the lowest quality exceeds the budget.
    candidate = _resize_image_to_limit(image, max_dimension).convert("RGB")
    smallest_payload = b""
    for dimension_limit in [max_dimension, 1344, 1152, 1024, 896, 768]:
        for quality in [88, 82, 76, 70, 64]:
            buffer = io.BytesIO()
            candidate.save(buffer, format="JPEG", quality=quality, optimize=True, progressive=True)
            payload = buffer.getvalue()
            if not smallest_payload or len(payload) < len(smallest_payload):
                smallest_payload = payload
            if len(payload) <= max_bytes:
                return payload, "image/jpeg", ".jpg"
        candidate = _resize_image_to_limit(candidate, dimension_limit).convert("RGB")
    return smallest_payload, "image/jpeg", ".jpg"


def _normalize_reference_image_for_edit(content: bytes, mime_type: str) -> tuple[bytes, str, str]:
    """Convert reference uploads to a provider-friendly, size-limited payload."""
    try:
        image = Image.open(io.BytesIO(content))
    except Exception as exc:
        raise ValueError("参考图解析失败") from exc

    image = ImageOps.exif_transpose(image)
    has_alpha = "A" in image.getbands() or "transparency" in image.info
    max_dimension = int(getattr(settings, "OPENAI_EDIT_REFERENCE_MAX_DIMENSION", 1600) or 1600)
    max_bytes = int(getattr(settings, "OPENAI_EDIT_REFERENCE_MAX_BYTES", 4 * 1024 * 1024) or 4 * 1024 * 1024)

    if has_alpha:
        return _save_png_with_limit(image.convert("RGBA"), max_bytes, max_dimension)
    return _save_jpeg_with_limit(image.convert("RGB"), max_bytes, max_dimension)


def _openai_edits_path(api_path: str) -> str:
    path = (api_path or "/v1/images/generations").strip() or "/v1/images/generations"
    if path.endswith("/images/generations"):
        return path[: -len("/images/generations")] + "/images/edits"
    if path.endswith("/generations"):
        return path[: -len("/generations")] + "/edits"
    return path.rstrip("/") + "/edits"


def _resolve_openai_edit_path(channel) -> str:
    custom_path = (getattr(channel, "edit_api_path", "") or "").strip()
    if custom_path:
        return custom_path
    return _openai_edits_path(channel.api_path or "/v1/images/generations")


def _openai_edit_timeout_seconds(channel) -> int:
    # Some OpenAI-compatible gateways accept the img2img request immediately
    # but need much longer than text-to-image before a final image payload is
    # returned. Keep a higher floor for edits to avoid false local timeouts.
    return max(
        int(getattr(channel, "timeout_seconds", 60) or 60),
        int(getattr(settings, "OPENAI_IMAGE_EDIT_TIMEOUT_SECONDS", 420) or 420),
    )


def _openai_generation_timeout_seconds(channel) -> int:
    return max(
        int(getattr(channel, "timeout_seconds", 60) or 60),
        int(getattr(settings, "PROVIDER_TIMEOUT_SECONDS", 300) or 300),
    )


class MockImageProviderAdapter(BaseImageProviderAdapter):
    def generate(self, *, task) -> ProviderResult:
        width, height = [int(item) for item in task.size_code.split("x")]
        image = Image.new("RGB", (width, height), color=(20, 32, 59))
        reference_content, _reference_mime_type = _load_task_reference_image(task)
        if reference_content:
            reference = Image.open(io.BytesIO(reference_content)).convert("RGB")
            image = ImageOps.fit(reference, (width, height))
            image = Image.blend(image, Image.new("RGB", (width, height), color=(15, 23, 42)), 0.28)
        draw = ImageDraw.Draw(image)
        lines = [
            "Mock 图片" if task.generation_mode == "text_to_image" else "Mock 图生图",
            f"任务: {task.task_no}",
            f"风格: {task.style_code}",
            f"参考图: {Path(task.reference_image_name).name or '未上传'}" if task.reference_image_data_url else "",
            "",
            *textwrap.wrap(task.prompt[:120], width=18),
        ]
        if not reference_content:
            draw.rectangle((0, 0, width, height), fill=(15, 23, 42))
            draw.rounded_rectangle((50, 50, width - 50, height - 50), radius=28, fill=(47, 107, 255))
        else:
            draw.rounded_rectangle((36, 36, width - 36, height - 36), radius=28, outline=(255, 255, 255), width=3)
        draw.multiline_text((90, 110), "\n".join(lines), fill=(255, 255, 255), spacing=12)
        buffer = io.BytesIO()
        image.save(buffer, format="PNG")
        return ProviderResult(
            request_id=f"mock-{task.task_no}",
            raw_response={"mock": True, "task_no": task.task_no},
            images=[ProviderImage(mime_type="image/png", content=buffer.getvalue())],
        )


class OpenAICompatibleImageProviderAdapter(BaseImageProviderAdapter):
    def _post_json(self, *, url: str, api_key: str, payload: dict, timeout_seconds: int) -> tuple[dict, str]:
        response = _provider_session().post(
            url,
            headers={
                "Content-Type": "application/json",
                "Authorization": f"Bearer {api_key}",
            },
            json=payload,
            timeout=timeout_seconds,
        )
        try:
            data = response.json()
        except ValueError as exc:
            raise ValueError(f"OpenAI 兼容渠道返回了不可解析的响应：HTTP {response.status_code}") from exc
        if response.status_code >= 400:
            error = data.get("error")
            request_id = response.headers.get("x-request-id", "")
            if isinstance(error, dict):
                raise ProviderRequestError(
                    str(error.get("message") or error.get("code") or f"OpenAI 兼容渠道请求失败：HTTP {response.status_code}"),
                    raw_response=data,
                    request_id=request_id,
                    status_code=response.status_code,
                )
            raise ProviderRequestError(
                _extract_provider_message(data, f"OpenAI 兼容渠道请求失败：HTTP {response.status_code}"),
                raw_response=data,
                request_id=request_id,
                status_code=response.status_code,
            )
        return data, response.headers.get("x-request-id", "")

    def _post_multipart(
        self,
        *,
        url: str,
        api_key: str,
        data: dict,
        files: dict,
        timeout_seconds: int,
    ) -> tuple[dict, str]:
        response = _provider_session().post(
            url,
            headers={
                "Authorization": f"Bearer {api_key}",
            },
            data=data,
            files=files,
            timeout=timeout_seconds,
        )
        try:
            payload = response.json()
        except ValueError as exc:
            raise ValueError(f"OpenAI 兼容渠道返回了不可解析的响应：HTTP {response.status_code}") from exc
        if response.status_code >= 400:
            error = payload.get("error")
            request_id = response.headers.get("x-request-id", "")
            if isinstance(error, dict):
                raise ProviderRequestError(
                    str(error.get("message") or error.get("code") or f"OpenAI 兼容渠道请求失败：HTTP {response.status_code}"),
                    raw_response=payload,
                    request_id=request_id,
                    status_code=response.status_code,
                )
            raise ProviderRequestError(
                _extract_provider_message(payload, f"OpenAI 兼容渠道请求失败：HTTP {response.status_code}"),
                raw_response=payload,
                request_id=request_id,
                status_code=response.status_code,
            )
        return payload, response.headers.get("x-request-id", "")

    def generate(self, *, task) -> ProviderResult:
        channel = task.channel
        api_key = channel.api_key
        if not api_key:
            raise ValueError("缺少 OpenAI 兼容渠道 API Key")

        prompt = (task.prompt or "").strip()
        if task.negative_prompt.strip():
            prompt = f"{prompt}\nAvoid: {task.negative_prompt.strip()}"
        quality = getattr(task, "quality_code", "auto") or "auto"

        if task.generation_mode == "image_to_image" and (task.reference_object_key or task.reference_image_data_url):
            reference_content, mime_type = _load_task_reference_image(task)
            if not reference_content or not mime_type:
                raise ValueError("参考图解析失败")
            reference_content, mime_type, file_suffix = _normalize_reference_image_for_edit(reference_content, mime_type)
            edit_url = _join_url(channel.base_url, _resolve_openai_edit_path(channel))
            edit_timeout_seconds = _openai_edit_timeout_seconds(channel)
            edit_payload = {
                "model": channel.model_name,
                "prompt": prompt,
                "size": task.size_code or "1024x1024",
                "quality": quality,
            }
            file_stem = Path(task.reference_image_name or "reference-image").stem or "reference-image"
            file_name = f"{file_stem}{file_suffix}"
            logger.info(
                "发送 OpenAI 兼容图生图请求 task_no=%s url=%s size=%s quality=%s transport=multipart-image timeout=%s bytes=%s mime=%s",
                task.task_no,
                edit_url,
                task.size_code or "1024x1024",
                quality,
                edit_timeout_seconds,
                len(reference_content),
                mime_type,
            )
            try:
                response_data, request_id = self._post_multipart(
                    url=edit_url,
                    api_key=api_key,
                    data=edit_payload,
                    files={"image": (file_name, reference_content, mime_type)},
                    timeout_seconds=edit_timeout_seconds,
                )
            except ProviderRequestError as exc:
                if exc.status_code not in {400, 404, 405, 415, 422}:
                    raise
                logger.warning(
                    "OpenAI 兼容图生图 multipart 被拒绝，回退 JSON image_url task_no=%s status=%s",
                    task.task_no,
                    exc.status_code,
                )
                fallback_payload = {
                    **edit_payload,
                    "images": [{"image_url": _encode_data_url(reference_content, mime_type)}],
                }
                response_data, request_id = self._post_json(
                    url=edit_url,
                    api_key=api_key,
                    payload=fallback_payload,
                    timeout_seconds=edit_timeout_seconds,
                )
        else:
            payload = {
                "model": channel.model_name,
                "prompt": prompt,
                "size": task.size_code or "1024x1024",
                "quality": quality,
            }
            generation_url = _join_url(channel.base_url, channel.api_path or "/v1/images/generations")
            logger.info(
                "发送 OpenAI 兼容文生图请求 task_no=%s url=%s size=%s quality=%s",
                task.task_no,
                generation_url,
                task.size_code or "1024x1024",
                quality,
            )
            generation_timeout_seconds = _openai_generation_timeout_seconds(channel)
            response_data, request_id = self._post_json(
                url=generation_url,
                api_key=api_key,
                payload=payload,
                timeout_seconds=generation_timeout_seconds,
            )

        images: list[ProviderImage] = []
        for item in response_data.get("data") or []:
            if not isinstance(item, dict):
                continue
            b64_json = item.get("b64_json")
            if b64_json:
                try:
                    content = base64.b64decode(b64_json)
                except Exception as exc:
                    raise ValueError("OpenAI 兼容渠道返回的图片数据无法解码") from exc
                images.append(ProviderImage(mime_type="image/png", content=content))
                continue
            for key in ("url", "image_url"):
                if item.get(key):
                    images.append(ProviderImage(mime_type="image/png", remote_url=item[key]))
                    break

        if not images:
            raise ValueError("OpenAI 兼容渠道未返回可用图片")

        fallback_request_id = str(response_data.get("created") or response_data.get("id") or task.task_no)
        logger.info(
            "OpenAI 兼容渠道已返回响应 task_no=%s images=%s request_id=%s",
            task.task_no,
            len(images),
            request_id or fallback_request_id,
        )
        return ProviderResult(
            request_id=request_id or fallback_request_id,
            raw_response=_trim_openai_image_response(response_data),
            images=images,
        )


class SiliconFlowImageProviderAdapter(OpenAICompatibleImageProviderAdapter):
    """Adapter for 硅基流动 (SiliconFlow) Qwen image models.

    SiliconFlow is only loosely OpenAI-compatible:
      - Both text-to-image and image-to-image (Qwen-Image-Edit) go to the SAME
        /v1/images/generations endpoint.
      - img2img passes the reference as a JSON `image` field whose value is a
        base64 data-URI (`data:image/png;base64,...`), NOT multipart on /edits.
      - The size parameter is `image_size` (optional for edit models).
      - The response carries the generated image as a remote `url` that must be
        fetched and stored.
    """

    def generate(self, *, task) -> ProviderResult:
        channel = task.channel
        api_key = channel.api_key
        if not api_key:
            raise ValueError("缺少生图渠道 API Key")

        prompt = (task.prompt or "").strip()
        if task.negative_prompt.strip():
            prompt = f"{prompt}\nAvoid: {task.negative_prompt.strip()}"
        quality = getattr(task, "quality_code", "auto") or "auto"

        payload = {
            "model": channel.model_name,
            "prompt": prompt,
        }
        # Map the app's size to SiliconFlow's `image_size`. Edit models (Qwen-Image*
        # -Edit) ignore image_size; t2i models (Qwen/Qwen-Image) accept it.
        size_code = task.size_code or "1024x1024"
        if task.generation_mode == GenerateTask.Mode.TEXT_TO_IMAGE:
            payload["image_size"] = size_code

        has_reference = bool(task.reference_object_key or task.reference_image_data_url)
        if task.generation_mode == GenerateTask.Mode.IMAGE_TO_IMAGE:
            if not has_reference:
                raise ValueError("图生图模式需要上传参考图")
            reference_content, mime_type = _load_task_reference_image(task)
            if not reference_content or not mime_type:
                raise ValueError("参考图解析失败")
            # Normalize/size-limit the reference, then send as a base64 data-URI.
            reference_content, mime_type, _suffix = _normalize_reference_image_for_edit(reference_content, mime_type)
            data_uri = _encode_data_url(reference_content, mime_type)
            payload["image"] = data_uri

        generation_url = _join_url(channel.base_url, channel.api_path or "/v1/images/generations")
        timeout_seconds = _openai_generation_timeout_seconds(channel)
        logger.info(
            "发送硅基流动生图请求 task_no=%s url=%s mode=%s size=%s quality=%s has_ref=%s",
            task.task_no,
            generation_url,
            task.generation_mode,
            size_code,
            quality,
            has_reference,
        )
        response_data, request_id = self._post_json(
            url=generation_url,
            api_key=api_key,
            payload=payload,
            timeout_seconds=timeout_seconds,
        )

        images: list[ProviderImage] = []
        # SiliconFlow returns both `images` and `data` arrays with {url}.
        for item in list(response_data.get("images") or []) + list(response_data.get("data") or []):
            if not isinstance(item, dict):
                continue
            b64_json = item.get("b64_json")
            if b64_json:
                try:
                    images.append(ProviderImage(mime_type="image/png", content=base64.b64decode(b64_json)))
                except Exception:
                    continue
                if images:
                    break
                continue
            for key in ("url", "image_url"):
                if item.get(key):
                    images.append(ProviderImage(mime_type="image/png", remote_url=item[key]))
                    break
            if images:
                break

        if not images:
            raise ValueError("硅基流动渠道未返回可用图片")

        fallback_request_id = str(response_data.get("created") or response_data.get("id") or task.task_no)
        logger.info(
            "硅基流动渠道已返回响应 task_no=%s images=%s request_id=%s",
            task.task_no,
            len(images),
            request_id or fallback_request_id,
        )
        return ProviderResult(
            request_id=request_id or fallback_request_id,
            raw_response=_trim_openai_image_response(response_data),
            images=images,
        )


def get_adapter(*, task=None) -> BaseImageProviderAdapter:
    if settings.MOCK_PROVIDER_ENABLED:
        return MockImageProviderAdapter()
    vendor = getattr(getattr(task, "channel", None), "provider_vendor", None)
    if vendor == ImageChannel.ProviderVendor.SILICONFLOW:
        return SiliconFlowImageProviderAdapter()
    return OpenAICompatibleImageProviderAdapter()
