// Package siteconfig resolves runtime settings. The lookup order is always
// database row, then environment variable, then compiled-in default, which is
// what makes the console's configuration page authoritative: rows written there
// shadow the deployment's environment.
package siteconfig

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"mirror-studio/internal/crypt"
	"mirror-studio/internal/db"
)

// Categories used by the console's configuration page.
const (
	CategoryGeneral = "general"
	CategorySite    = "site"
	CategoryEmail   = "email"
	CategoryRisk    = "risk"
)

// CategoryLabels maps category codes to the Chinese labels the console shows.
var CategoryLabels = []struct {
	Code  string
	Label string
}{
	{CategoryGeneral, "通用"},
	{CategorySite, "站点"},
	{CategoryEmail, "邮件验证"},
	{CategoryRisk, "风控"},
}

// Entry describes one known configuration key.
type Entry struct {
	Key         string
	Category    string
	Secret      bool
	Description string
}

// Keys is the registry of editable configuration, in the order rows are seeded
// and therefore the order they appear in the database.
var Keys = []Entry{
	{"site.name", CategorySite, false, "站点名称"},
	{"site.brand_name", CategorySite, false, "品牌中文名"},
	{"site.brand_english_name", CategorySite, false, "品牌英文名"},
	{"site.brand_tagline", CategorySite, false, "品牌标语"},
	{"site.register_enabled", CategorySite, false, "是否开放注册 (1/0)"},
	{"site.login_captcha_enabled", CategorySite, false, "是否启用登录验证码 (1/0)"},
	{"site.image_retention_days", CategorySite, false, "图片保留天数"},
	{"site.daily_user_task_limit", CategorySite, false, "每日每用户任务上限"},
	{"site.max_pending_tasks", CategorySite, false, "最大并行任务数"},
	{"site.default_channel_code", CategorySite, false, "默认生图渠道编码"},
	{"site.default_daily_free_quota", CategorySite, false, "默认每日免费次数"},
	{"site.workspace_release_version", CategorySite, false, ""},
	{"site.workspace_release_title", CategorySite, false, ""},
	{"site.workspace_release_notes", CategorySite, false, ""},
	{"site.workspace_release_published_at", CategorySite, false, ""},
	{"email.verification_enabled", CategoryEmail, false, "是否启用邮箱验证 (1/0)"},
	{"email.verification_code_length", CategoryEmail, false, "验证码长度"},
	{"email.verification_ttl_seconds", CategoryEmail, false, "验证码有效期(秒)"},
	{"email.verification_resend_seconds", CategoryEmail, false, "验证码重发间隔(秒)"},
	{"email.daily_limit_per_email", CategoryEmail, false, "每邮箱每日发送上限"},
	{"email.daily_limit_per_ip", CategoryEmail, false, "每 IP 每日发送上限"},
	{"risk.login_fail_limit", CategoryRisk, false, "登录失败锁定阈值"},
	{"risk.login_lock_minutes", CategoryRisk, false, "登录锁定分钟数"},
	{"risk.task_window_seconds", CategoryRisk, false, "任务频率窗口(秒)"},
	{"risk.task_limit_per_window", CategoryRisk, false, "窗口内任务上限"},
}

func entryFor(key string) (Entry, bool) {
	for _, entry := range Keys {
		if entry.Key == key {
			return entry, true
		}
	}
	return Entry{}, false
}

// Row is a configuration row as the console renders it.
type Row struct {
	ID          int64
	Key         string
	Value       string
	IsSecret    bool
	Description string
	Category    string
	UpdatedAt   string
}

// Masked returns the value the console may display.
func (r Row) Masked() string {
	if r.IsSecret && r.Value != "" {
		return "******"
	}
	return r.Value
}

// Service reads and writes configuration with a short-lived cache.
type Service struct {
	DB     *db.DB
	Cipher *crypt.FieldCipher
	Logger *slog.Logger
	// Env reads a deployment environment variable. It is consulted only when no
	// database row holds a value.
	Env func(string) string

	mu    sync.RWMutex
	cache map[string]cached
	ttl   time.Duration
}

type cached struct {
	value   string
	present bool
	at      time.Time
}

// New builds a configuration service.
func New(handle *db.DB, cipher *crypt.FieldCipher, logger *slog.Logger) *Service {
	return &Service{DB: handle, Cipher: cipher, Logger: logger, cache: map[string]cached{}, ttl: 60 * time.Second}
}

// Lookup returns the raw stored value, whether a row exists, and any error.
func (s *Service) Lookup(key string) (string, bool, error) {
	s.mu.RLock()
	if hit, ok := s.cache[key]; ok && time.Since(hit.at) < s.ttl {
		s.mu.RUnlock()
		return hit.value, hit.present, nil
	}
	s.mu.RUnlock()

	row, found, err := s.Get(key)
	if err != nil {
		return "", false, err
	}
	value := ""
	if found {
		value = row.Value
	}
	s.mu.Lock()
	s.cache[key] = cached{value: value, present: found, at: time.Now()}
	s.mu.Unlock()
	return value, found, nil
}

// Get loads a row without consulting the cache.
func (s *Service) Get(key string) (Row, bool, error) {
	var (
		id          int64
		stored      sql.NullString
		isSecret    int
		description sql.NullString
		category    sql.NullString
		updatedAt   sql.NullString
	)
	err := s.DB.QueryRow(
		`SELECT id, value_encrypted, is_secret, description, category, updated_at FROM core_siteconfig WHERE key = ?`,
		key,
	).Scan(&id, &stored, &isSecret, &description, &category, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Row{}, false, nil
	}
	if err != nil {
		return Row{}, false, err
	}
	value := stored.String
	if isSecret == 1 {
		plain, err := s.Cipher.Decrypt(value)
		if err != nil {
			s.logDecryptFailure(key, err)
			return Row{}, false, err
		}
		value = plain
	}
	return Row{
		ID:          id,
		Key:         key,
		Value:       value,
		IsSecret:    isSecret == 1,
		Description: description.String,
		Category:    category.String,
		UpdatedAt:   updatedAt.String,
	}, true, nil
}

// String resolves a key, falling back to the environment and then to def.
func (s *Service) String(key, def string) string {
	value, present, err := s.Lookup(key)
	if err == nil && present && strings.TrimSpace(value) != "" {
		return value
	}
	if s.Env != nil {
		if fromEnv := strings.TrimSpace(s.Env(key)); fromEnv != "" {
			return fromEnv
		}
	}
	return def
}

// Bool resolves a boolean key with a named default.
func (s *Service) Bool(key string, def bool) bool {
	value, present, err := s.Lookup(key)
	if err != nil || !present || strings.TrimSpace(value) == "" {
		if s.Env != nil {
			if fromEnv := strings.TrimSpace(s.Env(key)); fromEnv != "" {
				return Bool(fromEnv, def)
			}
		}
		return def
	}
	return Bool(value, def)
}

// Int resolves an integer key with a named default.
func (s *Service) Int(key string, def int) int {
	value, present, err := s.Lookup(key)
	if err != nil || !present || strings.TrimSpace(value) == "" {
		if s.Env != nil {
			if fromEnv := strings.TrimSpace(s.Env(key)); fromEnv != "" {
				return Int(fromEnv, def)
			}
		}
		return def
	}
	return Int(value, def)
}

// Bool resolves a boolean key using the same 1/true/yes/on spelling as the
// reference implementation.
func Bool(value string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

// Int parses an integer, falling back to def when the value is empty or bad.
func Int(value string, def int) int {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return def
	}
	parsed, err := strconv.Atoi(trimmed)
	if err != nil {
		return def
	}
	return parsed
}

// Set writes a value and invalidates the cache entry.
func (s *Service) Set(key, value string, isSecret *bool, description, category *string) (Row, error) {
	entry, known := entryFor(key)
	secret := false
	desc := ""
	cat := CategoryGeneral
	if known {
		secret = entry.Secret
		desc = entry.Description
		cat = entry.Category
	}
	if isSecret != nil {
		secret = *isSecret
	}
	if description != nil {
		desc = *description
	}
	if category != nil {
		cat = *category
	}

	stored := value
	if secret {
		encrypted, err := s.Cipher.Encrypt(value)
		if err != nil {
			return Row{}, err
		}
		stored = encrypted
	}

	now := db.FormatTime(time.Now())
	if _, err := s.DB.Exec(
		`INSERT INTO core_siteconfig (created_at, updated_at, key, value_encrypted, is_secret, description, category)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET
		     value_encrypted = excluded.value_encrypted,
		     is_secret = excluded.is_secret,
		     description = CASE WHEN ? IS NULL THEN core_siteconfig.description ELSE excluded.description END,
		     category = CASE WHEN ? IS NULL THEN core_siteconfig.category ELSE excluded.category END,
		     updated_at = excluded.updated_at`,
		now, now, key, stored, db.Bool(secret), desc, cat,
		nullable(description), nullable(category),
	); err != nil {
		return Row{}, err
	}
	s.Invalidate(key)
	row, _, err := s.Get(key)
	return row, err
}

func nullable(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

// Invalidate drops the cached value for a key, or the whole cache when key is
// empty.
func (s *Service) Invalidate(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == "" {
		s.cache = map[string]cached{}
		return
	}
	delete(s.cache, key)
}

// List returns rows for the console's configuration page.
func (s *Service) List(category string) ([]Row, error) {
	query := `SELECT id, key, value_encrypted, is_secret, description, category, updated_at FROM core_siteconfig`
	args := []any{}
	if category != "" {
		query += ` WHERE category = ?`
		args = append(args, category)
	}
	query += ` ORDER BY category, key`

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []Row{}
	for rows.Next() {
		var (
			row         Row
			stored      sql.NullString
			isSecret    int
			description sql.NullString
			categoryOut sql.NullString
			updatedAt   sql.NullString
		)
		if err := rows.Scan(&row.ID, &row.Key, &stored, &isSecret, &description, &categoryOut, &updatedAt); err != nil {
			return nil, err
		}
		row.IsSecret = isSecret == 1
		row.Description = description.String
		row.Category = categoryOut.String
		row.UpdatedAt = updatedAt.String
		row.Value = stored.String
		if row.IsSecret {
			plain, err := s.Cipher.Decrypt(row.Value)
			if err != nil {
				s.logDecryptFailure(row.Key, err)
				plain = ""
			}
			row.Value = plain
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// SeedDefaults inserts one row per registered key that has no stored value yet.
// It is idempotent and runs on every boot.
func (s *Service) SeedDefaults(defaults map[string]string) (int, error) {
	created := 0
	for _, entry := range Keys {
		var stored sql.NullString
		err := s.DB.QueryRow(`SELECT value_encrypted FROM core_siteconfig WHERE key = ?`, entry.Key).Scan(&stored)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return created, err
		}
		if err == nil && strings.TrimSpace(stored.String) != "" {
			continue
		}
		value := defaults[entry.Key]
		secret := entry.Secret
		description := entry.Description
		category := entry.Category
		if _, err := s.Set(entry.Key, value, &secret, &description, &category); err != nil {
			return created, err
		}
		created++
	}
	s.Invalidate("")
	return created, nil
}

func (s *Service) logDecryptFailure(key string, err error) {
	if s.Logger != nil {
		s.Logger.Error("site config decryption failed", "key", key, "error", err)
	}
}

// EncodeBool renders a boolean the way the console stores it.
func EncodeBool(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

// EncodeJSON renders a value for storage in a JSON text column.
func EncodeJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "null"
	}
	return string(encoded)
}
