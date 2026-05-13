from django.db import migrations, models


class Migration(migrations.Migration):
    dependencies = [
        ("generator", "0008_generatetask_reference_mode"),
    ]

    operations = [
        migrations.AddField(
            model_name="imagechannel",
            name="edit_api_path",
            field=models.CharField(blank=True, default="", max_length=128, verbose_name="图生图接口路径"),
        ),
    ]
