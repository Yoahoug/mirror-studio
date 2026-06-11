from django.db import migrations, models


def disable_legacy_yunmeng_channels(apps, schema_editor):
    ImageChannel = apps.get_model("generator", "ImageChannel")
    legacy_channels = ImageChannel.objects.filter(channel_type="image").filter(
        models.Q(base_url__icontains="ymapi.wancores.com")
        | models.Q(api_path__icontains="/api/public/image/draw/")
    )
    legacy_channels.update(is_active=False)


def noop_reverse(apps, schema_editor):
    pass


class Migration(migrations.Migration):

    dependencies = [
        ("generator", "0011_generatetask_worker_fields"),
    ]

    operations = [
        migrations.AddField(
            model_name="generatetask",
            name="quality_code",
            field=models.CharField(default="auto", max_length=16, verbose_name="质量"),
        ),
        migrations.RunPython(disable_legacy_yunmeng_channels, noop_reverse),
    ]
