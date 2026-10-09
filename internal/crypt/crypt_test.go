package crypt

import (
	"encoding/base64"
	"strings"
	"testing"
)

// The vectors below were produced by the reference implementation
// (python's cryptography.Fernet in .venv-ref), so they pin the key schedule in
// both directions: a canonical 44-character key must be used verbatim, and a
// short key must be sha256-derived. Hashing an already-canonical key a second
// time yields a working cipher in isolation but one the reference cannot read,
// which is what this test exists to prevent.
const (
	referenceCanonicalKey = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	referenceShortKey     = "diff-field-crypt-key"
	referencePlaintext    = "sk-test-channel-key"
	// Fernet(referenceCanonicalKey).encrypt(b"sk-test-channel-key")
	referenceCanonicalToken = "gAAAAABqyRKsUoqMZOSnV47W3nz7m8KuLn_UfdqoVUxtoOJtW6OKCcVtmGlSRuys11_KuXSZLWkBQqjovzc00I03et6MfYwkju97e7wsAfxgQJZxTy76T1E="
	// Fernet(b64(sha256(b"diff-field-crypt-key"))).encrypt(b"sk-test-channel-key")
	referenceShortKeyToken = "gAAAAABqyRKsUU0rn8uQ7ghrCnvLBM983Wz9rJKieP4lD6yUpDwqUCCWhtCIwMMSrAGL8ooRBK2AOW9Emg_jbhXIcgncRcxGB3SkRegCOKCZsSD9UxD24-k="
)

func TestDeriveFernetKeyUsesCanonicalKeysVerbatim(t *testing.T) {
	if got := string(DeriveFernetKey(referenceCanonicalKey)); got != referenceCanonicalKey {
		t.Fatalf("canonical key was re-derived:\n got %q\nwant %q", got, referenceCanonicalKey)
	}
}

func TestDeriveFernetKeyHashesShortKeys(t *testing.T) {
	derived := string(DeriveFernetKey(referenceShortKey))
	if derived == referenceShortKey {
		t.Fatal("a short key must not be used as-is")
	}
	decoded, err := base64.URLEncoding.DecodeString(derived)
	if err != nil || len(decoded) != 32 {
		t.Fatalf("derived key is not a 32-byte urlsafe base64 key: %q (%v)", derived, err)
	}
	// Stable across calls; the same value must be produced every time.
	if second := string(DeriveFernetKey(referenceShortKey)); second != derived {
		t.Fatalf("derivation is not deterministic: %q vs %q", derived, second)
	}
}

func TestDecryptsReferenceCiphertext(t *testing.T) {
	for name, testCase := range map[string]struct {
		key   string
		token string
	}{
		"canonical key": {referenceCanonicalKey, referenceCanonicalToken},
		"short key":     {referenceShortKey, referenceShortKeyToken},
	} {
		t.Run(name, func(t *testing.T) {
			cipher, err := NewFieldCipher(testCase.key)
			if err != nil {
				t.Fatalf("NewFieldCipher: %v", err)
			}
			plain, err := cipher.Decrypt(testCase.token)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if plain != referencePlaintext {
				t.Fatalf("plaintext = %q, want %q", plain, referencePlaintext)
			}
		})
	}
}

func TestEncryptedValuesAreBareFernetTokens(t *testing.T) {
	cipher, err := NewFieldCipher(referenceCanonicalKey)
	if err != nil {
		t.Fatalf("NewFieldCipher: %v", err)
	}
	token, err := cipher.Encrypt(referencePlaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if strings.Contains(token, ":") {
		t.Fatalf("token carries a wrapper the reference does not write: %q", token)
	}
	if !strings.HasPrefix(token, "gAAAAA") {
		t.Fatalf("token does not look like a Fernet token: %q", token)
	}
	if parts := strings.Split(token, "="); len(parts) > 2 {
		t.Fatalf("unexpected padding in %q", token)
	}
	plain, err := cipher.Decrypt(token)
	if err != nil || plain != referencePlaintext {
		t.Fatalf("round trip failed: %q (%v)", plain, err)
	}
}

func TestEncryptEmptyPlaintextStaysEmpty(t *testing.T) {
	cipher, err := NewFieldCipher(referenceShortKey)
	if err != nil {
		t.Fatalf("NewFieldCipher: %v", err)
	}
	token, err := cipher.Encrypt("")
	if err != nil || token != "" {
		t.Fatalf("Encrypt(\"\") = %q, %v; want empty", token, err)
	}
	if plain, err := cipher.Decrypt(""); err != nil || plain != "" {
		t.Fatalf("Decrypt(\"\") = %q, %v; want empty", plain, err)
	}
}

func TestCipherRejectsForeignCiphertext(t *testing.T) {
	cipher, err := NewFieldCipher(referenceShortKey)
	if err != nil {
		t.Fatalf("NewFieldCipher: %v", err)
	}
	if _, err := cipher.Decrypt(referenceCanonicalToken); err == nil {
		t.Fatal("a ciphertext from another key must not decrypt")
	}
}

func TestNewFieldCipherRequiresAKey(t *testing.T) {
	if _, err := NewFieldCipher(""); err == nil {
		t.Fatal("an empty FIELD_CRYPT_KEY must be refused, not treated as plaintext storage")
	}
	if _, err := NewFieldCipher("   "); err == nil {
		t.Fatal("a blank FIELD_CRYPT_KEY must be refused")
	}
}
