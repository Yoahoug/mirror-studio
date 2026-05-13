from django.db import migrations, models


class Migration(migrations.Migration):
    dependencies = [
        ("generator", "0007_generatetask_soft_delete"),
    ]

    operations = [
        migrations.AddField(
            model_name="generatetask",
            name="generation_mode",
            field=models.CharField(
                choices=[("text_to_image", "文生图"), ("image_to_image", "图生图")],
                default="text_to_image",
                max_length=24,
                verbose_name="生成模式",
            ),
        ),
        migrations.AddField(
            model_name="generatetask",
            name="reference_image_data_url",
            field=models.TextField(blank=True, verbose_name="参考图 Data URL"),
        ),
        migrations.AddField(
            model_name="generatetask",
            name="reference_image_name",
            field=models.CharField(blank=True, max_length=255, verbose_name="参考图文件名"),
        ),
    ]
