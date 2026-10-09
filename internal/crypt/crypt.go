// Package crypt implements the two cryptographic containers the reference
// implementation stores and signs with:
//
//   - Fernet (spec-compatible, as produced by python's cryptography.Fernet),
//     used to encrypt channel API keys at rest.
//   - Django's Signer / TimestampSigner, used for download tokens and the
//     reference-image upload token. The output shape is part of the public
//     contract (the differential harness masks tokens with a regex that expects
//     "<payload>:<base62 timestamp>:<base64 signature>").
package crypt

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"strings"
	"time"
)

// ── base64url without padding (python's urlsafe_b64encode().strip("=")) ──

var b64url = base64.URLEncoding.WithPadding(base64.NoPadding)

func b64Encode(data []byte) string { return b64url.EncodeToString(data) }

func b64Decode(value string) ([]byte, error) {
	trimmed := strings.TrimRight(value, "=")
	return b64url.DecodeString(trimmed)
}

// ── Fernet ──

const fernetVersion = 0x80

// DeriveFernetKey reproduces the reference key derivation and returns the
// *encoded* 44-character key both implementations feed to Fernet:
//
//   - a 44-character urlsafe base64 key that decodes to 32 bytes is already a
//     Fernet key and is used verbatim (python's `Fernet(key.encode())`);
//   - anything else is hashed with SHA-256 and base64-encoded
//     (`Fernet(base64.urlsafe_b64encode(sha256(key).digest()))`).
//
// Returning the decoded bytes for the first case would be wrong: the caller
// would then hash an already-canonical key a second time and derive a key the
// reference cannot reproduce.
func DeriveFernetKey(raw string) []byte {
	if len(raw) == 44 {
		if decoded, err := base64.URLEncoding.DecodeString(raw); err == nil && len(decoded) == 32 {
			return []byte(raw)
		}
	}
	sum := sha256.Sum256([]byte(raw))
	return []byte(base64.URLEncoding.EncodeToString(sum[:]))
}

type fernetCipher struct {
	signingKey []byte
	encKey     []byte
}

func newFernet(rawKey string) (*fernetCipher, error) {
	derived := DeriveFernetKey(rawKey)
	decoded, err := base64.URLEncoding.DecodeString(string(derived))
	if err != nil {
		return nil, fmt.Errorf("invalid FIELD_CRYPT_KEY: %w", err)
	}
	if len(decoded) != 32 {
		return nil, fmt.Errorf("invalid FIELD_CRYPT_KEY: derived key is %d bytes, want 32", len(decoded))
	}
	return &fernetCipher{signingKey: decoded[:16], encKey: decoded[16:]}, nil
}

func (c *fernetCipher) encrypt(plaintext []byte) (string, error) {
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	block, err := aes.NewCipher(c.encKey)
	if err != nil {
		return "", err
	}
	padded := pkcs7Pad(plaintext, aes.BlockSize)
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)

	token := make([]byte, 0, 1+8+len(iv)+len(ciphertext)+sha256.Size)
	token = append(token, fernetVersion)
	ts := make([]byte, 8)
	binary.BigEndian.PutUint64(ts, uint64(time.Now().Unix()))
	token = append(token, ts...)
	token = append(token, iv...)
	token = append(token, ciphertext...)

	mac := hmac.New(sha256.New, c.signingKey)
	mac.Write(token)
	token = append(token, mac.Sum(nil)...)
	return base64.URLEncoding.EncodeToString(token), nil
}

func (c *fernetCipher) decrypt(token string) ([]byte, error) {
	raw, err := base64.URLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return nil, errors.New("ciphertext is not valid base64")
	}
	if len(raw) < 1+8+aes.BlockSize+sha256.Size {
		return nil, errors.New("ciphertext is too short")
	}
	body, sig := raw[:len(raw)-sha256.Size], raw[len(raw)-sha256.Size:]
	mac := hmac.New(sha256.New, c.signingKey)
	mac.Write(body)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return nil, errors.New("ciphertext signature mismatch")
	}
	if body[0] != fernetVersion {
		return nil, fmt.Errorf("unknown ciphertext version 0x%02x", body[0])
	}
	iv := body[9 : 9+aes.BlockSize]
	ciphertext := body[9+aes.BlockSize:]
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, errors.New("ciphertext length is not a multiple of the block size")
	}
	block, err := aes.NewCipher(c.encKey)
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext)
	return pkcs7Unpad(plain, aes.BlockSize)
}

func pkcs7Pad(data []byte, size int) []byte {
	pad := size - len(data)%size
	return append(append([]byte{}, data...), bytes.Repeat([]byte{byte(pad)}, pad)...)
}

func pkcs7Unpad(data []byte, size int) ([]byte, error) {
	if len(data) == 0 || len(data)%size != 0 {
		return nil, errors.New("invalid padding")
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > size || pad > len(data) {
		return nil, errors.New("invalid padding")
	}
	for _, b := range data[len(data)-pad:] {
		if int(b) != pad {
			return nil, errors.New("invalid padding")
		}
	}
	return data[:len(data)-pad], nil
}

// FieldCipher encrypts and decrypts values that live in the database.
//
// The stored form is exactly the reference's: a bare Fernet token, no wrapper,
// so a value written by either implementation is readable by the other.
type FieldCipher struct {
	fernet *fernetCipher
}

// NewFieldCipher builds a cipher from FIELD_CRYPT_KEY. An empty key is a hard
// error: silently storing plaintext is exactly the defect 0.0.1 removes.
func NewFieldCipher(key string) (*FieldCipher, error) {
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("FIELD_CRYPT_KEY is required")
	}
	inner, err := newFernet(key)
	if err != nil {
		return nil, err
	}
	return &FieldCipher{fernet: inner}, nil
}

// Encrypt returns "" for an empty plaintext, matching the reference behaviour.
func (c *FieldCipher) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	token, err := c.fernet.encrypt([]byte(plaintext))
	if err != nil {
		return "", err
	}
	return token, nil
}

// Decrypt returns "" for an empty value. A value that cannot be decrypted
// yields an error instead of being echoed back as if it were plaintext.
func (c *FieldCipher) Decrypt(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	plain, err := c.fernet.decrypt(value)
	if err != nil {
		return "", fmt.Errorf("decrypt stored value: %w", err)
	}
	return string(plain), nil
}

// ── Django signing ──

const defaultSignerSalt = "django.core.signing.Signer"
const defaultTimestampSignerSalt = "django.core.signing.TimestampSigner"

// BadSignature reports a token whose signature does not match.
var BadSignature = errors.New("signature does not match")

// SignatureExpired reports a valid signature that is older than allowed.
var SignatureExpired = errors.New("signature has expired")

// Signer mirrors django.core.signing.Signer.
type Signer struct {
	key      string
	salt     string
	sep      string
	hashFunc func() hash.Hash
}

// NewSigner builds a signer with the SHA-256 HMAC the reference uses.
func NewSigner(key, salt string) *Signer {
	if salt == "" {
		salt = defaultSignerSalt
	}
	return &Signer{key: key, salt: salt, sep: ":", hashFunc: sha256.New}
}

// saltedHMAC mirrors django.utils.crypto.salted_hmac with algorithm="sha256".
func saltedHMAC(keySalt, value, secret string) []byte {
	hasher := sha256.New()
	hasher.Write([]byte(keySalt))
	hasher.Write([]byte(secret))
	key := hasher.Sum(nil)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(value))
	return mac.Sum(nil)
}

func (s *Signer) signature(value string) string {
	return b64Encode(saltedHMAC(s.salt+"signer", value, s.key))
}

// Sign returns "<value>:<signature>".
func (s *Signer) Sign(value string) string {
	return value + s.sep + s.signature(value)
}

// Unsign verifies and returns the original value.
func (s *Signer) Unsign(signed string) (string, error) {
	index := strings.LastIndex(signed, s.sep)
	if index < 0 {
		return "", BadSignature
	}
	value, sig := signed[:index], signed[index+len(s.sep):]
	if !hmac.Equal([]byte(sig), []byte(s.signature(value))) {
		return "", BadSignature
	}
	return value, nil
}

// TimestampSigner mirrors django.core.signing.TimestampSigner, including the
// base62 timestamp encoding introduced in Django 5.
type TimestampSigner struct {
	*Signer
}

// NewTimestampSigner builds a timestamped signer.
func NewTimestampSigner(key, salt string) *TimestampSigner {
	if salt == "" {
		salt = defaultTimestampSignerSalt
	}
	return &TimestampSigner{Signer: NewSigner(key, salt)}
}

const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func b62Encode(value int64) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	out := []byte{}
	for value > 0 {
		out = append([]byte{base62Alphabet[value%62]}, out...)
		value /= 62
	}
	if negative {
		return "-" + string(out)
	}
	return string(out)
}

func b62Decode(value string) (int64, error) {
	negative := strings.HasPrefix(value, "-")
	if negative {
		value = value[1:]
	}
	var result int64
	for _, char := range value {
		index := strings.IndexRune(base62Alphabet, char)
		if index < 0 {
			return 0, fmt.Errorf("invalid base62 digit %q", char)
		}
		result = result*62 + int64(index)
	}
	if negative {
		return -result, nil
	}
	return result, nil
}

// Sign returns "<value>:<base62 timestamp>:<signature>".
func (s *TimestampSigner) Sign(value string) string {
	return s.Signer.Sign(value + s.Signer.sep + b62Encode(time.Now().Unix()))
}

// Unsign verifies the signature and, when maxAge is positive, the age.
// The returned payload is "<value>:<base62 timestamp>".
func (s *TimestampSigner) Unsign(signed string, maxAge time.Duration) (string, error) {
	unsigned, err := s.Signer.Unsign(signed)
	if err != nil {
		return "", err
	}
	index := strings.LastIndex(unsigned, s.Signer.sep)
	if index < 0 {
		return "", BadSignature
	}
	encodedTimestamp := unsigned[index+1:]
	timestamp, err := b62Decode(encodedTimestamp)
	if err != nil {
		return "", BadSignature
	}
	if maxAge > 0 {
		age := time.Since(time.Unix(timestamp, 0))
		if age > maxAge {
			return "", SignatureExpired
		}
	}
	return unsigned, nil
}
