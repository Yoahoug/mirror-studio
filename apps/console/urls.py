from django.urls import path

from .views import (
    ConsoleConfigBatchUpdateAPI,
    ConsoleConfigDetailAPI,
    ConsoleConfigListAPI,
    ConsoleDashboardAPI,
    ConsoleImageChannelDetailAPI,
    ConsoleImageChannelListAPI,
    ConsoleTaskListAPI,
    ConsoleUserDetailAPI,
    ConsoleUserListAPI,
    ConsoleUserQuotaUpdateAPI,
)

urlpatterns = [
    path("console/api/dashboard/", ConsoleDashboardAPI.as_view(), name="console-api-dashboard"),
    path("console/api/config/", ConsoleConfigListAPI.as_view(), name="console-api-config-list"),
    path("console/api/config/batch/", ConsoleConfigBatchUpdateAPI.as_view(), name="console-api-config-batch"),
    path("console/api/config/<str:key>/", ConsoleConfigDetailAPI.as_view(), name="console-api-config-detail"),
    path("console/api/image-channels/", ConsoleImageChannelListAPI.as_view(), name="console-api-image-channels"),
    path("console/api/image-channels/<int:pk>/", ConsoleImageChannelDetailAPI.as_view(), name="console-api-image-channel-detail"),
    path("console/api/users/", ConsoleUserListAPI.as_view(), name="console-api-users"),
    path("console/api/users/<int:pk>/", ConsoleUserDetailAPI.as_view(), name="console-api-user-detail"),
    path("console/api/users/<int:pk>/quota/", ConsoleUserQuotaUpdateAPI.as_view(), name="console-api-user-quota-update"),
    path("console/api/tasks/", ConsoleTaskListAPI.as_view(), name="console-api-tasks"),
]
