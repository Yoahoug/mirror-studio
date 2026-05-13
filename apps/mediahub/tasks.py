from .services import expire_available_images, expire_temporary_reference_images, expire_upload_sessions


def expire_images_task() -> int:
    return expire_available_images()


def expire_temporary_reference_images_task() -> int:
    return expire_temporary_reference_images()


def expire_upload_sessions_task() -> int:
    return expire_upload_sessions()
