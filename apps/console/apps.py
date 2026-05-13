from django.apps import AppConfig


class ConsoleConfig(AppConfig):
    default_auto_field = "django.db.models.BigAutoField"
    name = "apps.console"
    verbose_name = "控制台"

    def ready(self) -> None:
        from django.contrib import admin

        admin.site.site_header = "绘镜管理后台"
        admin.site.site_title = "绘镜管理后台"
        admin.site.index_title = "绘镜控制台"
