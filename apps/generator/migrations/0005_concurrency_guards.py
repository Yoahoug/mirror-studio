from django.db import migrations, models


class Migration(migrations.Migration):
    dependencies = [
        ("generator", "0004_generatetask_progress_percent"),
    ]

    operations = [
        migrations.AddIndex(
            model_name="generatetask",
            index=models.Index(fields=["user", "status", "created_at"], name="generator_g_user_id_9de8f4_idx"),
        ),
        migrations.AddConstraint(
            model_name="generatetask",
            constraint=models.UniqueConstraint(
                condition=~models.Q(client_request_id=""),
                fields=("user", "client_request_id"),
                name="unique_nonblank_client_request_per_user",
            ),
        ),
    ]
