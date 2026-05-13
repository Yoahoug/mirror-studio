from django.apps import AppConfig


class UsersConfig(AppConfig):
    default_auto_field = "django.db.models.BigAutoField"
    name = "apps.users"
    verbose_name = "用户"

    def ready(self) -> None:
        from . import signals  # noqa: F401

