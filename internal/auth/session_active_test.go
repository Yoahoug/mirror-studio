package auth

import (
	"context"
	"net/http"
	"testing"
)

// Django resolves request.user through ModelBackend.get_user, which returns
// None when user_can_authenticate() is false — so deactivating an account
// (is_active = 0) ends its existing sessions, not just new logins. The Go
// session path only looked at the custom status column, so a deactivated
// account kept full API access through any session it already had.
func TestDeactivatedAccountLosesItsSession(t *testing.T) {
	env := newTestEnv(t, envOptions{registerEnabled: true, captchaEnabled: false})
	user, err := env.svc.Register(context.Background(), "diff-user", "diff@example.com", "DiffPass123!@#", "127.0.0.1")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if status, payload, _ := env.call(func() (*http.Response, map[string]any) {
		return env.postJSON("/api/auth/login", map[string]any{
			"username": "diff-user", "password": "DiffPass123!@#", "captcha_code": "abcd",
		})
	}); status != http.StatusOK {
		t.Fatalf("login: %d %s", status, messageOf(t, payload))
	}

	response := env.get("/api/account/summary")
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("active session status = %d, want 200", response.StatusCode)
	}

	if _, err := env.db.Exec(`UPDATE users_user SET is_active = 0 WHERE id = ?`, user.ID()); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	response = env.get("/api/account/summary")
	response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("deactivated session status = %d, want the login redirect 302", response.StatusCode)
	}
}
