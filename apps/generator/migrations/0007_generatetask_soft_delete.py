from django.db import migrations, models


class Migration(migrations.Migration):
    dependencies = [
        ("generator", "0006_sync_model_meta"),
    ]

    operations = [
        migrations.AddField(
            model_name="generatetask",
            name="deleted_at",
            field=models.DateTimeField(blank=True, null=True, verbose_name="删除时间"),
        ),
        migrations.AddField(
            model_name="generatetask",
            name="is_deleted",
            field=models.BooleanField(default=False, verbose_name="用户已删除"),
        ),
        migrations.AddIndex(
            model_name="generatetask",
            index=models.Index(fields=["user", "is_deleted", "-created_at"], name="generator_g_user_id_2e4d8a_idx"),
        ),
    ]
