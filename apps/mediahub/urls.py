from django.urls import path

from .views import (
    HistoryDetailPageView,
    HistoryPageView,
    ImageDetailAPIView,
    ImageDownloadUrlAPIView,
    ImageListAPIView,
    SignedDownloadView,
    UploadChunkAPIView,
    UploadSessionCompleteAPIView,
    UploadSessionDetailAPIView,
    UploadSessionListCreateAPIView,
)

urlpatterns = [
    path("account/history", HistoryPageView.as_view(), name="account-history"),
    path("account/history/<str:task_no>", HistoryDetailPageView.as_view(), name="account-history-detail"),
    path("api/images", ImageListAPIView.as_view(), name="api-images"),
    path("api/images/<int:image_id>", ImageDetailAPIView.as_view(), name="api-image-detail"),
    path("api/images/<int:image_id>/download-url", ImageDownloadUrlAPIView.as_view(), name="api-image-download"),
    path("api/uploads/sessions", UploadSessionListCreateAPIView.as_view(), name="api-upload-sessions"),
    path("api/uploads/sessions/<str:upload_id>", UploadSessionDetailAPIView.as_view(), name="api-upload-session-detail"),
    path("api/uploads/sessions/<str:upload_id>/chunks/<int:index>", UploadChunkAPIView.as_view(), name="api-upload-session-chunk"),
    path("api/uploads/sessions/<str:upload_id>/complete", UploadSessionCompleteAPIView.as_view(), name="api-upload-session-complete"),
    path("downloads/<path:token>/", SignedDownloadView.as_view(), name="image-download"),
]
