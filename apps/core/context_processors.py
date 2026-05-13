from django.conf import settings


def site_context(_request):
    return {
        "SITE_NAME": settings.SITE_NAME,
        "BRAND_NAME": settings.BRAND_NAME,
        "BRAND_ENGLISH_NAME": settings.BRAND_ENGLISH_NAME,
        "BRAND_TAGLINE": settings.BRAND_TAGLINE,
        "WORKSPACE_VITE_DEV_URL": settings.WORKSPACE_VITE_DEV_URL,
    }
