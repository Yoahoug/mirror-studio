from __future__ import annotations

from django.contrib.auth.mixins import LoginRequiredMixin
from django.http import Http404
from django.shortcuts import get_object_or_404
from django.views import View
from django.views.generic import TemplateView

from apps.core.api import InvalidPayloadError, api_error, api_success, parse_request_payload

from .models import GeneratedImage
from .services import (
    build_download_response,
    cancel_upload_session,
    complete_upload_session,
    create_or_resume_upload_session,
    issue_download_url,
    resolve_download_token,
    serialize_upload_session_for_user,
    store_upload_chunk,
)


def serialize_image(image: GeneratedImage) -> dict:
    return {
        "id": image.id,
        "task_no": image.task.task_no,
        "prompt": image.task.prompt,
        "status": image.status,
        "width": image.width,
        "height": image.height,
        "file_size": image.file_size,
        "created_at": image.created_at.isoformat(),
        "expires_at": image.expires_at.isoformat() if image.expires_at else None,
    }


class HistoryPageView(LoginRequiredMixin, TemplateView):
    template_name = "account/history.html"

    def get_context_data(self, **kwargs):
        context = super().get_context_data(**kwargs)
        context["images"] = GeneratedImage.objects.filter(user=self.request.user, task__is_deleted=False).select_related("task")[:50]
        return context


class HistoryDetailPageView(LoginRequiredMixin, TemplateView):
    template_name = "account/history_detail.html"

    def get_context_data(self, **kwargs):
        context = super().get_context_data(**kwargs)
        task_no = self.kwargs["task_no"]
        images = GeneratedImage.objects.filter(user=self.request.user, task__task_no=task_no, task__is_deleted=False).select_related("task")
        task = images.first().task if images.exists() else None
        if not task:
            raise Http404("任务不存在")
        context["task"] = task
        context["images"] = images
        return context


class ImageListAPIView(LoginRequiredMixin, View):
    def get(self, request):
        queryset = GeneratedImage.objects.filter(user=request.user, task__is_deleted=False).select_related("task")
        status = request.GET.get("status")
        keyword = request.GET.get("keyword")
        if status:
            queryset = queryset.filter(status=status)
        if keyword:
            queryset = queryset.filter(task__prompt__icontains=keyword)
        images = list(queryset[:100])
        return api_success([serialize_image(image) for image in images])


class ImageDetailAPIView(LoginRequiredMixin, View):
    def get(self, request, image_id: int):
        image = get_object_or_404(GeneratedImage.objects.filter(user=request.user, task__is_deleted=False).select_related("task"), id=image_id)
        return api_success(serialize_image(image))


class ImageDownloadUrlAPIView(LoginRequiredMixin, View):
    def post(self, request, image_id: int):
        image = get_object_or_404(GeneratedImage.objects.filter(user=request.user, task__is_deleted=False), id=image_id)
        if image.status != GeneratedImage.Status.AVAILABLE:
            return api_error("图片已过期或不可下载", code=400404, status=404)
        return api_success(
            {
                "url": issue_download_url(image=image, user=request.user),
                "preview_url": issue_download_url(image=image, user=request.user, inline=True, preview=True),
            }
        )


class UploadSessionListCreateAPIView(LoginRequiredMixin, View):
    def post(self, request):
        try:
            payload = parse_request_payload(request)
            data = create_or_resume_upload_session(
                user=request.user,
                kind=payload.get("kind", ""),
                filename=payload.get("filename", ""),
                content_type=payload.get("content_type", ""),
                total_size=payload.get("total_size", 0),
                client_upload_id=payload.get("client_upload_id", ""),
            )
        except InvalidPayloadError as exc:
            return api_error(str(exc), code=400001)
        except ValueError as exc:
            return api_error(str(exc), code=400100)
        return api_success(data, status=201)


class UploadSessionDetailAPIView(LoginRequiredMixin, View):
    def get(self, request, upload_id: str):
        try:
            return api_success(serialize_upload_session_for_user(user=request.user, upload_id=upload_id))
        except FileNotFoundError:
            raise Http404("上传会话不存在")

    def delete(self, request, upload_id: str):
        try:
            return api_success(cancel_upload_session(user=request.user, upload_id=upload_id))
        except FileNotFoundError:
            raise Http404("上传会话不存在")
        except ValueError as exc:
            return api_error(str(exc), code=400100)


class UploadChunkAPIView(LoginRequiredMixin, View):
    def put(self, request, upload_id: str, index: int):
        try:
            data = store_upload_chunk(
                user=request.user,
                upload_id=upload_id,
                index=index,
                content=request.body,
            )
        except FileNotFoundError:
            raise Http404("上传会话不存在")
        except ValueError as exc:
            return api_error(str(exc), code=400100)
        return api_success(data)


class UploadSessionCompleteAPIView(LoginRequiredMixin, View):
    def post(self, request, upload_id: str):
        try:
            return api_success(complete_upload_session(user=request.user, upload_id=upload_id))
        except FileNotFoundError:
            raise Http404("上传会话不存在")
        except PermissionError as exc:
            return api_error(str(exc), code=400403, status=403)
        except ValueError as exc:
            return api_error(str(exc), code=400100)


class SignedDownloadView(View):
    def get(self, request, token: str):
        image = resolve_download_token(token)
        return build_download_response(
            image,
            inline=request.GET.get("disposition") == "inline",
            preview=request.GET.get("preview") == "1",
        )
