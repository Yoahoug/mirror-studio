from django.urls import path

from .views import (
    NovelBookListAPIView,
    NovelBookUploadAPIView,
    NovelSceneImageView,
    NovelTaskDetailAPIView,
    NovelTaskEventsView,
    NovelTaskListCreateAPIView,
    NovelTaskRetryAPIView,
)

urlpatterns = [
    path("api/novelviz/books", NovelBookListAPIView.as_view(), name="api-novelviz-books"),
    path("api/novelviz/books/upload", NovelBookUploadAPIView.as_view(), name="api-novelviz-books-upload"),
    path("api/novelviz/tasks", NovelTaskListCreateAPIView.as_view(), name="api-novelviz-tasks"),
    path("api/novelviz/tasks/<int:task_id>", NovelTaskDetailAPIView.as_view(), name="api-novelviz-task-detail"),
    path("api/novelviz/tasks/<int:task_id>/retry", NovelTaskRetryAPIView.as_view(), name="api-novelviz-task-retry"),
    path("api/novelviz/tasks/<int:task_id>/events", NovelTaskEventsView.as_view(), name="api-novelviz-task-events"),
    path("api/novelviz/tasks/<int:task_id>/images/<int:image_id>.png", NovelSceneImageView.as_view(), name="api-novelviz-scene-image"),
]
