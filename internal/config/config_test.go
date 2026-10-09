package config

import (
	"os"
	"strings"
	"testing"
)

// prodEnv assembles the minimum environment a non-debug boot needs.
func prodEnv(extra map[string]string) map[string]string {
	env := map[string]string{
		"SECRET_KEY":      "0123456789abcdef0123456789abcdef",
		"FIELD_CRYPT_KEY": "diff-field-crypt-key",
		"PUBLIC_ORIGIN":   "https://image.example.com",
		"DATABASE_URL":    "sqlite:///db.sqlite3",
	}
	for key, value := range extra {
		env[key] = value
	}
	return env
}

func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, key := range []string{
		"SECRET_KEY", "FIELD_CRYPT_KEY", "PUBLIC_ORIGIN", "SITE_DOMAIN", "DATABASE_URL",
		"DEBUG", "MOCK_PROVIDER_ENABLED", "ALLOW_INSECURE_PROD", "CONSOLE_HMAC_ENABLED",
		"GENERATION_TASKS_RUN_INLINE", "NOVELVIZ_RUN_INLINE", "MINIO_ENDPOINT",
		"TIME_ZONE", "APP_RELEASE_FALLBACK_VERSION",
	} {
		t.Setenv(key, "")
	}
	for key, value := range env {
		t.Setenv(key, value)
	}
	t.Chdir(t.TempDir())
	if err := os.WriteFile("CHANGELOG.md", []byte("# 更新说明\n\n## 0.0.1 - 2026-10-10\n\n- 测试\n"), 0o644); err != nil {
		t.Fatalf("write changelog: %v", err)
	}
}

func TestLoadMockProviderDefaultsOff(t *testing.T) {
	setEnv(t, prodEnv(nil))
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MockProviderEnabled {
		t.Fatal("MOCK_PROVIDER_ENABLED must default to false")
	}
}

func TestLoadRejectsMockProviderInProduction(t *testing.T) {
	setEnv(t, prodEnv(map[string]string{"MOCK_PROVIDER_ENABLED": "1"}))
	if _, err := Load(); err == nil {
		t.Fatal("production boot with MOCK_PROVIDER_ENABLED=1 must fail")
	} else if !strings.Contains(err.Error(), "MOCK_PROVIDER_ENABLED") {
		t.Fatalf("error should name the switch, got %v", err)
	}
}

func TestLoadAllowsMockProviderInDebug(t *testing.T) {
	setEnv(t, prodEnv(map[string]string{"MOCK_PROVIDER_ENABLED": "1", "DEBUG": "1"}))
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.MockProviderEnabled {
		t.Fatal("debug boot should honour MOCK_PROVIDER_ENABLED=1")
	}
}

func TestLoadSecureSSLRedirectDefaults(t *testing.T) {
	setEnv(t, prodEnv(nil))
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.SecureSSLRedirect {
		t.Fatal("SECURE_SSL_REDIRECT must default to true outside DEBUG")
	}
	setEnv(t, prodEnv(map[string]string{"DEBUG": "1"}))
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SecureSSLRedirect {
		t.Fatal("SECURE_SSL_REDIRECT must default to false in DEBUG")
	}
}

func TestLoadRejectsRemovedSwitches(t *testing.T) {
	for _, name := range []string{
		"ALLOW_INSECURE_PROD", "CONSOLE_HMAC_ENABLED",
		"GENERATION_TASKS_RUN_INLINE", "NOVELVIZ_RUN_INLINE",
	} {
		setEnv(t, prodEnv(map[string]string{name: "1"}))
		if _, err := Load(); err == nil {
			t.Fatalf("%s=1 must refuse to boot", name)
		} else if !strings.Contains(err.Error(), name) {
			t.Fatalf("error should name %s, got %v", name, err)
		}
	}
}

func TestLoadRejectsMinioStorage(t *testing.T) {
	setEnv(t, prodEnv(map[string]string{"MINIO_ENDPOINT": "http://127.0.0.1:9000"}))
	if _, err := Load(); err == nil {
		t.Fatal("MINIO_* storage must refuse to boot")
	} else if !strings.Contains(err.Error(), "MINIO") {
		t.Fatalf("error should name MINIO, got %v", err)
	}
}
