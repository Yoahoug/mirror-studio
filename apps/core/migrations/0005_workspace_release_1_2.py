from django.db import migrations


RELEASE_CONFIG = {
    "site.workspace_release_version": "v1.2",
    "site.workspace_release_title": "绘镜工作台 1.2",
    "site.workspace_release_notes": (
        "生图工作台支持 1K / 2K、预设与自定义比例、质量选择\n"
        "移除废弃云梦渠道，统一走 OpenAI-compatible 中转\n"
        "优化小说日志、上传与媒体清理，降低内存和存储占用"
    ),
    "site.workspace_release_published_at": "2026-06-12",
}


def publish_workspace_release(apps, _schema_editor):
    SiteConfig = apps.get_model("core", "SiteConfig")
    for key, value in RELEASE_CONFIG.items():
        SiteConfig.objects.update_or_create(
            key=key,
            defaults={
                "value_encrypted": value,
                "is_secret": False,
                "category": "site",
            },
        )


class Migration(migrations.Migration):
    dependencies = [
        ("core", "0004_siteconfig_chat_llm_category"),
    ]

    operations = [
        migrations.RunPython(publish_workspace_release, migrations.RunPython.noop),
    ]
