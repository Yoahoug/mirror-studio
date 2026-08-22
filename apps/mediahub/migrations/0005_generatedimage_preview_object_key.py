from django.db import migrations, models


class Migration(migrations.Migration):

    dependencies = [
        ("mediahub", "0004_generatedimage_status_deleted_idx"),
    ]

    operations = [
        migrations.AddField(
            model_name="generatedimage",
            name="preview_object_key",
            field=models.CharField(blank=True, default="", max_length=255, verbose_name="预览图对象 Key"),
        ),
    ]
