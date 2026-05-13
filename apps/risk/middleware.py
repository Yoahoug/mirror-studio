from django.http import HttpResponseForbidden

from .services import ensure_ip_allowed


class IPBlockMiddleware:
    def __init__(self, get_response):
        self.get_response = get_response

    def __call__(self, request):
        try:
            ensure_ip_allowed(getattr(request, "client_ip", ""))
        except PermissionError as exc:
            return HttpResponseForbidden(str(exc))
        return self.get_response(request)

