from django.db import migrations, models


class Migration(migrations.Migration):
    dependencies = [
        ("generator", "0003_imagechannel_provider_type"),
    ]

    operations = [
        migrations.AddField(
            model_name="generatetask",
            name="progress_percent",
            field=models.PositiveSmallIntegerField(default=0, verbose_name="进度"),
        ),
    ]
