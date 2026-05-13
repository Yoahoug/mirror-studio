from django.db import migrations, models


class Migration(migrations.Migration):
    dependencies = [
        ("generator", "0010_generatetask_reference_storage"),
    ]

    operations = [
        migrations.AddField(
            model_name="generatetask",
            name="execution_attempts",
            field=models.PositiveIntegerField(default=0, verbose_name="执行尝试次数"),
        ),
        migrations.AddField(
            model_name="generatetask",
            name="lease_expires_at",
            field=models.DateTimeField(blank=True, null=True, verbose_name="执行租约过期时间"),
        ),
    ]
