from django.contrib import admin

from .models import User, UserProfile


@admin.register(User)
class UserAdmin(admin.ModelAdmin):
    list_display = ("id", "username", "email", "role", "status", "is_staff", "date_joined")
    list_filter = ("role", "status", "is_staff", "is_superuser")
    search_fields = ("username", "email", "mobile")


@admin.register(UserProfile)
class UserProfileAdmin(admin.ModelAdmin):
    list_display = ("user", "nickname", "register_ip", "register_source", "created_at")
    search_fields = ("user__username", "nickname")

