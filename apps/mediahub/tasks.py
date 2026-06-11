from .services import (
    cleanup_media_storage,
    expire_available_images,
    expire_temporary_reference_images,
    expire_upload_sessions,
    purge_deleted_image_files,
)


def expire_images_task() -> int:
    return expire_available_images()


def expire_temporary_reference_images_task() -> int:
    return expire_temporary_reference_images()


def expire_upload_sessions_task() -> int:
    return expire_upload_sessions()


def purge_deleted_image_files_task() -> int:
    return purge_deleted_image_files()


def cleanup_media_storage_task() -> dict[str, int]:
    return cleanup_media_storage()
