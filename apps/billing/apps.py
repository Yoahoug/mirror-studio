from django.apps import AppConfig


class BillingConfig(AppConfig):
    default_auto_field = "django.db.models.BigAutoField"
    name = "apps.billing"
    verbose_name = "免费额度"

    def ready(self) -> None:
        from . import signals  # noqa: F401
