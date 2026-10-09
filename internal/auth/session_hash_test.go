package auth

import (
	"context"
	"net/http"
	"testing"
)

// TestPasswordChangeInvalidatesOtherSessions reproduces the Django
// _auth_user_hash contract: the session that performs the change survives, a
// session minted before it dies.
func TestPasswordChangeInvalidatesOtherSessions(t *testing.T) {
	env := newTestEnv(t, envOptions{registerEnabled: true, captchaEnabled: true})
	if _, err := env.svc.Register(context.Background(), "diff-user", "diff@example.com", "DiffPass123!@#", "127.0.0.1"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	// Session A logs in.
	code := env.captchaCode()
	if status, payload, _ := env.call(func() (*http.Response, map[string]any) {
		return env.postJSON("/api/auth/login", map[string]any{
			"username": "diff-user", "password": "DiffPass123!@#", "captcha_code": code,
		})
	}); status != http.StatusOK {
		t.Fatalf("login A: %d %s", status, messageOf(t, payload))
	}
	env.refreshCSRF()

	// Session B logs in from a separate client over the same database, drawing
	// its own session-scoped captcha code.
	envB := newTestEnvSharingDB(t, env)
	codeB := envB.captchaCode()
	if status, payload, _ := envB.call(func() (*http.Response, map[string]any) {
		return envB.postJSON("/api/auth/login", map[string]any{
			"username": "diff-user", "password": "DiffPass123!@#", "captcha_code": codeB,
		})
	}); status != http.StatusOK {
		t.Fatalf("login B: %d %s", status, messageOf(t, payload))
	}
	envB.refreshCSRF()

	// Session A changes the password.
	if status, payload, _ := env.call(func() (*http.Response, map[string]any) {
		return env.postJSON("/api/account/password", map[string]any{
			"current_password": "DiffPass123!@#", "new_password": "DiffPass456!@#", "confirm_password": "DiffPass456!@#",
		})
	}); status != http.StatusOK {
		t.Fatalf("password change: %d %s", status, messageOf(t, payload))
	}
	env.refreshCSRF()

	// Session A survives with a refreshed token; B is flushed to an anonymous
	// state, which the summary endpoint answers with the login redirect.
	env.refreshCSRF()
	if status, _, _ := env.call(func() (*http.Response, map[string]any) {
		response := env.get("/api/account/summary")
		return response, decodeEnvelope(t, response)
	}); status != http.StatusOK {
		t.Fatalf("session A did not survive the change (status %d)", status)
	}
	response := envB.get("/api/account/summary")
	response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("session B must be logged out after the change, status = %d", response.StatusCode)
	}
}

// TestSessionAuthHashChangesWithPassword checks the fingerprint's basic
// property: same hash in, same fingerprint; new hash, new fingerprint.
func TestSessionAuthHashChangesWithPassword(t *testing.T) {
	first := SessionAuthHash("argon2$one")
	if first != SessionAuthHash("argon2$one") {
		t.Fatal("fingerprint is not deterministic")
	}
	if first == SessionAuthHash("argon2$two") {
		t.Fatal("fingerprint did not change with the stored hash")
	}
	if len(first) != 64 {
		t.Fatalf("fingerprint length = %d, want 64 hex characters", len(first))
	}
}
