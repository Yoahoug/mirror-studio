package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// fakeRisk records the risk calls the SSR login submit makes, mirroring what
// the reference's LoginPageView does through apps.risk.services.
type fakeRisk struct {
	allowed  map[string]error
	failures []string
	cleared  []string
}

func (r *fakeRisk) EnsureLoginAllowed(_ context.Context, username, _ string) error {
	if err, ok := r.allowed[username]; ok {
		return err
	}
	return nil
}

func (r *fakeRisk) RecordLoginFailure(_ context.Context, username, _ string) int64 {
	r.failures = append(r.failures, username)
	return int64(len(r.failures))
}

func (r *fakeRisk) ClearLoginFailures(_ context.Context, username, _ string) {
	r.cleared = append(r.cleared, username)
}

// loginRiskHarness boots the page harness with a risk gateway wired in and the
// corpus user present, so a correct password authenticates.
func loginRiskHarness(t *testing.T) (*pageHarness, *fakeRisk) {
	t.Helper()
	risk := &fakeRisk{allowed: map[string]error{}}
	harness := newPageHarnessWithRisk(t, harnessOptions{users: []*fakeUser{diffUser()}}, risk)
	return harness, risk
}

// postLoginForm submits the SSR login form the way a browser does: the CSRF
// cookie the GET minted plus the csrfmiddlewaretoken the page embedded.
func postLoginForm(h *pageHarness, form url.Values) pageResponse {
	page := h.get("/login")
	form.Set("csrfmiddlewaretoken", csrfTokenOfPage(h.t, page.body))
	return h.request(http.MethodPost, "/login", form)
}

func TestSSRLoginRecordsFailureAndClearsOnSuccess(t *testing.T) {
	h, risk := loginRiskHarness(t)
	// A wrong password must record one failure for the submitted username.
	response := postLoginForm(h, url.Values{
		"username":     {"diff-user"},
		"password":     {"wrong"},
		"captcha_code": {""},
	})
	if response.status != http.StatusOK {
		t.Fatalf("wrong-password login should re-render 200, got %d", response.status)
	}
	if len(risk.failures) != 1 || risk.failures[0] != "diff-user" {
		t.Fatalf("failure not recorded: %v", risk.failures)
	}

	// The correct password must clear the counter.
	postLoginForm(h, url.Values{
		"username":     {"diff-user"},
		"password":     {"DiffAdmin123!@#"},
		"captcha_code": {""},
	})
	if len(risk.cleared) != 1 || risk.cleared[0] != "diff-user" {
		t.Fatalf("success did not clear failures: %v", risk.cleared)
	}
}

func TestSSRLoginBlockedByLockoutShowsMessage(t *testing.T) {
	h, risk := loginRiskHarness(t)
	risk.allowed["diff-user"] = errors.New("登录失败次数过多，请 15 分钟后再试")
	response := postLoginForm(h, url.Values{
		"username":     {"diff-user"},
		"password":     {"DiffAdmin123!@#"},
		"captcha_code": {""},
	})
	// The credentials are correct but the pre-check refuses: no redirect, the
	// reference's form.add_error(None, str(exc)) text.
	if response.status != http.StatusOK {
		t.Fatalf("locked login should re-render 200, got %d", response.status)
	}
	if !strings.Contains(response.body, "登录失败次数过多") {
		t.Fatal("lockout message missing from the rendered form")
	}
	if location := response.headers.Get("Location"); location != "" {
		t.Fatalf("a locked login must not redirect, got %q", location)
	}
	// A blocked attempt is still a failure for the counter, like form_invalid.
	if len(risk.failures) != 1 || risk.failures[0] != "diff-user" {
		t.Fatalf("blocked attempt not recorded: %v", risk.failures)
	}
}

func TestSSRLoginSkipsFailureRecordWithoutUsername(t *testing.T) {
	h, risk := loginRiskHarness(t)
	postLoginForm(h, url.Values{"username": {""}, "password": {"x"}, "captcha_code": {""}})
	if len(risk.failures) != 0 {
		t.Fatalf("empty username must not record a failure: %v", risk.failures)
	}
}
