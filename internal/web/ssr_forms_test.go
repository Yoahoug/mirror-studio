package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Contract §1: "/login | GET | 200；已登录 302 → /workspace" and the same for
// /register. Both pages used to render their form for an authenticated visitor.
func TestLoginAndRegisterRedirectAuthenticatedVisitors(t *testing.T) {
	h := newPageHarness(t, harnessOptions{users: []*fakeUser{diffUser()}, sessionUser: 3})
	for _, path := range []string{"/login", "/register"} {
		response := h.get(path)
		if response.status != http.StatusFound {
			t.Fatalf("%s status = %d, want 302", path, response.status)
		}
		if location := response.headers.Get("Location"); location != "/workspace" {
			t.Fatalf("%s Location = %q, want /workspace", path, location)
		}
	}
}

// postForm gets the page first so the CSRF cookie and the embedded token match,
// the way a browser submits a server-rendered form.
func postForm(h *pageHarness, path string, form url.Values) pageResponse {
	page := h.get(path)
	form.Set("csrfmiddlewaretoken", csrfTokenOfPage(h.t, page.body))
	return h.request(http.MethodPost, path, form)
}

// The SSR register form has to verify the emailed code, exactly like
// POST /api/auth/register: without it any visitor could register an arbitrary
// address without ever receiving the mail.
func TestSSRRegisterVerifiesTheEmailCode(t *testing.T) {
	h := newPageHarness(t, harnessOptions{emailVerification: true, users: []*fakeUser{diffUser()}})
	h.service.auth.(*fakeAuth).emailCodeCheck = func(_, code string) error {
		if code != "123456" {
			return errors.New("验证码不存在或已过期")
		}
		return nil
	}

	rejected := postForm(h, "/register", url.Values{
		"username":                {"newcomer"},
		"email":                   {"newcomer@example.com"},
		"email_verification_code": {"000000"},
		"password":                {"StrongPass123!@#"},
		"confirm_password":        {"StrongPass123!@#"},
	})
	if rejected.status != http.StatusOK || !strings.Contains(rejected.body, "验证码不存在或已过期") {
		t.Fatalf("bad code must re-render with the verification error, got %d", rejected.status)
	}

	accepted := postForm(h, "/register", url.Values{
		"username":                {"newcomer"},
		"email":                   {"newcomer@example.com"},
		"email_verification_code": {"123456"},
		"password":                {"StrongPass123!@#"},
		"confirm_password":        {"StrongPass123!@#"},
	})
	if accepted.status != http.StatusFound {
		t.Fatalf("valid code status = %d, want the /workspace redirect", accepted.status)
	}
}

// The password policy is part of the frozen error set (§0.4) and the API
// enforces it on register and reset; both SSR forms used to accept "1".
func TestSSRFormsEnforceThePasswordPolicy(t *testing.T) {
	register := newPageHarness(t, harnessOptions{users: []*fakeUser{diffUser()}})
	response := postForm(register, "/register", url.Values{
		"username":         {"newcomer"},
		"email":            {"newcomer@example.com"},
		"password":         {"1"},
		"confirm_password": {"1"},
	})
	if response.status != http.StatusOK || !strings.Contains(response.body, "This password is too short") {
		t.Fatalf("weak register password accepted: status %d", response.status)
	}

	reset := newPageHarness(t, harnessOptions{users: []*fakeUser{diffUser()}})
	reset.config.LoginCaptchaEnabled = false
	response = postForm(reset, "/forgot-password", url.Values{
		"username":         {"diff-user"},
		"email":            {"diff@example.com"},
		"new_password":     {"1"},
		"confirm_password": {"1"},
		// The field is required regardless of the captcha setting, so a blank
		// one would fail validation before the password policy is reached.
		"captcha_code": {"abcd"},
	})
	if response.status != http.StatusOK || !strings.Contains(response.body, "This password is too short") {
		t.Fatalf("weak reset password accepted: status %d", response.status)
	}
}

// Browsers normalize a backslash to a slash, so "/\evil.com" is a
// protocol-relative URL in disguise; the login redirect must refuse it.
func TestAllowedNextRejectsBackslashTargets(t *testing.T) {
	for _, target := range []string{"/\\evil.com", "\\/evil.com", "/workspace\\..\\.."} {
		if allowedNext(target) {
			t.Fatalf("allowedNext(%q) = true, want false", target)
		}
	}
	for _, target := range []string{"/workspace", "/account/history", "/console/"} {
		if !allowedNext(target) {
			t.Fatalf("allowedNext(%q) = false, want true", target)
		}
	}
}

// The SSR login form used to authenticate on correct credentials even when the
// captcha was wrong or missing: the field error was collected but nothing gated
// the sign-in on it, so the login captcha could be bypassed by submitting an
// empty field. The reference only reaches form_valid() through is_valid(), and
// the API path answers 400 for the same payload.
func TestSSRLoginRejectsAnInvalidCaptcha(t *testing.T) {
	for _, submitted := range []string{"", "wrong-code"} {
		h, risk := loginRiskHarness(t)
		page := h.get("/login")
		response := h.request(http.MethodPost, "/login", url.Values{
			"csrfmiddlewaretoken": {csrfTokenOfPage(t, page.body)},
			"username":            {"diff-user"},
			"password":            {"DiffAdmin123!@#"},
			"captcha_code":        {submitted},
		})
		if response.status != http.StatusOK {
			t.Fatalf("captcha %q: status = %d, want a 200 re-render", submitted, response.status)
		}
		if len(risk.cleared) != 0 {
			t.Fatalf("captcha %q: the session was signed in anyway", submitted)
		}
		if len(risk.failures) != 1 {
			t.Fatalf("captcha %q: failures = %v, want one recorded failure", submitted, risk.failures)
		}
	}
}
