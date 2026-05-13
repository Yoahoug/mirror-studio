from django.db import models


class TimeStampedModel(models.Model):
    created_at = models.DateTimeField("创建时间", auto_now_add=True)
    updated_at = models.DateTimeField("更新时间", auto_now=True)

    class Meta:
        abstract = True


class SoftDeleteModel(TimeStampedModel):
    deleted_at = models.DateTimeField("删除时间", blank=True, null=True)

    class Meta:
        abstract = True


from apps.core.siteconfig import SiteConfig  # noqa: E402, F401  — re-export for migration discovery

