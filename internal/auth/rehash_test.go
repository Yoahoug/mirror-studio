package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/pbkdf2"
)

// TestAuthenticateRehashesLegacyPBKDF2 reproduces Django's must_update path: a
// login that verifies a pbkdf2_sha256 hash upgrades the stored hash to the
// current argon2 parameters and the same password keeps working.
func TestAuthenticateRehashesLegacyPBKDF2(t *testing.T) {
	env := newTestEnv(t, envOptions{registerEnabled: true, captchaEnabled: false})
	ctx := context.Background()
	if _, err := env.svc.Register(ctx, "legacy-user", "legacy@example.com", "DiffPass123!@#", "127.0.0.1"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	// Downgrade the stored hash to a pbkdf2_sha256 value Django would have written.
	legacy := "pbkdf2_sha256$870000$" + "saltedsaltedsalted" + "$" + pbkdf2Digest(t, "DiffPass123!@#", "saltedsaltedsalted", 870000)
	if _, err := env.db.Exec(`UPDATE users_user SET password = ? WHERE username = 'legacy-user'`, legacy); err != nil {
		t.Fatalf("downgrade hash: %v", err)
	}

	if _, err := env.svc.Authenticate(ctx, "legacy-user", "DiffPass123!@#"); err != nil {
		t.Fatalf("authenticate legacy user: %v", err)
	}
	var stored string
	if err := env.db.QueryRow(`SELECT password FROM users_user WHERE username = 'legacy-user'`).Scan(&stored); err != nil {
		t.Fatalf("read upgraded hash: %v", err)
	}
	if !strings.HasPrefix(stored, "argon2$argon2id$v=19$m=102400,t=2,p=8$") {
		t.Fatalf("stored hash was not upgraded: %.40s", stored)
	}
	if _, err := env.svc.Authenticate(ctx, "legacy-user", "DiffPass123!@#"); err != nil {
		t.Fatalf("upgraded hash must verify with the same password: %v", err)
	}
}

// TestEmailCodeSurvivesWrongGuess pins the reference's consume order: the code
// is checked first and only deleted once it matched.
func TestEmailCodeSurvivesWrongGuess(t *testing.T) {
	env := newTestEnv(t, envOptions{registerEnabled: true, captchaEnabled: false, emailVerification: true})
	env2 := env
	env2.svc.emailCodes.storeCode("diff@example.com", hashEmailCode("123456"), time.Minute)
	if err := env2.svc.ConsumeRegisterEmailCode("diff@example.com", "000000"); err != ErrEmailCodeInvalid {
		t.Fatalf("wrong guess error = %v, want ErrEmailCodeInvalid", err)
	}
	// The still-valid code must verify after the failed guess.
	if err := env2.svc.ConsumeRegisterEmailCode("diff@example.com", "123456"); err != nil {
		t.Fatalf("valid code burned by a wrong guess: %v", err)
	}
	// And the successful validation consumed it.
	if err := env2.svc.ConsumeRegisterEmailCode("diff@example.com", "123456"); err != ErrEmailCodeExpired {
		t.Fatalf("second validation error = %v, want ErrEmailCodeExpired", err)
	}
}

func pbkdf2Digest(t *testing.T, password, salt string, iterations int) string {
	t.Helper()
	digest := pbkdf2.Key([]byte(password), []byte(salt), iterations, 32, sha256.New)
	return base64.StdEncoding.EncodeToString(digest)
}
