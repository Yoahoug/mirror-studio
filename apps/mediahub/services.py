from __future__ import annotations

import hashlib
import io
import ipaddress
import mimetypes
import re
import shutil
import socket
import time
from datetime import timedelta
from pathlib import Path
from urllib.parse import quote, urljoin, urlparse

import requests
from PIL import Image
from django.conf import settings
from django.core.signing import BadSignature, SignatureExpired, TimestampSigner
from django.db import transaction
from django.http import Http404, HttpResponse
from django.utils import timezone
from minio import Minio

from .models import GeneratedImage, UploadSession


TEMPORARY_REFERENCE_OBJECT_KEY_RE = re.compile(r"^users/\d+/tasks/R[^/]+/reference(?:\.[^/]+)?$")
REMOTE_IMAGE_REDIRECT_LIMIT = 3
ALLOWED_RESUMABLE_UPLOAD_KINDS = {
    UploadSession.Kind.GENERATION_REFERENCE_IMAGE,
    UploadSession.Kind.NOVEL_EPUB,
}


def _storage_mode() -> str:
    return "local" if settings.MINIO_USE_LOCAL_FS else "minio"


def _minio_client() -> Minio:
    return Minio(
        settings.MINIO_ENDPOINT,
        access_key=settings.MINIO_ACCESS_KEY,
        secret_key=settings.MINIO_SECRET_KEY,
        secure=settings.MINIO_SECURE,
        region=settings.MINIO_REGION,
    )


def ensure_bucket() -> None:
    if _storage_mode() == "local":
        Path(settings.MINIO_LOCAL_ROOT).mkdir(parents=True, exist_ok=True)
        return
    client = _minio_client()
    if not client.bucket_exists(settings.MINIO_BUCKET):
        client.make_bucket(settings.MINIO_BUCKET)


def _object_key(user_id: int, task_no: str, image_index: int, mime_type: str) -> str:
    extension = mimetypes.guess_extension(mime_type) or ".png"
    return f"users/{user_id}/tasks/{task_no}/original-{image_index}{extension}"


def _reference_object_key(user_id: int, task_no: str, mime_type: str) -> str:
    extension = mimetypes.guess_extension(mime_type) or ".png"
    return f"users/{user_id}/tasks/{task_no}/reference{extension}"


def _reference_upload_expiry_seconds() -> int:
    return max(int(getattr(settings, "GENERATION_REFERENCE_UPLOAD_TOKEN_MAX_AGE_SECONDS", 1800) or 1800), 60)


def _resumable_upload_root() -> Path:
    return Path(settings.MEDIA_ROOT) / "uploads" / "resumable"


def _session_dir(session: UploadSession) -> Path:
    return _resumable_upload_root() / str(session.user_id) / session.upload_id


def _chunk_path(session: UploadSession, index: int) -> Path:
    return _session_dir(session) / "chunks" / f"{index:08d}.part"


def _assembled_path(session: UploadSession) -> Path:
    return _session_dir(session) / "assembled.bin"


def _safe_upload_filename(filename: str, *, fallback: str) -> str:
    clean_name = Path((filename or "").strip()).name
    clean_name = re.sub(r"[\x00-\x1f/\\:]+", "-", clean_name).strip(" .")
    return (clean_name or fallback)[:255]


def _upload_size_limit(kind: str) -> int:
    if kind == UploadSession.Kind.GENERATION_REFERENCE_IMAGE:
        return int(settings.MAX_REFERENCE_IMAGE_BYTES)
    if kind == UploadSession.Kind.NOVEL_EPUB:
        return int(settings.NOVELVIZ_MAX_EPUB_UPLOAD_BYTES)
    raise ValueError("不支持的上传类型")


def _refresh_upload_expiry(session: UploadSession) -> None:
    session.expires_at = timezone.now() + timedelta(
        seconds=max(int(getattr(settings, "RESUMABLE_UPLOAD_SESSION_TTL_SECONDS", 24 * 60 * 60) or 0), 60)
    )


def _uploaded_chunk_indexes(session: UploadSession) -> list[int]:
    return sorted({int(index) for index in (session.uploaded_chunks or [])})


def _file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _abandon_upload_session(session: UploadSession) -> None:
    session.status = UploadSession.Status.CANCELED
    session.save(update_fields=["status", "updated_at"])
    cleanup_upload_session_files(session)


def _serialize_upload_session(session: UploadSession) -> dict:
    uploaded_chunks = _uploaded_chunk_indexes(session)
    return {
        "upload_id": session.upload_id,
        "kind": session.kind,
        "filename": session.filename,
        "content_type": session.content_type,
        "total_size": session.total_size,
        "chunk_size": session.chunk_size,
        "total_chunks": session.total_chunks,
        "uploaded_chunks": uploaded_chunks,
        "status": session.status,
        "expires_at": session.expires_at.isoformat() if session.expires_at else None,
        "result": session.result_json or {},
    }


def _mark_upload_expired_if_needed(session: UploadSession) -> UploadSession:
    if session.status == UploadSession.Status.UPLOADING and session.expires_at <= timezone.now():
        session.status = UploadSession.Status.EXPIRED
        session.save(update_fields=["status", "updated_at"])
    return session


def _is_temporary_reference_object_key(object_key: str) -> bool:
    return bool(TEMPORARY_REFERENCE_OBJECT_KEY_RE.match((object_key or "").strip()))


def _remote_image_allowed_hosts() -> set[str]:
    raw_hosts = getattr(settings, "REMOTE_IMAGE_ALLOWED_HOSTS", []) or []
    return {str(host).strip().lower() for host in raw_hosts if str(host).strip()}


def _validate_remote_image_url(url: str) -> str:
    parsed = urlparse((url or "").strip())
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        raise ValueError("远程图片地址不合法")

    hostname = (parsed.hostname or "").strip().lower()
    if not hostname:
        raise ValueError("远程图片地址不合法")

    allowed_hosts = _remote_image_allowed_hosts()
    if hostname in allowed_hosts:
        return parsed.geturl()

    try:
        resolved = socket.getaddrinfo(
            hostname,
            parsed.port or (443 if parsed.scheme == "https" else 80),
            type=socket.SOCK_STREAM,
        )
    except socket.gaierror as exc:
        raise ValueError("远程图片地址解析失败") from exc

    for _family, _socktype, _proto, _canonname, sockaddr in resolved:
        address = ipaddress.ip_address(sockaddr[0])
        if not address.is_global:
            raise ValueError("远程图片地址禁止访问内网主机")

    return parsed.geturl()


def _open_remote_image_stream(url: str) -> requests.Response:
    current_url = _validate_remote_image_url(url)
    for _ in range(REMOTE_IMAGE_REDIRECT_LIMIT + 1):
        response = requests.get(current_url, timeout=60, stream=True, allow_redirects=False)
        if response.is_redirect or response.is_permanent_redirect:
            location = (response.headers.get("Location") or "").strip()
            response.close()
            if not location:
                raise ValueError("远程图片重定向地址无效")
            current_url = _validate_remote_image_url(urljoin(current_url, location))
            continue
        return response
    raise ValueError("远程图片重定向次数过多")


def _prune_empty_parent_dirs(path: Path, *, root: Path) -> None:
    current = path
    while current != root and current.is_dir():
        try:
            current.rmdir()
        except OSError:
            break
        current = current.parent


def _image_dimensions(content: bytes) -> tuple[int, int]:
    try:
        image = Image.open(io.BytesIO(content))
        return image.size
    except Exception:
        return 0, 0


def _write_storage_object(*, object_key: str, content: bytes, mime_type: str) -> None:
    ensure_bucket()
    if _storage_mode() == "local":
        target = Path(settings.MINIO_LOCAL_ROOT) / object_key
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(content)
        return
    client = _minio_client()
    client.put_object(
        settings.MINIO_BUCKET,
        object_key,
        io.BytesIO(content),
        length=len(content),
        content_type=mime_type,
    )


def read_storage_object(*, bucket_name: str, object_key: str) -> bytes:
    if _storage_mode() == "local":
        target = Path(settings.MINIO_LOCAL_ROOT) / object_key
        return target.read_bytes()
    client = _minio_client()
    response = client.get_object(bucket_name, object_key)
    try:
        return response.read()
    finally:
        response.close()
        response.release_conn()


def delete_storage_object(*, bucket_name: str, object_key: str) -> None:
    if not object_key:
        return
    if _storage_mode() == "local":
        target = Path(settings.MINIO_LOCAL_ROOT) / object_key
        if target.exists():
            target.unlink()
            _prune_empty_parent_dirs(target.parent, root=Path(settings.MINIO_LOCAL_ROOT))
        return
    client = _minio_client()
    client.remove_object(bucket_name, object_key)


def create_or_resume_upload_session(
    *,
    user,
    kind: str,
    filename: str,
    content_type: str = "",
    total_size: int,
    client_upload_id: str = "",
) -> dict:
    kind = (kind or "").strip()
    if kind not in ALLOWED_RESUMABLE_UPLOAD_KINDS:
        raise ValueError("不支持的上传类型")
    try:
        total_size = int(total_size)
    except (TypeError, ValueError) as exc:
        raise ValueError("文件大小不合法") from exc
    if total_size <= 0:
        raise ValueError("上传文件不能为空")
    size_limit = _upload_size_limit(kind)
    if total_size > size_limit:
        raise ValueError(f"上传文件不能超过 {size_limit // 1024 // 1024} MB")

    fallback_name = "reference-image" if kind == UploadSession.Kind.GENERATION_REFERENCE_IMAGE else "novel.epub"
    clean_filename = _safe_upload_filename(filename, fallback=fallback_name)
    content_type = (content_type or "").strip().lower()[:128]
    client_upload_id = (client_upload_id or "").strip()[:128]
    chunk_size = max(int(getattr(settings, "RESUMABLE_UPLOAD_CHUNK_SIZE", 1024 * 1024) or 0), 1)
    total_chunks = (total_size + chunk_size - 1) // chunk_size

    with transaction.atomic():
        session = None
        if client_upload_id:
            session = (
                UploadSession.objects.select_for_update()
                .filter(
                    user=user,
                    kind=kind,
                    client_upload_id=client_upload_id,
                    total_size=total_size,
                    status__in=[UploadSession.Status.UPLOADING, UploadSession.Status.COMPLETED],
                )
                .order_by("-created_at", "-id")
                .first()
            )
        if session:
            _mark_upload_expired_if_needed(session)
            if session.status == UploadSession.Status.EXPIRED:
                cleanup_upload_session_files(session)
                session = None
            elif session.status == UploadSession.Status.UPLOADING and session.chunk_size != chunk_size:
                _abandon_upload_session(session)
                session = None
        if not session:
            session = UploadSession(
                user=user,
                kind=kind,
                filename=clean_filename,
                content_type=content_type,
                total_size=total_size,
                chunk_size=chunk_size,
                total_chunks=total_chunks,
                client_upload_id=client_upload_id,
            )
        elif session.status == UploadSession.Status.UPLOADING:
            session.filename = clean_filename
            session.content_type = content_type
        if session.status == UploadSession.Status.UPLOADING:
            _refresh_upload_expiry(session)
        session.save()
    return _serialize_upload_session(session)


def get_upload_session_for_user(*, user, upload_id: str, for_update: bool = False) -> UploadSession:
    queryset = UploadSession.objects.filter(user=user, upload_id=(upload_id or "").strip())
    if for_update:
        queryset = queryset.select_for_update()
    session = queryset.first()
    if not session:
        raise FileNotFoundError("上传会话不存在")
    return _mark_upload_expired_if_needed(session)


def serialize_upload_session_for_user(*, user, upload_id: str) -> dict:
    return _serialize_upload_session(get_upload_session_for_user(user=user, upload_id=upload_id))


def store_upload_chunk(*, user, upload_id: str, index: int, content: bytes) -> dict:
    try:
        index = int(index)
    except (TypeError, ValueError) as exc:
        raise ValueError("分片序号不合法") from exc
    if not content:
        raise ValueError("分片内容不能为空")

    with transaction.atomic():
        session = get_upload_session_for_user(user=user, upload_id=upload_id, for_update=True)
        if session.status != UploadSession.Status.UPLOADING:
            raise ValueError("上传会话不可继续上传")
        if index < 0 or index >= session.total_chunks:
            raise ValueError("分片序号超出范围")
        expected_size = session.chunk_size
        if index == session.total_chunks - 1:
            expected_size = session.total_size - session.chunk_size * (session.total_chunks - 1)
        if len(content) != expected_size:
            raise ValueError("分片大小不匹配")

        target = _chunk_path(session, index)
        target.parent.mkdir(parents=True, exist_ok=True)
        if target.exists() and target.stat().st_size == len(content):
            existing_hash = _file_sha256(target)
            next_hash = hashlib.sha256(content).hexdigest()
            if existing_hash != next_hash:
                target.write_bytes(content)
        else:
            target.write_bytes(content)
        uploaded_chunks = set(_uploaded_chunk_indexes(session))
        uploaded_chunks.add(index)
        session.uploaded_chunks = sorted(uploaded_chunks)
        _refresh_upload_expiry(session)
        session.save(update_fields=["uploaded_chunks", "expires_at", "updated_at"])
    return _serialize_upload_session(session)


def _assemble_upload(session: UploadSession) -> tuple[Path, str]:
    missing = [index for index in range(session.total_chunks) if index not in set(_uploaded_chunk_indexes(session))]
    if missing:
        raise ValueError("上传分片不完整，请继续上传")
    target = _assembled_path(session)
    target.parent.mkdir(parents=True, exist_ok=True)
    digest = hashlib.sha256()
    total_size = 0
    with target.open("wb") as output:
        for index in range(session.total_chunks):
            chunk = _chunk_path(session, index)
            if not chunk.exists():
                raise ValueError("上传分片不完整，请继续上传")
            with chunk.open("rb") as source:
                for content in iter(lambda: source.read(1024 * 1024), b""):
                    total_size += len(content)
                    digest.update(content)
                    output.write(content)
    if total_size != session.total_size:
        target.unlink(missing_ok=True)
        raise ValueError("上传文件大小校验失败，请重新上传")
    return target, digest.hexdigest()


def _complete_generation_reference_upload(*, session: UploadSession, assembled_path: Path) -> dict:
    from apps.generator.services import create_reference_upload_token, _validate_reference_image_content

    content = assembled_path.read_bytes()
    content, mime_type, reference_image_name = _validate_reference_image_content(
        content=content,
        mime_type=session.content_type,
        name=session.filename,
    )
    task_no = f"R{session.user_id}{int(time.time() * 1000)}"
    reference_meta = store_reference_image(
        user_id=session.user_id,
        task_no=task_no,
        content=content,
        mime_type=mime_type,
    )
    token = create_reference_upload_token(
        user_id=session.user_id,
        reference_image_name=reference_image_name,
        reference_meta=reference_meta,
    )
    return {
        "reference_upload_token": token,
        "reference_image_name": reference_image_name,
        "reference_image_attached": True,
    }


def _complete_novel_epub_upload(*, session: UploadSession, assembled_path: Path, sha256: str) -> dict:
    from apps.novelviz.services import serialize_uploaded_book, store_uploaded_epub_path

    uploaded_book = store_uploaded_epub_path(
        source_path=assembled_path,
        display_name=session.filename,
        user=session.user,
        sha256=sha256,
    )
    return serialize_uploaded_book(uploaded_book)


def complete_upload_session(*, user, upload_id: str) -> dict:
    with transaction.atomic():
        session = get_upload_session_for_user(user=user, upload_id=upload_id, for_update=True)
        if session.status == UploadSession.Status.COMPLETED:
            return _serialize_upload_session(session)
        if session.status != UploadSession.Status.UPLOADING:
            raise ValueError("上传会话不可完成")
        assembled_path, sha256 = _assemble_upload(session)
        if session.kind == UploadSession.Kind.GENERATION_REFERENCE_IMAGE:
            result = _complete_generation_reference_upload(session=session, assembled_path=assembled_path)
        elif session.kind == UploadSession.Kind.NOVEL_EPUB:
            result = _complete_novel_epub_upload(session=session, assembled_path=assembled_path, sha256=sha256)
        else:
            raise ValueError("不支持的上传类型")
        session.status = UploadSession.Status.COMPLETED
        session.result_json = result
        session.completed_at = timezone.now()
        session.save(update_fields=["status", "result_json", "completed_at", "updated_at"])
    cleanup_upload_session_files(session)
    return _serialize_upload_session(session)


def cleanup_upload_session_files(session: UploadSession) -> None:
    shutil.rmtree(_session_dir(session), ignore_errors=True)


def cancel_upload_session(*, user, upload_id: str) -> dict:
    with transaction.atomic():
        session = get_upload_session_for_user(user=user, upload_id=upload_id, for_update=True)
        if session.status == UploadSession.Status.UPLOADING:
            session.status = UploadSession.Status.CANCELED
            session.save(update_fields=["status", "updated_at"])
    cleanup_upload_session_files(session)
    return _serialize_upload_session(session)


def expire_upload_sessions(limit: int = 100) -> int:
    now = timezone.now()
    expired = list(
        UploadSession.objects.filter(status=UploadSession.Status.UPLOADING, expires_at__lte=now).order_by("expires_at", "id")[:limit]
    )
    for session in expired:
        session.status = UploadSession.Status.EXPIRED
        session.save(update_fields=["status", "updated_at"])
        cleanup_upload_session_files(session)
    return len(expired)


def store_generated_image(*, user_id: int, task_no: str, image_index: int, content: bytes, mime_type: str) -> dict:
    object_key = _object_key(user_id, task_no, image_index, mime_type)
    width, height = _image_dimensions(content)
    _write_storage_object(object_key=object_key, content=content, mime_type=mime_type)
    return {
        "image_index": image_index,
        "storage_provider": _storage_mode(),
        "bucket_name": settings.MINIO_BUCKET,
        "object_key": object_key,
        "mime_type": mime_type,
        "file_size": len(content),
        "width": width,
        "height": height,
        "sha256": hashlib.sha256(content).hexdigest(),
        "expires_at": timezone.now() + timedelta(days=settings.IMAGE_RETENTION_DAYS),
    }


def store_reference_image(*, user_id: int, task_no: str, content: bytes, mime_type: str) -> dict:
    object_key = _reference_object_key(user_id, task_no, mime_type)
    width, height = _image_dimensions(content)
    _write_storage_object(object_key=object_key, content=content, mime_type=mime_type)
    return {
        "reference_storage_provider": _storage_mode(),
        "reference_bucket_name": settings.MINIO_BUCKET,
        "reference_object_key": object_key,
        "reference_image_mime_type": mime_type,
        "reference_image_size": len(content),
        "reference_image_width": width,
        "reference_image_height": height,
        "reference_image_sha256": hashlib.sha256(content).hexdigest(),
    }


def expire_temporary_reference_images(limit: int = 100) -> int:
    expiry_seconds = _reference_upload_expiry_seconds()
    cutoff_timestamp = time.time() - expiry_seconds
    deleted = 0

    if _storage_mode() == "local":
        root = Path(settings.MINIO_LOCAL_ROOT) / "users"
        candidates = sorted(
            [path for path in root.glob("*/tasks/R*/reference*") if path.is_file()],
            key=lambda path: path.stat().st_mtime,
        )
        for path in candidates:
            if deleted >= limit:
                break
            if path.stat().st_mtime > cutoff_timestamp:
                continue
            object_key = path.relative_to(Path(settings.MINIO_LOCAL_ROOT)).as_posix()
            delete_storage_object(bucket_name=settings.MINIO_BUCKET, object_key=object_key)
            deleted += 1
        return deleted

    client = _minio_client()
    for obj in client.list_objects(settings.MINIO_BUCKET, prefix="users/", recursive=True):
        if deleted >= limit:
            break
        object_key = getattr(obj, "object_name", "")
        if not _is_temporary_reference_object_key(object_key):
            continue
        last_modified = getattr(obj, "last_modified", None)
        if not last_modified or last_modified.timestamp() > cutoff_timestamp:
            continue
        delete_storage_object(bucket_name=settings.MINIO_BUCKET, object_key=object_key)
        deleted += 1
    return deleted


def fetch_remote_image(url: str) -> tuple[bytes, str]:
    with _open_remote_image_stream(url) as response:
        response.raise_for_status()
        content_type = response.headers.get("Content-Type", "image/png").split(";", 1)[0].strip().lower()
        if not (content_type.startswith("image/") or content_type == "application/octet-stream"):
            raise ValueError("远程地址未返回图片内容")
        max_bytes = settings.MAX_REMOTE_IMAGE_BYTES
        chunks = []
        total = 0
        for chunk in response.iter_content(chunk_size=1024 * 1024):
            if not chunk:
                continue
            total += len(chunk)
            if total > max_bytes:
                raise ValueError("远程图片超过大小限制")
            chunks.append(chunk)
        return b"".join(chunks), content_type or "image/png"


def read_image_bytes(image: GeneratedImage) -> bytes:
    if image.status != GeneratedImage.Status.AVAILABLE:
        raise FileNotFoundError("图片已不可用")
    return read_storage_object(bucket_name=image.bucket_name, object_key=image.object_key)


def issue_download_url(*, image: GeneratedImage, user, inline: bool = False, preview: bool = False) -> str:
    signer = TimestampSigner()
    token = signer.sign(f"{image.id}:{user.id}")
    query = []
    if inline:
        query.append("disposition=inline")
    if preview:
        query.append("preview=1")
    suffix = f"?{'&'.join(query)}" if query else ""
    return f"/downloads/{quote(token)}/{suffix}"


def resolve_download_token(token: str, max_age: int = 300) -> GeneratedImage:
    signer = TimestampSigner()
    try:
        value = signer.unsign(token, max_age=max_age)
    except (BadSignature, SignatureExpired) as exc:
        raise Http404("下载链接已失效") from exc
    image_id, user_id = value.split(":")
    image = GeneratedImage.objects.filter(id=image_id, user_id=user_id, task__is_deleted=False).select_related("user", "task").first()
    if not image:
        raise Http404("图片不存在")
    return image


def _download_filename(image: GeneratedImage) -> str:
    prompt = re.sub(r"\s+", " ", (image.task.prompt or "").strip())
    prompt = re.sub(r'[\\/:*?"<>|]+', "", prompt)[:48].strip()
    stem = prompt or image.task.task_no
    extension = mimetypes.guess_extension(image.mime_type) or ".png"
    return f"{stem}{extension}"


def _build_preview_content(image: GeneratedImage, *, max_dimension: int = 768) -> tuple[bytes, str]:
    content = read_image_bytes(image)
    try:
        rendered = Image.open(io.BytesIO(content))
        rendered.load()
    except Exception:
        return content, image.mime_type

    width, height = rendered.size
    longest_edge = max(width, height)
    if longest_edge > max_dimension:
        rendered.thumbnail((max_dimension, max_dimension), Image.Resampling.LANCZOS)

    has_alpha = rendered.mode in {"RGBA", "LA"} or (rendered.mode == "P" and "transparency" in rendered.info)
    buffer = io.BytesIO()
    if has_alpha:
        rendered.save(buffer, format="PNG", optimize=True)
        return buffer.getvalue(), "image/png"

    rendered.convert("RGB").save(
        buffer,
        format="JPEG",
        quality=82,
        optimize=True,
        progressive=True,
    )
    return buffer.getvalue(), "image/jpeg"


def build_download_response(image: GeneratedImage, *, inline: bool = False, preview: bool = False) -> HttpResponse:
    content, content_type = _build_preview_content(image) if preview else (read_image_bytes(image), image.mime_type)
    response = HttpResponse(content, content_type=content_type)
    filename = _download_filename(image)
    disposition = "inline" if inline else "attachment"
    response["Content-Disposition"] = (
        f'{disposition}; filename="generated-image{mimetypes.guess_extension(content_type) or Path(filename).suffix or ".png"}"; '
        f"filename*=UTF-8''{quote(filename)}"
    )
    return response


def expire_image(image: GeneratedImage) -> None:
    if image.status != GeneratedImage.Status.AVAILABLE:
        return
    delete_storage_object(bucket_name=image.bucket_name, object_key=image.object_key)
    image.status = GeneratedImage.Status.EXPIRED
    image.deleted_at = timezone.now()
    image.save(update_fields=["status", "deleted_at", "updated_at"])


def expire_available_images(limit: int = 100) -> int:
    now = timezone.now()
    images = list(GeneratedImage.objects.filter(status=GeneratedImage.Status.AVAILABLE, expires_at__lt=now)[:limit])
    for image in images:
        expire_image(image)
    return len(images)
