from django.urls import path

from .views import ReferenceUploadAPIView, TaskCancelAPIView, TaskDetailAPIView, TaskListCreateAPIView, TopicListCreateAPIView, WorkspaceHistoryAPIView

urlpatterns = [
    path("api/generate/topics", TopicListCreateAPIView.as_view(), name="api-generate-topics"),
    path("api/generate/reference-images", ReferenceUploadAPIView.as_view(), name="api-generate-reference-images"),
    path("api/generate/tasks", TaskListCreateAPIView.as_view(), name="api-generate-tasks"),
    path("api/workspace/history", WorkspaceHistoryAPIView.as_view(), name="api-workspace-history"),
    path("api/generate/tasks/<str:task_no>", TaskDetailAPIView.as_view(), name="api-generate-task-detail"),
    path("api/generate/tasks/<str:task_no>/cancel", TaskCancelAPIView.as_view(), name="api-generate-task-cancel"),
]
