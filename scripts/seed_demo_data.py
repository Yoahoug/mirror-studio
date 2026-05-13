import os
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

os.environ.setdefault("DJANGO_SETTINGS_MODULE", "config.settings.dev")

import django

django.setup()

from apps.generator.defaults import ensure_default_image_channel, ensure_default_llm_channel
from apps.users.models import User


def _get_env_value(*names: str) -> str:
    for name in names:
        value = (os.getenv(name) or "").strip()
        if value:
            return value
    return ""


def ensure_admin_user() -> None:
    username = _get_env_value("ADMIN_USERNAME", "INIT_ADMIN_USERNAME")
    password = _get_env_value("ADMIN_PASSWORD", "INIT_ADMIN_PASSWORD")
    email = _get_env_value("ADMIN_EMAIL", "INIT_ADMIN_EMAIL") or "admin@example.com"
    if not username or not password:
        print("未提供 ADMIN_USERNAME / ADMIN_PASSWORD，跳过管理员初始化")
        return

    user, created = User.objects.get_or_create(
        username=username,
        defaults={
            "email": email,
            "is_staff": True,
            "is_superuser": True,
            "role": User.Role.ADMIN,
        },
    )
    user.email = email
    user.is_staff = True
    user.is_superuser = True
    user.role = User.Role.ADMIN
    user.set_password(password)
    user.save()
    print(f"{'创建' if created else '更新'}管理员账号：{username}")


def main() -> None:
    ensure_admin_user()
    channel = ensure_default_image_channel()
    print(f"默认生图渠道已就绪：{channel.name} ({channel.channel_code})")
    llm_channel = ensure_default_llm_channel()
    if llm_channel:
        print(f"默认 LLM 渠道已就绪：{llm_channel.name} ({llm_channel.channel_code})")


if __name__ == "__main__":
    main()
