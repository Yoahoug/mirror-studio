package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func csrfOriginRequest(method, origin, referer string, secure bool) *http.Request {
	target := "http://test/api/x"
	if secure {
		target = "https://test/api/x"
	}
	request := httptest.NewRequest(method, target, nil)
	request.Host = "test"
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if referer != "" {
		request.Header.Set("Referer", referer)
	}
	if secure {
		request.URL.Scheme = "https"
		request.TLS = nil
	}
	return request
}

func TestVerifyCSRFOriginMatchesOwnHost(t *testing.T) {
	// Over plain HTTP an Origin equal to http://host passes.
	if !VerifyCSRFOrigin(csrfOriginRequest(http.MethodPost, "http://test", "", false), nil, false) {
		t.Fatal("own-host origin over http must pass")
	}
	// Over https the good origin carries the https scheme.
	if !VerifyCSRFOrigin(csrfOriginRequest(http.MethodPost, "https://test", "", true), nil, true) {
		t.Fatal("own-host origin over https must pass")
	}
	// A mismatched scheme is rejected, exactly like Django's good_origin check.
	if VerifyCSRFOrigin(csrfOriginRequest(http.MethodPost, "http://test", "", true), nil, true) {
		t.Fatal("http origin on an https request must be rejected")
	}
	// A cross-site origin is rejected even over http.
	if VerifyCSRFOrigin(csrfOriginRequest(http.MethodPost, "http://evil.example", "", false), nil, false) {
		t.Fatal("cross-site origin must be rejected")
	}
}

func TestVerifyCSRFOriginTrustedList(t *testing.T) {
	trusted := []string{"https://mirror.example.com"}
	if !VerifyCSRFOrigin(csrfOriginRequest(http.MethodPost, "https://mirror.example.com", "", false), trusted, false) {
		t.Fatal("a trusted origin must pass")
	}
	if VerifyCSRFOrigin(csrfOriginRequest(http.MethodPost, "https://other.example.com", "", false), trusted, false) {
		t.Fatal("an unlisted origin must not pass")
	}
}

func TestVerifyCSRFOriginSecureWithoutOriginNeedsReferer(t *testing.T) {
	// https request, no Origin: a same-host Referer passes, anything else fails.
	if !VerifyCSRFOrigin(csrfOriginRequest(http.MethodPost, "", "https://test/page", true), nil, true) {
		t.Fatal("same-host referer must pass")
	}
	if VerifyCSRFOrigin(csrfOriginRequest(http.MethodPost, "", "https://evil.example/page", true), nil, true) {
		t.Fatal("cross-host referer must be rejected")
	}
	if VerifyCSRFOrigin(csrfOriginRequest(http.MethodPost, "", "", true), nil, true) {
		t.Fatal("a secure request without Origin and Referer must be rejected")
	}
	// Over plain http the missing-Origin branch does not require a Referer.
	if !VerifyCSRFOrigin(csrfOriginRequest(http.MethodPost, "", "", false), nil, false) {
		t.Fatal("http request without Origin needs no Referer")
	}
}
