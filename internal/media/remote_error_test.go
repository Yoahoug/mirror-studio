package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchRemoteImageClassifiesErrors(t *testing.T) {
	env := newTestEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)
	// The host is not on the allow list, so the guard resolves and passes it
	// (a public loopback address is still 127.0.0.1 -> guard rejects).
	_, _, err := env.service.FetchRemoteImage(context.Background(), server.URL+"/x.png")
	remote, ok := err.(*RemoteImageError)
	if !ok {
		t.Fatalf("error type = %T, want *RemoteImageError", err)
	}
	if remote.Kind != RemoteGuardError || remote.Retryable() {
		t.Fatalf("loopback fetch must be a non-retryable guard error, got %v", remote.Kind)
	}

	// An allow-listed host that answers 502 is an HTTPError and retryable,
	// which is what the reference's requests.HTTPError classification does.
	// The allow list compares bare hostnames, so the port is dropped.
	env.cfg.RemoteImageAllowedHosts = []string{"127.0.0.1"}
	_, _, err = env.service.FetchRemoteImage(context.Background(), server.URL+"/x.png")
	remote, ok = err.(*RemoteImageError)
	if !ok || remote.Kind != RemoteHTTPError || !remote.Retryable() {
		t.Fatalf("502 must be a retryable HTTPError, got %#v", err)
	}

	// A host that refuses the connection is a retryable ConnectionError.
	closed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := closed.URL
	closed.Close()
	_, _, err = env.service.FetchRemoteImage(context.Background(), closedURL+"/x.png")
	remote, ok = err.(*RemoteImageError)
	if !ok || remote.Kind != RemoteConnectionError || !remote.Retryable() {
		t.Fatalf("refused connection must be a retryable ConnectionError, got %#v", err)
	}
}
