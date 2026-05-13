from django.db import migrations, models


class Migration(migrations.Migration):
    dependencies = [
        ("generator", "0009_imagechannel_edit_api_path"),
    ]

    operations = [
        migrations.AddField(
            model_name="generatetask",
            name="reference_storage_provider",
            field=models.CharField(blank=True, default="", max_length=32, verbose_name="参考图存储提供方"),
        ),
        migrations.AddField(
            model_name="generatetask",
            name="reference_bucket_name",
            field=models.CharField(blank=True, default="", max_length=128, verbose_name="参考图桶名"),
        ),
        migrations.AddField(
            model_name="generatetask",
            name="reference_object_key",
            field=models.CharField(blank=True, default="", max_length=255, verbose_name="参考图对象 Key"),
        ),
        migrations.AddField(
            model_name="generatetask",
            name="reference_image_mime_type",
            field=models.CharField(blank=True, default="", max_length=64, verbose_name="参考图 MIME"),
        ),
        migrations.AddField(
            model_name="generatetask",
            name="reference_image_size",
            field=models.PositiveIntegerField(default=0, verbose_name="参考图大小"),
        ),
        migrations.AddField(
            model_name="generatetask",
            name="reference_image_width",
            field=models.PositiveIntegerField(default=0, verbose_name="参考图宽"),
        ),
        migrations.AddField(
            model_name="generatetask",
            name="reference_image_height",
            field=models.PositiveIntegerField(default=0, verbose_name="参考图高"),
        ),
        migrations.AddField(
            model_name="generatetask",
            name="reference_image_sha256",
            field=models.CharField(blank=True, default="", max_length=64, verbose_name="参考图 SHA256"),
        ),
    ]
