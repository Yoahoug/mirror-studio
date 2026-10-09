package media

import (
	"net/http"

	"mirror-studio/internal/httpapi"
)

// writeTechnicalHTML delegates to the one shared debug error page renderer, so
// media, web and console cannot drift apart on the URLconf listing.
func writeTechnicalHTML(w http.ResponseWriter, r *http.Request, status int, title string) {
	httpapi.WriteTechnicalHTML(w, r, status, title)
}

func writeNotFoundHTML(w http.ResponseWriter, r *http.Request) {
	writeTechnicalHTML(w, r, http.StatusNotFound, "Page not found")
}

func writeServerErrorHTML(w http.ResponseWriter, r *http.Request) {
	writeTechnicalHTML(w, r, http.StatusInternalServerError, "Server Error")
}

func writeBadRequestHTML(w http.ResponseWriter, r *http.Request) {
	writeTechnicalHTML(w, r, http.StatusBadRequest, "Bad Request")
}
