from django.db.models.signals import post_save
from django.dispatch import receiver

from apps.billing.services import ensure_quota_account

from .models import User, UserProfile


@receiver(post_save, sender=User)
def create_user_profile(sender, instance: User, created: bool, **kwargs):
    if created:
        UserProfile.objects.get_or_create(
            user=instance,
            defaults={"nickname": instance.username},
        )
        ensure_quota_account(instance)
