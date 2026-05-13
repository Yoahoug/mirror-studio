from django.conf import settings
from django.db import migrations, models
import django.db.models.deletion
import django.utils.timezone


class Migration(migrations.Migration):
    initial = True

    dependencies = [
        migrations.swappable_dependency(settings.AUTH_USER_MODEL),
    ]

    operations = [
        migrations.CreateModel(
            name="QuotaAccount",
            fields=[
                ("id", models.BigAutoField(auto_created=True, primary_key=True, serialize=False, verbose_name="ID")),
                ("created_at", models.DateTimeField(auto_now_add=True, verbose_name="创建时间")),
                ("updated_at", models.DateTimeField(auto_now=True, verbose_name="更新时间")),
                ("daily_quota", models.PositiveIntegerField(default=3, verbose_name="每日免费次数")),
                ("available_quota", models.IntegerField(default=3, verbose_name="当前剩余次数")),
                ("frozen_quota", models.IntegerField(default=0, verbose_name="冻结次数")),
                ("total_generated_count", models.PositiveIntegerField(default=0, verbose_name="累计成功生成次数")),
                ("last_reset_date", models.DateField(default=django.utils.timezone.localdate, verbose_name="上次刷新日期")),
                (
                    "user",
                    models.OneToOneField(
                        on_delete=django.db.models.deletion.CASCADE,
                        related_name="quota_account",
                        to=settings.AUTH_USER_MODEL,
                        verbose_name="用户",
                    ),
                ),
            ],
            options={
                "verbose_name": "免费额度账户",
                "verbose_name_plural": "免费额度账户",
            },
        ),
        migrations.CreateModel(
            name="QuotaLedger",
            fields=[
                ("id", models.BigAutoField(auto_created=True, primary_key=True, serialize=False, verbose_name="ID")),
                ("created_at", models.DateTimeField(auto_now_add=True, verbose_name="创建时间")),
                ("updated_at", models.DateTimeField(auto_now=True, verbose_name="更新时间")),
                ("ledger_no", models.CharField(max_length=32, unique=True, verbose_name="流水号")),
                (
                    "change_type",
                    models.CharField(
                        choices=[
                            ("reset", "每日刷新"),
                            ("freeze", "冻结"),
                            ("consume", "消耗"),
                            ("unfreeze", "解冻"),
                            ("admin_adjust", "管理员调整"),
                            ("daily_quota_update", "每日额度调整"),
                        ],
                        max_length=32,
                        verbose_name="变更类型",
                    ),
                ),
                (
                    "direction",
                    models.CharField(
                        choices=[("in", "增加"), ("out", "减少"), ("none", "无变化")],
                        default="none",
                        max_length=8,
                        verbose_name="方向",
                    ),
                ),
                ("amount", models.IntegerField(default=0, verbose_name="变更数量")),
                ("before_available", models.IntegerField(default=0, verbose_name="变更前剩余次数")),
                ("after_available", models.IntegerField(default=0, verbose_name="变更后剩余次数")),
                ("before_frozen", models.IntegerField(default=0, verbose_name="变更前冻结次数")),
                ("after_frozen", models.IntegerField(default=0, verbose_name="变更后冻结次数")),
                ("related_task_no", models.CharField(blank=True, max_length=32, verbose_name="关联任务号")),
                ("remark", models.CharField(blank=True, max_length=255, verbose_name="备注")),
                (
                    "user",
                    models.ForeignKey(
                        on_delete=django.db.models.deletion.CASCADE,
                        related_name="quota_ledgers",
                        to=settings.AUTH_USER_MODEL,
                        verbose_name="用户",
                    ),
                ),
            ],
            options={
                "verbose_name": "免费额度流水",
                "verbose_name_plural": "免费额度流水",
                "ordering": ["-created_at", "-id"],
            },
        ),
        migrations.AddIndex(
            model_name="quotaledger",
            index=models.Index(fields=["user", "-created_at"], name="billing_quo_user_id_191d5d_idx"),
        ),
    ]
