package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"mirror-studio/internal/config"
	"mirror-studio/internal/crypt"
	"mirror-studio/internal/db"
	"mirror-studio/internal/httpapi"
	"mirror-studio/internal/siteconfig"
)

// ── fixtures generated with the reference environment ──

// djangoArgon2Hash was produced by the Django 1.x reference:
//
//	make_password('DiffPass123!@#', hasher='argon2')
const djangoArgon2Hash = "argon2$argon2id$v=19$m=102400,t=2,p=8$YVByY2dQcEo3Q2VJZkRqODQ0NW10NQ$yxF/mX/28Sg8RTHLI6K9RPHQhwl4pTecCjyQFMMN4dc"

// djangoPBKDF2Hash was produced by the same environment with the pbkdf2_sha256
// hasher, which user records created before the argon2 switch still carry.
const djangoPBKDF2Hash = "pbkdf2_sha256$1000000$MR1EpgMVlFAXWAQmQ0TrdM$hB+7Z+ponuYccjhxml740zIDLytEEe8NVJkJzUmj2pQ="

// Captcha fixtures produced by the reference apps/users/utils.py
// render_captcha_data_uri; the artwork is deterministic for a fixed code.
const captchaFixtureABCD = "data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIxNjgiIGhlaWdodD0iNTYiIHZpZXdCb3g9IjAgMCAxNjggNTYiIHJvbGU9ImltZyIgYXJpYS1sYWJlbD0i6aqM6K+B56CBIj4KICA8ZGVmcz4KICAgIDxsaW5lYXJHcmFkaWVudCBpZD0iY2FwdGNoYS1iZyIgeDE9IjAlIiB5MT0iMCUiIHgyPSIxMDAlIiB5Mj0iMTAwJSI+CiAgICAgIDxzdG9wIG9mZnNldD0iMCUiIHN0b3AtY29sb3I9IiNmOGZiZmYiIC8+CiAgICAgIDxzdG9wIG9mZnNldD0iMTAwJSIgc3RvcC1jb2xvcj0iI2VlZjRmZiIgLz4KICAgIDwvbGluZWFyR3JhZGllbnQ+CiAgICA8ZmlsdGVyIGlkPSJzb2Z0LW5vaXNlIj4KICAgICAgPGZlVHVyYnVsZW5jZSB0eXBlPSJmcmFjdGFsTm9pc2UiIGJhc2VGcmVxdWVuY3k9IjAuOSIgbnVtT2N0YXZlcz0iMSIgc2VlZD0iNjcwIiAvPgogICAgICA8ZmVDb2xvck1hdHJpeCB0eXBlPSJzYXR1cmF0ZSIgdmFsdWVzPSIwIiAvPgogICAgICA8ZmVDb21wb25lbnRUcmFuc2Zlcj4KICAgICAgICA8ZmVGdW5jQSB0eXBlPSJ0YWJsZSIgdGFibGVWYWx1ZXM9IjAgMCAwLjA4IDAiIC8+CiAgICAgIDwvZmVDb21wb25lbnRUcmFuc2Zlcj4KICAgIDwvZmlsdGVyPgogIDwvZGVmcz4KICA8cmVjdCB4PSIwLjc1IiB5PSIwLjc1IiB3aWR0aD0iMTY2LjUiIGhlaWdodD0iNTQuNSIgcng9IjE2IiBmaWxsPSJ1cmwoI2NhcHRjaGEtYmcpIiBzdHJva2U9InJnYmEoMTQ4LDE2MywxODQsMC4yOCkiIC8+CiAgPHJlY3QgeD0iMCIgeT0iMCIgd2lkdGg9IjE2OCIgaGVpZ2h0PSI1NiIgcng9IjE2IiBmaWxsPSJ3aGl0ZSIgZmlsdGVyPSJ1cmwoI3NvZnQtbm9pc2UpIiBvcGFjaXR5PSIwLjQ1IiAvPgogIDxjaXJjbGUgY3g9IjE1MiIgY3k9IjMwIiByPSI0IiBmaWxsPSJyZ2JhKDE5MiwgMjE2LCAyNTUsIDAuMzIpIiAvPjxjaXJjbGUgY3g9IjQyIiBjeT0iMzIiIHI9IjMiIGZpbGw9InJnYmEoMTk2LCAyMzQsIDI1NSwgMC4zMikiIC8+PGNpcmNsZSBjeD0iNzYiIGN5PSIzNSIgcj0iMiIgZmlsbD0icmdiYSgxODMsIDE5NywgMjU1LCAwLjMyKSIgLz48Y2lyY2xlIGN4PSI0NCIgY3k9IjkiIHI9IjMiIGZpbGw9InJnYmEoMjE1LCAyMTQsIDI1NSwgMC4zMikiIC8+PGNpcmNsZSBjeD0iNDUiIGN5PSIxNyIgcj0iMyIgZmlsbD0icmdiYSgxOTgsIDIyOCwgMjU1LCAwLjMyKSIgLz48Y2lyY2xlIGN4PSI4NiIgY3k9IjI4IiByPSIzIiBmaWxsPSJyZ2JhKDE5MCwgMTkyLCAyNTUsIDAuMzIpIiAvPjxjaXJjbGUgY3g9IjE1MiIgY3k9IjMzIiByPSIzIiBmaWxsPSJyZ2JhKDIyOSwgMjIzLCAyNTUsIDAuMzIpIiAvPgogIDxwYXRoIGQ9Ik0gLTggNDIgUSA4NC4wIDI5IDE3NiAzNiIgc3Ryb2tlPSJyZ2JhKDE3NCwgMTM5LCAyMjMsIDAuNDIpIiBzdHJva2Utd2lkdGg9IjEuOTIiIGZpbGw9Im5vbmUiIHN0cm9rZS1saW5lY2FwPSJyb3VuZCIgLz48cGF0aCBkPSJNIC04IDM0IFEgODQuMCA0NyAxNzYgNDIiIHN0cm9rZT0icmdiYSgxMzAsIDE0MiwgMjM2LCAwLjQyKSIgc3Ryb2tlLXdpZHRoPSIxLjcwIiBmaWxsPSJub25lIiBzdHJva2UtbGluZWNhcD0icm91bmQiIC8+PHBhdGggZD0iTSAtOCAzOCBRIDg0LjAgMTYgMTc2IDI3IiBzdHJva2U9InJnYmEoMTU1LCAxNzEsIDIxMywgMC40MikiIHN0cm9rZS13aWR0aD0iMS41MyIgZmlsbD0ibm9uZSIgc3Ryb2tlLWxpbmVjYXA9InJvdW5kIiAvPjxwYXRoIGQ9Ik0gLTggMjcgUSA4NC4wIDUxIDE3NiAxNCIgc3Ryb2tlPSJyZ2JhKDE4MSwgMTg0LCAyMjgsIDAuNDIpIiBzdHJva2Utd2lkdGg9IjEuOTkiIGZpbGw9Im5vbmUiIHN0cm9rZS1saW5lY2FwPSJyb3VuZCIgLz4KICA8dGV4dCB4PSIyNiIgeT0iMzgiIHRyYW5zZm9ybT0icm90YXRlKC0zIDI2IDM4KSIgZmlsbD0iIzBmMTcyYSIgZmlsbC1vcGFjaXR5PSIwLjkwIiBmb250LWZhbWlseT0idWktbW9ub3NwYWNlLCBTRk1vbm8tUmVndWxhciwgTWVubG8sIE1vbmFjbywgQ29uc29sYXMsIG1vbm9zcGFjZSIgZm9udC1zaXplPSIyOCIgZm9udC13ZWlnaHQ9IjcwMCIgbGV0dGVyLXNwYWNpbmc9IjEuNSI+QTwvdGV4dD48dGV4dCB4PSI1NyIgeT0iMzQiIHRyYW5zZm9ybT0icm90YXRlKC03IDU3IDM0KSIgZmlsbD0iIzBmMTcyYSIgZmlsbC1vcGFjaXR5PSIwLjg2IiBmb250LWZhbWlseT0idWktbW9ub3NwYWNlLCBTRk1vbm8tUmVndWxhciwgTWVubG8sIE1vbmFjbywgQ29uc29sYXMsIG1vbm9zcGFjZSIgZm9udC1zaXplPSIyOCIgZm9udC13ZWlnaHQ9IjcwMCIgbGV0dGVyLXNwYWNpbmc9IjEuNSI+QjwvdGV4dD48dGV4dCB4PSI5MiIgeT0iMzYiIHRyYW5zZm9ybT0icm90YXRlKDYgOTIgMzYpIiBmaWxsPSIjMGYxNzJhIiBmaWxsLW9wYWNpdHk9IjAuOTMiIGZvbnQtZmFtaWx5PSJ1aS1tb25vc3BhY2UsIFNGTW9uby1SZWd1bGFyLCBNZW5sbywgTW9uYWNvLCBDb25zb2xhcywgbW9ub3NwYWNlIiBmb250LXNpemU9IjI5IiBmb250LXdlaWdodD0iNzAwIiBsZXR0ZXItc3BhY2luZz0iMS41Ij5DPC90ZXh0Pjx0ZXh0IHg9IjEyMSIgeT0iMzYiIHRyYW5zZm9ybT0icm90YXRlKC02IDEyMSAzNikiIGZpbGw9IiMwZjE3MmEiIGZpbGwtb3BhY2l0eT0iMC44OSIgZm9udC1mYW1pbHk9InVpLW1vbm9zcGFjZSwgU0ZNb25vLVJlZ3VsYXIsIE1lbmxvLCBNb25hY28sIENvbnNvbGFzLCBtb25vc3BhY2UiIGZvbnQtc2l6ZT0iMjciIGZvbnQtd2VpZ2h0PSI3MDAiIGxldHRlci1zcGFjaW5nPSIxLjUiPkQ8L3RleHQ+Cjwvc3ZnPg=="
const captchaFixture2K7Z = "data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIxNjgiIGhlaWdodD0iNTYiIHZpZXdCb3g9IjAgMCAxNjggNTYiIHJvbGU9ImltZyIgYXJpYS1sYWJlbD0i6aqM6K+B56CBIj4KICA8ZGVmcz4KICAgIDxsaW5lYXJHcmFkaWVudCBpZD0iY2FwdGNoYS1iZyIgeDE9IjAlIiB5MT0iMCUiIHgyPSIxMDAlIiB5Mj0iMTAwJSI+CiAgICAgIDxzdG9wIG9mZnNldD0iMCUiIHN0b3AtY29sb3I9IiNmOGZiZmYiIC8+CiAgICAgIDxzdG9wIG9mZnNldD0iMTAwJSIgc3RvcC1jb2xvcj0iI2VlZjRmZiIgLz4KICAgIDwvbGluZWFyR3JhZGllbnQ+CiAgICA8ZmlsdGVyIGlkPSJzb2Z0LW5vaXNlIj4KICAgICAgPGZlVHVyYnVsZW5jZSB0eXBlPSJmcmFjdGFsTm9pc2UiIGJhc2VGcmVxdWVuY3k9IjAuOSIgbnVtT2N0YXZlcz0iMSIgc2VlZD0iNzI1IiAvPgogICAgICA8ZmVDb2xvck1hdHJpeCB0eXBlPSJzYXR1cmF0ZSIgdmFsdWVzPSIwIiAvPgogICAgICA8ZmVDb21wb25lbnRUcmFuc2Zlcj4KICAgICAgICA8ZmVGdW5jQSB0eXBlPSJ0YWJsZSIgdGFibGVWYWx1ZXM9IjAgMCAwLjA4IDAiIC8+CiAgICAgIDwvZmVDb21wb25lbnRUcmFuc2Zlcj4KICAgIDwvZmlsdGVyPgogIDwvZGVmcz4KICA8cmVjdCB4PSIwLjc1IiB5PSIwLjc1IiB3aWR0aD0iMTY2LjUiIGhlaWdodD0iNTQuNSIgcng9IjE2IiBmaWxsPSJ1cmwoI2NhcHRjaGEtYmcpIiBzdHJva2U9InJnYmEoMTQ4LDE2MywxODQsMC4yOCkiIC8+CiAgPHJlY3QgeD0iMCIgeT0iMCIgd2lkdGg9IjE2OCIgaGVpZ2h0PSI1NiIgcng9IjE2IiBmaWxsPSJ3aGl0ZSIgZmlsdGVyPSJ1cmwoI3NvZnQtbm9pc2UpIiBvcGFjaXR5PSIwLjQ1IiAvPgogIDxjaXJjbGUgY3g9IjI5IiBjeT0iMzkiIHI9IjIiIGZpbGw9InJnYmEoMTgwLCAyMjUsIDI1NSwgMC4zMikiIC8+PGNpcmNsZSBjeD0iMTQ3IiBjeT0iMjgiIHI9IjQiIGZpbGw9InJnYmEoMTg4LCAyMzMsIDI1NSwgMC4zMikiIC8+PGNpcmNsZSBjeD0iNjMiIGN5PSIyOSIgcj0iMiIgZmlsbD0icmdiYSgyMDYsIDIyMCwgMjU1LCAwLjMyKSIgLz48Y2lyY2xlIGN4PSIzOCIgY3k9IjIxIiByPSIyIiBmaWxsPSJyZ2JhKDE4NCwgMjI1LCAyNTUsIDAuMzIpIiAvPjxjaXJjbGUgY3g9IjE2MCIgY3k9IjI4IiByPSIyIiBmaWxsPSJyZ2JhKDIxMiwgMjE0LCAyNTUsIDAuMzIpIiAvPjxjaXJjbGUgY3g9IjMzIiBjeT0iMjkiIHI9IjIiIGZpbGw9InJnYmEoMjAxLCAyMTEsIDI1NSwgMC4zMikiIC8+PGNpcmNsZSBjeD0iMTAzIiBjeT0iMzMiIHI9IjQiIGZpbGw9InJnYmEoMjI1LCAyMTUsIDI1NSwgMC4zMikiIC8+CiAgPHBhdGggZD0iTSAtOCAzNSBRIDg0LjAgNDkgMTc2IDMyIiBzdHJva2U9InJnYmEoMTgxLCAxNzIsIDIzMiwgMC40MikiIHN0cm9rZS13aWR0aD0iMS4zNyIgZmlsbD0ibm9uZSIgc3Ryb2tlLWxpbmVjYXA9InJvdW5kIiAvPjxwYXRoIGQ9Ik0gLTggMjUgUSA4NC4wIDcgMTc2IDQ2IiBzdHJva2U9InJnYmEoMTg0LCAxOTcsIDIxOSwgMC40MikiIHN0cm9rZS13aWR0aD0iMS42MCIgZmlsbD0ibm9uZSIgc3Ryb2tlLWxpbmVjYXA9InJvdW5kIiAvPjxwYXRoIGQ9Ik0gLTggMzYgUSA4NC4wIDMxIDE3NiA0NiIgc3Ryb2tlPSJyZ2JhKDE0OSwgMTU4LCAxOTUsIDAuNDIpIiBzdHJva2Utd2lkdGg9IjEuNTMiIGZpbGw9Im5vbmUiIHN0cm9rZS1saW5lY2FwPSJyb3VuZCIgLz48cGF0aCBkPSJNIC04IDEzIFEgODQuMCAyMiAxNzYgMjMiIHN0cm9rZT0icmdiYSgxMzAsIDE0MSwgMjMxLCAwLjQyKSIgc3Ryb2tlLXdpZHRoPSIxLjkxIiBmaWxsPSJub25lIiBzdHJva2UtbGluZWNhcD0icm91bmQiIC8+CiAgPHRleHQgeD0iMjUiIHk9IjM0IiB0cmFuc2Zvcm09InJvdGF0ZSg0IDI1IDM0KSIgZmlsbD0iIzBmMTcyYSIgZmlsbC1vcGFjaXR5PSIwLjg3IiBmb250LWZhbWlseT0idWktbW9ub3NwYWNlLCBTRk1vbm8tUmVndWxhciwgTWVubG8sIE1vbmFjbywgQ29uc29sYXMsIG1vbm9zcGFjZSIgZm9udC1zaXplPSIyOCIgZm9udC13ZWlnaHQ9IjcwMCIgbGV0dGVyLXNwYWNpbmc9IjEuNSI+MjwvdGV4dD48dGV4dCB4PSI1OSIgeT0iMzUiIHRyYW5zZm9ybT0icm90YXRlKC00IDU5IDM1KSIgZmlsbD0iIzBmMTcyYSIgZmlsbC1vcGFjaXR5PSIwLjg2IiBmb250LWZhbWlseT0idWktbW9ub3NwYWNlLCBTRk1vbm8tUmVndWxhciwgTWVubG8sIE1vbmFjbywgQ29uc29sYXMsIG1vbm9zcGFjZSIgZm9udC1zaXplPSIzMCIgZm9udC13ZWlnaHQ9IjcwMCIgbGV0dGVyLXNwYWNpbmc9IjEuNSI+SzwvdGV4dD48dGV4dCB4PSI5MSIgeT0iMzQiIHRyYW5zZm9ybT0icm90YXRlKC0xMSA5MSAzNCkiIGZpbGw9IiMwZjE3MmEiIGZpbGwtb3BhY2l0eT0iMC45MCIgZm9udC1mYW1pbHk9InVpLW1vbm9zcGFjZSwgU0ZNb25vLVJlZ3VsYXIsIE1lbmxvLCBNb25hY28sIENvbnNvbGFzLCBtb25vc3BhY2UiIGZvbnQtc2l6ZT0iMzEiIGZvbnQtd2VpZ2h0PSI3MDAiIGxldHRlci1zcGFjaW5nPSIxLjUiPjc8L3RleHQ+PHRleHQgeD0iMTI1IiB5PSIzOSIgdHJhbnNmb3JtPSJyb3RhdGUoNiAxMjUgMzkpIiBmaWxsPSIjMGYxNzJhIiBmaWxsLW9wYWNpdHk9IjAuODYiIGZvbnQtZmFtaWx5PSJ1aS1tb25vc3BhY2UsIFNGTW9uby1SZWd1bGFyLCBNZW5sbywgTW9uYWNvLCBDb25zb2xhcywgbW9ub3NwYWNlIiBmb250LXNpemU9IjI2IiBmb250LXdlaWdodD0iNzAwIiBsZXR0ZXItc3BhY2luZz0iMS41Ij5aPC90ZXh0Pgo8L3N2Zz4="

// ── password hashing ──

func TestArgon2RoundTrip(t *testing.T) {
	encoded, err := HashPassword("DiffPass123!@#")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(encoded, "argon2$argon2id$v=19$m=102400,t=2,p=8$") {
		t.Fatalf("unexpected hash prefix: %q", encoded)
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		t.Fatalf("expected 6 sections, got %d in %q", len(parts), encoded)
	}
	if salt, err := decodeRawBase64(parts[4]); err != nil || len(salt) != 20 {
		t.Fatalf("salt must be 20 raw-base64 bytes, got %d (%v)", len(salt), err)
	}
	if hash, err := decodeRawBase64(parts[5]); err != nil || len(hash) != 32 {
		t.Fatalf("hash must be 32 raw-base64 bytes, got %d (%v)", len(hash), err)
	}

	if ok, needsRehash := VerifyPassword(encoded, "DiffPass123!@#"); !ok || needsRehash {
		t.Fatalf("verify of a fresh hash = (%v, %v), want (true, false)", ok, needsRehash)
	}
	if ok, _ := VerifyPassword(encoded, "DiffPass123!@"); ok {
		t.Fatal("wrong password verified")
	}
	if ok, _ := VerifyPassword(encoded, ""); ok {
		t.Fatal("empty password verified")
	}
}

func TestVerifyDjangoArgon2Hash(t *testing.T) {
	ok, needsRehash := VerifyPassword(djangoArgon2Hash, "DiffPass123!@#")
	if !ok {
		t.Fatal("Django-produced argon2 hash did not verify")
	}
	if needsRehash {
		t.Fatal("Django hash uses the default parameters, no rehash expected")
	}
	if ok, _ := VerifyPassword(djangoArgon2Hash, "wrong"); ok {
		t.Fatal("wrong password verified against the Django hash")
	}
}

func TestVerifyPBKDF2Fallback(t *testing.T) {
	ok, needsRehash := VerifyPassword(djangoPBKDF2Hash, "DiffPass123!@#")
	if !ok {
		t.Fatal("pbkdf2_sha256 fallback did not verify")
	}
	if !needsRehash {
		t.Fatal("a pbkdf2 hash should report needsRehash so it can be upgraded")
	}
	if ok, _ := VerifyPassword(djangoPBKDF2Hash, "nope"); ok {
		t.Fatal("wrong password verified against the pbkdf2 hash")
	}
}

// ── password validation ──

func TestValidatePasswordMessages(t *testing.T) {
	user := &User{Username: "diff-user", Email: "diff@example.com"}
	cases := []struct {
		name     string
		password string
		user     *User
		want     []httpapi.FieldError
	}{
		{
			name:     "minimum length and common list",
			password: "1234567",
			want: []httpapi.FieldError{
				{Message: "This password is too short. It must contain at least 8 characters.", Code: "password_too_short"},
				{Message: "这个密码太常见了。", Code: "password_too_common"},
				{Message: "密码只包含数字。", Code: "password_entirely_numeric"},
			},
		},
		{
			name:     "numeric eight digits",
			password: "12345678",
			want: []httpapi.FieldError{
				{Message: "这个密码太常见了。", Code: "password_too_common"},
				{Message: "密码只包含数字。", Code: "password_entirely_numeric"},
			},
		},
		{
			name:     "common word",
			password: "password",
			want: []httpapi.FieldError{
				{Message: "这个密码太常见了。", Code: "password_too_common"},
			},
		},
		{
			name:     "similar to username",
			password: "diffuser1",
			user:     user,
			want: []httpapi.FieldError{
				{Message: "密码跟 用户名 太相似了。", Code: "password_too_similar"},
			},
		},
		{
			name:     "similar to email",
			password: "differ@example.com",
			user:     user,
			want: []httpapi.FieldError{
				{Message: "密码跟 邮箱 太相似了。", Code: "password_too_similar"},
			},
		},
		{
			name:     "accepted without a user",
			password: "correcthorsebattery",
			want:     []httpapi.FieldError{},
		},
		{
			name:     "accepted with a user",
			password: "DiffPass123!@#",
			user:     user,
			want:     []httpapi.FieldError{},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := ValidatePassword(testCase.password, testCase.user)
			if len(got) != len(testCase.want) {
				t.Fatalf("ValidatePassword(%q) = %#v, want %#v", testCase.password, got, testCase.want)
			}
			for index := range got {
				if got[index] != testCase.want[index] {
					t.Fatalf("error %d = %#v, want %#v", index, got[index], testCase.want[index])
				}
			}
		})
	}
}

// TestRegistrationAndResetSkipSimilarity documents the asymmetry the contract
// froze: validate_password(password) without a user never reports similarity.
func TestRegistrationAndResetSkipSimilarity(t *testing.T) {
	user := &User{Username: "diff-user"}
	if got := ValidatePassword("diffuser1", nil); len(got) != 0 {
		t.Fatalf("without a user the similarity validator must be skipped, got %#v", got)
	}
	if got := ValidatePassword("diffuser1", user); len(got) != 1 || got[0].Code != "password_too_similar" {
		t.Fatalf("with a user the similarity validator must fire, got %#v", got)
	}
}

func TestFormErrorsASCIIEscaping(t *testing.T) {
	errs := httpapi.NewFormErrors()
	errs.Add("captcha_code", "验证码错误", "")
	// Literal output of python json.dumps({...}, ensure_ascii=True):
	// {"captcha_code": [{"message": "\u9a8c\u8bc1\u7801\u9519\u8bef", "code": ""}]}
	const want = `{"captcha_code": [{"message": "\u9a8c\u8bc1\u7801\u9519\u8bef", "code": ""}]}`
	if got := errs.AsJSON(); got != want {
		t.Fatalf("AsJSON mismatch:\n got %s\nwant %s", got, want)
	}
}

func TestFormErrorsOneFieldJSON(t *testing.T) {
	errs := httpapi.NewFormErrors()
	errs.Add("", "账号或密码错误", "")
	const want = `{"__all__": [{"message": "\u8d26\u53f7\u6216\u5bc6\u7801\u9519\u8bef", "code": ""}]}`
	if got := errs.AsJSON(); got != want {
		t.Fatalf("AsJSON mismatch:\n got %s\nwant %s", got, want)
	}
}

// ── captcha ──

func TestCaptchaMatchesReferenceSVG(t *testing.T) {
	if got := renderCaptchaDataURI("ABCD"); got != captchaFixtureABCD {
		t.Fatalf("captcha SVG for ABCD differs from the reference:\n got %s\nwant %s", got, captchaFixtureABCD)
	}
	if got := renderCaptchaDataURI("2K7Z"); got != captchaFixture2K7Z {
		t.Fatalf("captcha SVG for 2K7Z differs from the reference:\n got %s\nwant %s", got, captchaFixture2K7Z)
	}
}

func TestCaptchaCodeShape(t *testing.T) {
	for i := 0; i < 32; i++ {
		code := GenerateCaptchaCode(CaptchaLength)
		if len(code) != 4 {
			t.Fatalf("code %q is not 4 characters", code)
		}
		for _, char := range code {
			if !strings.ContainsRune(CaptchaAlphabet, char) {
				t.Fatalf("code %q contains %q outside the alphabet", code, char)
			}
		}
	}
}

func TestCommonPasswordList(t *testing.T) {
	if len(commonPasswords) != 19640 {
		t.Fatalf("common password list has %d entries, want 19640", len(commonPasswords))
	}
	if !sort.StringsAreSorted(commonPasswords) {
		t.Fatal("common password list must be sorted")
	}
	for _, sample := range []string{"123456", "password", "qwerty", "letmein"} {
		if _, ok := commonPasswordSet[sample]; !ok {
			t.Fatalf("expected %q in the common password set", sample)
		}
	}
}

// ── integration harness ──

type testEnv struct {
	t      *testing.T
	server *httptest.Server
	svc    *Service
	db     *db.DB
	cfg    *config.Config
	client *http.Client
	token  string
}

type envOptions struct {
	registerEnabled   bool
	captchaEnabled    bool
	emailVerification bool
	emailHost         string
}

func newTestEnv(t *testing.T, options envOptions) *testEnv {
	t.Helper()
	handle, err := db.Open(filepath.Join(t.TempDir(), "auth-test.sqlite3"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := handle.Migrate(nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cfg := &config.Config{
		Debug:               true,
		TimeZone:            "Asia/Shanghai",
		SiteName:            "绘镜 Studio",
		RegisterEnabled:     options.registerEnabled,
		LoginCaptchaEnabled: options.captchaEnabled,
		EmailVerification:   options.emailVerification,
		EmailCodeLength:     6,
		EmailCodeTTL:        600 * time.Second,
		EmailResendInterval: 60 * time.Second,
		EmailDailyPerEmail:  10,
		EmailDailyPerIP:     30,
		EmailHost:           options.emailHost,
		EmailPort:           465,
		EmailFrom:           "noreply@example.com",
		EmailTimeout:        5 * time.Second,
	}
	cipher, err := crypt.NewFieldCipher("test-field-crypt-key")
	if err != nil {
		t.Fatalf("field cipher: %v", err)
	}
	site := siteconfig.New(handle, cipher, nil)
	svc := NewService(handle, cfg, cipher, site, nil)
	mux := http.NewServeMux()
	NewHandlers(svc, site, cfg, nil).Register(mux)
	chain := httpapi.NewMiddleware(httpapi.Options{
		Config:      cfg,
		Sessions:    httpapi.NewSessionStore(handle, false),
		ResolveUser: svc.ResolvePrincipal,
	}, mux)
	server := httptest.NewServer(chain)
	t.Cleanup(server.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return &testEnv{
		t:      t,
		server: server,
		svc:    svc,
		db:     handle,
		cfg:    cfg,
		client: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}
}

func (e *testEnv) request(method, path, body, contentType string, withCSRF bool) *http.Response {
	e.t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequest(method, e.server.URL+path, reader)
	if err != nil {
		e.t.Fatalf("build request: %v", err)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if withCSRF {
		if e.token == "" {
			e.refreshCSRF()
		}
		request.Header.Set("X-CSRFToken", e.token)
	}
	response, err := e.client.Do(request)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	return response
}

func (e *testEnv) get(path string) *http.Response {
	return e.request(http.MethodGet, path, "", "", false)
}

func (e *testEnv) refreshCSRF() {
	e.t.Helper()
	response := e.get("/api/auth/csrf")
	defer response.Body.Close()
	payload := decodeEnvelope(e.t, response)
	data, _ := payload["data"].(map[string]any)
	token, _ := data["csrf_token"].(string)
	if token == "" {
		e.t.Fatal("csrf endpoint returned an empty token")
	}
	e.token = token
}

func (e *testEnv) postJSON(path string, payload any) (*http.Response, map[string]any) {
	e.t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		e.t.Fatalf("encode payload: %v", err)
	}
	response := e.request(http.MethodPost, path, string(encoded), "application/json", true)
	return response, decodeEnvelope(e.t, response)
}

func (e *testEnv) patchJSON(path string, payload any) (*http.Response, map[string]any) {
	e.t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		e.t.Fatalf("encode payload: %v", err)
	}
	response := e.request(http.MethodPatch, path, string(encoded), "application/json", true)
	return response, decodeEnvelope(e.t, response)
}

func (e *testEnv) captchaCode() string {
	e.t.Helper()
	response := e.get("/api/auth/captcha")
	defer response.Body.Close()
	payload := decodeEnvelope(e.t, response)
	data, _ := payload["data"].(map[string]any)
	code, _ := data["captcha_code"].(string)
	if code == "" {
		e.t.Fatal("captcha endpoint returned an empty code")
	}
	return code
}

func (e *testEnv) sessionCookie() string {
	e.t.Helper()
	parsed, err := url.Parse(e.server.URL)
	if err != nil {
		e.t.Fatalf("parse server url: %v", err)
	}
	for _, cookie := range e.client.Jar.Cookies(parsed) {
		if cookie.Name == httpapi.SessionCookieName {
			return cookie.Value
		}
	}
	return ""
}

func (e *testEnv) call(handler func() (*http.Response, map[string]any)) (int, map[string]any, http.Header) {
	e.t.Helper()
	response, payload := handler()
	defer response.Body.Close()
	return response.StatusCode, payload, response.Header
}

func decodeEnvelope(t *testing.T, response *http.Response) map[string]any {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	envelope := map[string]any{}
	if len(bytes.TrimSpace(body)) == 0 {
		t.Fatalf("empty body for %s %s (status %d)", response.Request.Method, response.Request.URL.Path, response.StatusCode)
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode %q: %v", string(body), err)
	}
	return envelope
}

func messageOf(t *testing.T, payload map[string]any) string {
	t.Helper()
	message, _ := payload["message"].(string)
	return message
}

func dataOf(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	data, _ := payload["data"].(map[string]any)
	return data
}

// ── endpoint behaviour ──

func TestCaptchaEndpoint(t *testing.T) {
	env := newTestEnv(t, envOptions{registerEnabled: true, captchaEnabled: true})
	response := env.get("/api/auth/captcha")
	defer response.Body.Close()
	payload := decodeEnvelope(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	data := dataOf(t, payload)
	code, _ := data["captcha_code"].(string)
	if len(code) != CaptchaLength {
		t.Fatalf("captcha_code = %q, want %d characters", code, CaptchaLength)
	}
	svg, _ := data["captcha_svg"].(string)
	if !strings.HasPrefix(svg, "data:image/svg+xml;base64,") {
		t.Fatalf("captcha_svg = %q, want a base64 SVG data URI", svg)
	}
	if env.sessionCookie() == "" {
		t.Fatal("captcha response did not issue a session cookie")
	}
	if !containsCookie(response, httpapi.CSRFCookieName) {
		t.Fatal("captcha response did not (re)issue the csrftoken cookie")
	}
}

func TestCaptchaDisabled(t *testing.T) {
	env := newTestEnv(t, envOptions{registerEnabled: true, captchaEnabled: false})
	response := env.get("/api/auth/captcha")
	defer response.Body.Close()
	data := dataOf(t, decodeEnvelope(t, response))
	if code, _ := data["captcha_code"].(string); code != "" {
		t.Fatalf("captcha_code = %q, want empty when disabled", code)
	}
	if svg, _ := data["captcha_svg"].(string); svg != "" {
		t.Fatalf("captcha_svg = %q, want empty when disabled", svg)
	}
}

func TestLoginFlow(t *testing.T) {
	env := newTestEnv(t, envOptions{registerEnabled: true, captchaEnabled: true})
	if _, err := env.svc.Register(context.Background(), "diff-user", "diff@example.com", "DiffPass123!@#", "127.0.0.1"); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	code := env.captchaCode()
	status, payload, _ := env.call(func() (*http.Response, map[string]any) {
		return env.postJSON("/api/auth/login", map[string]any{
			"username": "diff-user", "password": "DiffPass123!@#", "captcha_code": strings.ToLower(code),
		})
	})
	if status != http.StatusOK {
		t.Fatalf("login status = %d, message = %s", status, messageOf(t, payload))
	}
	if username, _ := dataOf(t, payload)["username"].(string); username != "diff-user" {
		t.Fatalf("login data.username = %q", username)
	}
	if _, ok := dataOf(t, payload)["next"]; ok {
		t.Fatal("login payload must not carry a next field")
	}
	if env.sessionCookie() == "" {
		t.Fatal("login did not store a session")
	}
	user, err := env.svc.ByUsername(context.Background(), "diff-user")
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.LastLoginAt == nil || user.LastLoginIP != "127.0.0.1" {
		t.Fatalf("last_login_at/last_login_ip not written: %#v", user)
	}
	if user.LastLogin == nil {
		t.Fatal("users_user.last_login was not written")
	}
}

func TestLoginCaptchaFailure(t *testing.T) {
	env := newTestEnv(t, envOptions{registerEnabled: true, captchaEnabled: true})
	if _, err := env.svc.Register(context.Background(), "diff-user", "diff@example.com", "DiffPass123!@#", "127.0.0.1"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	env.captchaCode()
	status, payload, _ := env.call(func() (*http.Response, map[string]any) {
		return env.postJSON("/api/auth/login", map[string]any{
			"username": "diff-user", "password": "DiffPass123!@#", "captcha_code": "ZZZZ",
		})
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	const want = `{"captcha_code": [{"message": "\u9a8c\u8bc1\u7801\u9519\u8bef", "code": ""}]}`
	if got := messageOf(t, payload); got != want {
		t.Fatalf("message = %s, want %s", got, want)
	}
}

func TestLoginBadCredentials(t *testing.T) {
	env := newTestEnv(t, envOptions{registerEnabled: true, captchaEnabled: true})
	if _, err := env.svc.Register(context.Background(), "diff-user", "diff@example.com", "DiffPass123!@#", "127.0.0.1"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	code := env.captchaCode()
	status, payload, _ := env.call(func() (*http.Response, map[string]any) {
		return env.postJSON("/api/auth/login", map[string]any{
			"username": "diff-user", "password": "wrong", "captcha_code": code,
		})
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	const want = `{"__all__": [{"message": "\u8d26\u53f7\u6216\u5bc6\u7801\u9519\u8bef", "code": ""}]}`
	if got := messageOf(t, payload); got != want {
		t.Fatalf("message = %s, want %s", got, want)
	}
}

func TestRegisterCheck(t *testing.T) {
	env := newTestEnv(t, envOptions{registerEnabled: true, captchaEnabled: false})
	if _, err := env.svc.Register(context.Background(), "taken", "taken@example.com", "DiffPass123!@#", "127.0.0.1"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	status, payload, _ := env.call(func() (*http.Response, map[string]any) {
		return env.postJSON("/api/auth/register/check", map[string]any{"username": "taken"})
	})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	item, _ := dataOf(t, payload)["username"].(map[string]any)
	if item["available"] != false || item["message"] != "账号已存在" {
		t.Fatalf("username item = %#v", item)
	}
	if _, ok := dataOf(t, payload)["email"]; ok {
		t.Fatal("only supplied fields may be echoed")
	}

	status, payload, _ = env.call(func() (*http.Response, map[string]any) {
		return env.postJSON("/api/auth/register/check", map[string]any{})
	})
	if status != http.StatusBadRequest || messageOf(t, payload) != "至少提供账号或邮箱" {
		t.Fatalf("empty payload -> %d %q", status, messageOf(t, payload))
	}
}

func TestRegisterClosed(t *testing.T) {
	env := newTestEnv(t, envOptions{registerEnabled: false, captchaEnabled: false})
	env.refreshCSRF()
	for _, path := range []string{
		"/api/auth/register/check",
		"/api/auth/register/email-code",
		"/api/auth/register",
	} {
		status, payload, _ := env.call(func() (*http.Response, map[string]any) {
			return env.postJSON(path, map[string]any{"username": "x", "email": "x@example.com"})
		})
		if status != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403", path, status)
		}
		if code, _ := payload["code"].(float64); int(code) != httpapi.CodePermission {
			t.Fatalf("%s code = %v, want %d", path, payload["code"], httpapi.CodePermission)
		}
		if messageOf(t, payload) != "当前站点已关闭注册" {
			t.Fatalf("%s message = %q", path, messageOf(t, payload))
		}
	}
}

func TestRegisterAutoLogin(t *testing.T) {
	env := newTestEnv(t, envOptions{registerEnabled: true, captchaEnabled: false, emailVerification: false})
	status, payload, _ := env.call(func() (*http.Response, map[string]any) {
		return env.postJSON("/api/auth/register", map[string]any{
			"username": "diff-user", "email": "diff@example.com",
			"email_verification_code": "", "password": "DiffPass123!@#", "confirm_password": "DiffPass123!@#",
		})
	})
	if status != http.StatusOK {
		t.Fatalf("register status = %d, message = %s", status, messageOf(t, payload))
	}
	if username, _ := dataOf(t, payload)["username"].(string); username != "diff-user" {
		t.Fatalf("data.username = %q", username)
	}
	if env.sessionCookie() == "" {
		t.Fatal("registration must auto-login")
	}
	status, payload, _ = env.call(func() (*http.Response, map[string]any) {
		return env.postJSON("/api/auth/register", map[string]any{
			"username": "diff-user", "email": "other@example.com",
			"email_verification_code": "", "password": "DiffPass123!@#", "confirm_password": "DiffPass123!@#",
		})
	})
	if status != http.StatusBadRequest {
		t.Fatalf("duplicate register status = %d", status)
	}
	const want = `{"username": [{"message": "\u8d26\u53f7\u5df2\u5b58\u5728", "code": ""}]}`
	if got := messageOf(t, payload); got != want {
		t.Fatalf("message = %s, want %s", got, want)
	}
}

func TestRegisterWeakPasswordJSON(t *testing.T) {
	env := newTestEnv(t, envOptions{registerEnabled: true, captchaEnabled: false, emailVerification: false})
	status, payload, _ := env.call(func() (*http.Response, map[string]any) {
		return env.postJSON("/api/auth/register", map[string]any{
			"username": "diff-weak", "email": "weak@example.com",
			"email_verification_code": "", "password": "12345678", "confirm_password": "12345678",
		})
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d", status)
	}
	const want = `{"__all__": [{"message": "\u8fd9\u4e2a\u5bc6\u7801\u592a\u5e38\u89c1\u4e86\u3002", "code": "password_too_common"}, {"message": "\u5bc6\u7801\u53ea\u5305\u542b\u6570\u5b57\u3002", "code": "password_entirely_numeric"}]}`
	if got := messageOf(t, payload); got != want {
		t.Fatalf("message = %s, want %s", got, want)
	}
}

func TestRegisterEmailCode(t *testing.T) {
	env := newTestEnv(t, envOptions{registerEnabled: true, captchaEnabled: false, emailVerification: true})
	status, payload, _ := env.call(func() (*http.Response, map[string]any) {
		return env.postJSON("/api/auth/register/email-code", map[string]any{
			"username": "code-user", "email": "code@example.com",
		})
	})
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}
	if code, _ := payload["code"].(float64); int(code) != httpapi.CodeEmailNotConfigured {
		t.Fatalf("code = %v, want %d", payload["code"], httpapi.CodeEmailNotConfigured)
	}
	want := "邮件发送配置不完整：缺少 EMAIL_HOST, EMAIL_HOST_USER, EMAIL_HOST_PASSWORD"
	if got := messageOf(t, payload); got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}

	// The failed send still armed the resend cooldown, exactly like the reference.
	status, payload, _ = env.call(func() (*http.Response, map[string]any) {
		return env.postJSON("/api/auth/register/email-code", map[string]any{
			"username": "code-user", "email": "code@example.com",
		})
	})
	if status != http.StatusTooManyRequests {
		t.Fatalf("second send status = %d, want 429", status)
	}
	if messageOf(t, payload) != "验证码发送过于频繁，请 60 秒后再试" {
		t.Fatalf("message = %q", messageOf(t, payload))
	}

	status, payload, _ = env.call(func() (*http.Response, map[string]any) {
		return env.postJSON("/api/auth/register/email-code", map[string]any{"email": "other@example.com"})
	})
	if status != http.StatusBadRequest || messageOf(t, payload) != "这个字段是必填项。" {
		t.Fatalf("missing username -> %d %q", status, messageOf(t, payload))
	}
}

func TestRegisterEmailCodeDuplicateUsername(t *testing.T) {
	env := newTestEnv(t, envOptions{registerEnabled: true, captchaEnabled: false, emailVerification: true})
	if _, err := env.svc.Register(context.Background(), "taken", "taken@example.com", "DiffPass123!@#", "127.0.0.1"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	status, payload, _ := env.call(func() (*http.Response, map[string]any) {
		return env.postJSON("/api/auth/register/email-code", map[string]any{
			"username": "taken", "email": "code@example.com",
		})
	})
	if status != http.StatusBadRequest || messageOf(t, payload) != "账号已存在" {
		t.Fatalf("duplicate username -> %d %q", status, messageOf(t, payload))
	}
}

func TestResetPassword(t *testing.T) {
	env := newTestEnv(t, envOptions{registerEnabled: true, captchaEnabled: true})
	if _, err := env.svc.Register(context.Background(), "diff-user", "diff@example.com", "DiffPass123!@#", "127.0.0.1"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	code := env.captchaCode()
	status, payload, _ := env.call(func() (*http.Response, map[string]any) {
		return env.postJSON("/api/auth/reset-password", map[string]any{
			"captcha_code": code, "username": "diff-user", "email": "diff@example.com",
			"new_password": "DiffPass456!@#", "confirm_password": "DiffPass456!@#",
		})
	})
	if status != http.StatusOK {
		t.Fatalf("reset status = %d, message = %s", status, messageOf(t, payload))
	}
	if _, err := env.svc.Authenticate(context.Background(), "diff-user", "DiffPass456!@#"); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}

	// Unknown user and mismatched email are reported with the contract wording.
	env2 := newTestEnv(t, envOptions{registerEnabled: true, captchaEnabled: false})
	status, payload, _ = env2.call(func() (*http.Response, map[string]any) {
		return env2.postJSON("/api/auth/reset-password", map[string]any{
			"captcha_code": "ABCD", "username": "nobody", "email": "",
			"new_password": "DiffPass456!@#", "confirm_password": "DiffPass456!@#",
		})
	})
	if status != http.StatusBadRequest || messageOf(t, payload) != `{"__all__": [{"message": "\u7528\u6237\u4e0d\u5b58\u5728", "code": ""}]}` {
		t.Fatalf("unknown user -> %d %s", status, messageOf(t, payload))
	}
}

func TestAccountEndpoints(t *testing.T) {
	env := newTestEnv(t, envOptions{registerEnabled: true, captchaEnabled: true, emailVerification: false})
	if _, err := env.svc.Register(context.Background(), "diff-user", "diff@example.com", "DiffPass123!@#", "127.0.0.1"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := env.svc.Register(context.Background(), "other", "other@example.com", "DiffPass123!@#", "127.0.0.1"); err != nil {
		t.Fatalf("seed second user: %v", err)
	}

	// Anonymous access redirects to the workspace login URL.
	response := env.get("/api/account/summary")
	response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("anonymous summary status = %d, want 302", response.StatusCode)
	}
	if location := response.Header.Get("Location"); location != "/workspace?auth=login&next=/api/account/summary" {
		t.Fatalf("anonymous redirect = %q", location)
	}

	code := env.captchaCode()
	if status, payload, _ := env.call(func() (*http.Response, map[string]any) {
		return env.postJSON("/api/auth/login", map[string]any{
			"username": "diff-user", "password": "DiffPass123!@#", "captcha_code": code,
		})
	}); status != http.StatusOK {
		t.Fatalf("login failed: %d %s", status, messageOf(t, payload))
	}

	status, payload, _ := env.call(func() (*http.Response, map[string]any) {
		response := env.get("/api/account/summary")
		return response, decodeEnvelope(t, response)
	})
	if status != http.StatusOK {
		t.Fatalf("summary status = %d", status)
	}
	data := dataOf(t, payload)
	user, _ := data["user"].(map[string]any)
	if user["username"] != "diff-user" || user["nickname"] != "diff-user" {
		t.Fatalf("summary user = %#v", user)
	}
	quota, _ := data["quota"].(map[string]any)
	if quota == nil || quota["last_reset_date"] != "" {
		t.Fatalf("summary quota = %#v", quota)
	}
	tasks, ok := data["recent_tasks"].([]any)
	if !ok || len(tasks) != 0 {
		t.Fatalf("recent_tasks = %#v, want an empty array", data["recent_tasks"])
	}

	status, payload, _ = env.call(func() (*http.Response, map[string]any) {
		response := env.get("/api/account/settings")
		return response, decodeEnvelope(t, response)
	})
	settingsUser, _ := dataOf(t, payload)["user"].(map[string]any)
	if settingsUser["role"] != "普通用户" || settingsUser["status"] != "正常" {
		t.Fatalf("settings user = %#v", settingsUser)
	}
	if settingsUser["register_source"] != "website" || settingsUser["last_login_ip"] != "127.0.0.1" {
		t.Fatalf("settings user = %#v", settingsUser)
	}
	if _, ok := settingsUser["date_joined"].(string); !ok {
		t.Fatalf("date_joined = %#v, want an ISO string", settingsUser["date_joined"])
	}

	status, payload, _ = env.call(func() (*http.Response, map[string]any) {
		return env.patchJSON("/api/account/settings", map[string]any{
			"nickname": "差异测试账号", "email": "diff@example.com",
			"avatar": "https://cdn.example.com/avatar.png", "remark": "差分验证",
		})
	})
	if status != http.StatusOK || messageOf(t, payload) != "资料已更新" {
		t.Fatalf("patch -> %d %s", status, messageOf(t, payload))
	}
	patched, _ := dataOf(t, payload)["user"].(map[string]any)
	if patched["nickname"] != "差异测试账号" || patched["avatar"] != "https://cdn.example.com/avatar.png" {
		t.Fatalf("patched user = %#v", patched)
	}

	for _, testCase := range []struct {
		name, want string
		payload    map[string]any
	}{
		{"empty nickname", "昵称不能为空", map[string]any{"nickname": ""}},
		{"taken email", "邮箱已存在", map[string]any{"nickname": "x", "email": "other@example.com"}},
		{"bad avatar", "头像地址格式不正确", map[string]any{"nickname": "x", "avatar": "not-a-url"}},
	} {
		status, payload, _ = env.call(func() (*http.Response, map[string]any) {
			return env.patchJSON("/api/account/settings", testCase.payload)
		})
		if status != http.StatusBadRequest || messageOf(t, payload) != testCase.want {
			t.Fatalf("%s -> %d %q, want %q", testCase.name, status, messageOf(t, payload), testCase.want)
		}
	}

	status, payload, _ = env.call(func() (*http.Response, map[string]any) {
		return env.postJSON("/api/account/password", map[string]any{
			"current_password": "wrong", "new_password": "DiffPass456!@#", "confirm_password": "DiffPass456!@#",
		})
	})
	if status != http.StatusBadRequest || messageOf(t, payload) != "当前密码不正确" {
		t.Fatalf("wrong current password -> %d %q", status, messageOf(t, payload))
	}
	status, payload, _ = env.call(func() (*http.Response, map[string]any) {
		return env.postJSON("/api/account/password", map[string]any{
			"current_password": "DiffPass123!@#", "new_password": "DiffPass456!@#", "confirm_password": "mismatch",
		})
	})
	if status != http.StatusBadRequest || messageOf(t, payload) != "两次密码输入不一致" {
		t.Fatalf("mismatch -> %d %q", status, messageOf(t, payload))
	}
	status, payload, _ = env.call(func() (*http.Response, map[string]any) {
		return env.postJSON("/api/account/password", map[string]any{
			"current_password": "DiffPass123!@#", "new_password": "12345678", "confirm_password": "12345678",
		})
	})
	if status != http.StatusBadRequest || messageOf(t, payload) != "这个密码太常见了。" {
		t.Fatalf("weak password -> %d %q", status, messageOf(t, payload))
	}
	status, payload, _ = env.call(func() (*http.Response, map[string]any) {
		return env.postJSON("/api/account/password", map[string]any{
			"current_password": "DiffPass123!@#", "new_password": "DiffPass456!@#", "confirm_password": "DiffPass456!@#",
		})
	})
	if status != http.StatusOK || messageOf(t, payload) != "密码已更新" {
		t.Fatalf("password change -> %d %q", status, messageOf(t, payload))
	}
	if _, err := env.svc.Authenticate(context.Background(), "diff-user", "DiffPass456!@#"); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
	// update_session_auth_hash keeps the session usable.
	status, _, _ = env.call(func() (*http.Response, map[string]any) {
		response := env.get("/api/account/settings")
		return response, decodeEnvelope(t, response)
	})
	if status != http.StatusOK {
		t.Fatalf("session did not survive the password change (status %d)", status)
	}

	// Logout: JSON content type gets the envelope, anything else a redirect.
	status, payload, _ = env.call(func() (*http.Response, map[string]any) {
		return env.postJSON("/api/auth/logout", map[string]any{})
	})
	if status != http.StatusOK {
		t.Fatalf("logout status = %d", status)
	}
	response = env.get("/api/account/summary")
	response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("summary after logout = %d, want 302", response.StatusCode)
	}
	response = env.request(http.MethodPost, "/api/auth/logout", "", "", true)
	response.Body.Close()
	if response.StatusCode != http.StatusFound || response.Header.Get("Location") != "/" {
		t.Fatalf("form logout = %d %q, want 302 /", response.StatusCode, response.Header.Get("Location"))
	}
}

// ── payload parsing error branches ──

func TestPayloadParsingErrorBranches(t *testing.T) {
	env := newTestEnv(t, envOptions{registerEnabled: true, captchaEnabled: true, emailVerification: true})
	env.refreshCSRF()
	cases := []struct {
		name string
		path string
		body string
		want string
	}{
		{"login malformed json", "/api/auth/login", "{bad-json", "请求 JSON 格式错误"},
		{"register non-object json", "/api/auth/register", "[1, 2]", "请求 JSON 必须是对象"},
		{"register null json", "/api/auth/register", "null", "请求 JSON 必须是对象"},
		{"register-check malformed json", "/api/auth/register/check", "{", "请求 JSON 格式错误"},
		{"email-code non-object json", "/api/auth/register/email-code", `"x"`, "请求 JSON 必须是对象"},
		{"reset-password malformed json", "/api/auth/reset-password", "not json", "请求 JSON 格式错误"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			status, payload, _ := env.call(func() (*http.Response, map[string]any) {
				response := env.request(http.MethodPost, testCase.path, testCase.body, "application/json", true)
				return response, decodeEnvelope(t, response)
			})
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", status)
			}
			if code, _ := payload["code"].(float64); int(code) != httpapi.CodeBadRequest {
				t.Fatalf("code = %v, want %d", payload["code"], httpapi.CodeBadRequest)
			}
			if got := messageOf(t, payload); got != testCase.want {
				t.Fatalf("message = %q, want %q", got, testCase.want)
			}
		})
	}
}

// ── bootstrap administrator ──

func TestEnsureAdminUser(t *testing.T) {
	env := newTestEnv(t, envOptions{registerEnabled: true, captchaEnabled: false})
	ctx := context.Background()

	created, err := env.svc.EnsureAdminUser(ctx, "admin", "DiffAdmin123!@#", "")
	if err != nil || !created {
		t.Fatalf("EnsureAdminUser = (%v, %v), want (true, nil)", created, err)
	}
	admin, err := env.svc.ByUsername(ctx, "admin")
	if err != nil {
		t.Fatalf("load admin: %v", err)
	}
	if !admin.IsStaff || !admin.IsSuperuser || admin.Role != RoleAdmin || admin.Status != StatusActive {
		t.Fatalf("admin flags = %#v", admin)
	}
	if admin.Email != "admin@example.com" {
		t.Fatalf("admin email = %q, want the default", admin.Email)
	}
	if ok, _ := VerifyPassword(admin.Password, "DiffAdmin123!@#"); !ok {
		t.Fatal("admin password does not verify")
	}
	profile, err := env.svc.ProfileOf(ctx, admin.IDValue)
	if err != nil || profile.Nickname != "admin" {
		t.Fatalf("admin profile = %#v (%v)", profile, err)
	}

	created, err = env.svc.EnsureAdminUser(ctx, "admin", "OtherPass123!@#", "ops@example.com")
	if err != nil || created {
		t.Fatalf("second EnsureAdminUser = (%v, %v), want (false, nil)", created, err)
	}
	updated, err := env.svc.ByUsername(ctx, "admin")
	if err != nil {
		t.Fatalf("reload admin: %v", err)
	}
	if updated.Email != "ops@example.com" {
		t.Fatalf("admin email = %q, want the updated value", updated.Email)
	}
	if ok, _ := VerifyPassword(updated.Password, "OtherPass123!@#"); !ok {
		t.Fatal("admin password was not reset")
	}
	var count int
	if err := env.db.QueryRow(`SELECT COUNT(1) FROM users_user WHERE username = 'admin'`).Scan(&count); err != nil {
		t.Fatalf("count admins: %v", err)
	}
	if count != 1 {
		t.Fatalf("admin rows = %d, want 1", count)
	}

	created, err = env.svc.EnsureAdminUser(ctx, "", "", "")
	if err != nil || created {
		t.Fatalf("empty EnsureAdminUser = (%v, %v), want (false, nil)", created, err)
	}
}

// ── store helpers ──

func TestRegisterStoresLowercasedEmailAndProfile(t *testing.T) {
	env := newTestEnv(t, envOptions{registerEnabled: true, captchaEnabled: false})
	user, err := env.svc.Register(context.Background(), "Mixed", "Mixed@Example.COM", "DiffPass123!@#", "203.0.113.5")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if user.Email != "mixed@example.com" {
		t.Fatalf("email = %q, want lowercased", user.Email)
	}
	profile, err := env.svc.ProfileOf(context.Background(), user.IDValue)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if profile.Nickname != "Mixed" || profile.RegisterIP != "203.0.113.5" || profile.RegisterSource != "website" {
		t.Fatalf("profile = %#v", profile)
	}
	var lastLogin sql.NullString
	var isActive, isStaff, isSuper int
	if err := env.db.QueryRow(
		`SELECT last_login, is_active, is_staff, is_superuser, last_login_at FROM users_user WHERE id = ?`, user.IDValue,
	).Scan(&lastLogin, &isActive, &isStaff, &isSuper, new(sql.NullString)); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if isActive != 1 || isStaff != 0 || isSuper != 0 {
		t.Fatalf("flags = active:%d staff:%d super:%d", isActive, isStaff, isSuper)
	}
	if lastLogin.Valid {
		t.Fatalf("register must not set last_login (got %q)", lastLogin.String)
	}
}

func TestEmailCodeRoundTrip(t *testing.T) {
	env := newTestEnv(t, envOptions{registerEnabled: true, captchaEnabled: false, emailVerification: true})
	store := env.svc.emailCodes
	store.storeCode("user@example.com", hashEmailCode("123456"), time.Minute)
	if err := env.svc.ValidateRegisterEmailCode("User@Example.com", "123456"); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := env.svc.ValidateRegisterEmailCode("user@example.com", "000000"); !errors.Is(err, ErrEmailCodeInvalid) {
		t.Fatalf("wrong code error = %v", err)
	}
	if err := env.svc.ConsumeRegisterEmailCode("user@example.com", "123456"); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if err := env.svc.ValidateRegisterEmailCode("user@example.com", "123456"); !errors.Is(err, ErrEmailCodeExpired) {
		t.Fatalf("consumed code error = %v, want expired", err)
	}
}

func containsCookie(response *http.Response, name string) bool {
	for _, cookie := range response.Cookies() {
		if cookie.Name == name {
			return true
		}
	}
	return false
}

func decodeRawBase64(value string) ([]byte, error) {
	return base64.RawStdEncoding.DecodeString(value)
}
