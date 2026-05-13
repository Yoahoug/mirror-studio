from django.db import migrations, models


class Migration(migrations.Migration):
    initial = True

    dependencies = []

    operations = [
        migrations.CreateModel(
            name="SiteConfig",
            fields=[
                ("id", models.BigAutoField(auto_created=True, primary_key=True, serialize=False, verbose_name="ID")),
                ("created_at", models.DateTimeField(auto_now_add=True, verbose_name="创建时间")),
                ("updated_at", models.DateTimeField(auto_now=True, verbose_name="更新时间")),
                ("key", models.CharField(db_index=True, max_length=128, unique=True, verbose_name="配置键")),
                ("value_encrypted", models.TextField(blank=True, default="", verbose_name="加密值")),
                ("is_secret", models.BooleanField(default=False, verbose_name="敏感项")),
                ("description", models.CharField(blank=True, default="", max_length=255, verbose_name="描述")),
                (
                    "category",
                    models.CharField(
                        choices=[
                            ("general", "通用"),
                            ("site", "站点"),
                            ("email", "邮件验证"),
                            ("risk", "风控"),
                        ],
                        default="general",
                        max_length=32,
                        verbose_name="分类",
                    ),
                ),
            ],
            options={
                "verbose_name": "系统配置",
                "verbose_name_plural": "系统配置",
                "ordering": ["category", "key"],
            },
        ),
    ]
