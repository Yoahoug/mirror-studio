from django.db import migrations, models


class Migration(migrations.Migration):

    dependencies = [
        ("generator", "0002_initial"),
    ]

    operations = [
        migrations.AddField(
            model_name="imagechannel",
            name="channel_type",
            field=models.CharField(
                choices=[("image", "生图模型"), ("llm", "LLM模型")],
                default="image",
                max_length=16,
                verbose_name="模型类型",
            ),
        ),
        migrations.AddField(
            model_name="imagechannel",
            name="provider_name",
            field=models.CharField(blank=True, default="", max_length=64, verbose_name="对接方"),
        ),
    ]
