// Package auth owns the users domain: account records, password hashing and
// validation, the login/session plumbing, email-verification codes and the
// /api/auth and /api/account JSON endpoints.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/pbkdf2"

	"mirror-studio/internal/config"
	"mirror-studio/internal/crypt"
	"mirror-studio/internal/db"
	"mirror-studio/internal/httpapi"
	"mirror-studio/internal/siteconfig"
)

// Roles and statuses as stored in users_user.
const (
	RoleUser  = "user"
	RoleAdmin = "admin"

	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusLocked   = "locked"
)

// Session keys the reference implementation uses.
const (
	SessionUserIDKey  = "user_id"
	SessionCaptchaKey = "captcha_code"
)

// User is the account record. It satisfies the parts of httpapi.Principal that
// do not collide with field names (ID, IsOperator, IsActiveStatus); the
// Username()/Email() methods live on Principal because a struct cannot declare
// a method with the same name as one of its fields.
type User struct {
	IDValue      int64
	Username     string
	Email        string
	Password     string
	Role         string
	Status       string
	IsStaff      bool
	IsSuperuser  bool
	DateJoined   time.Time
	LastLogin    *time.Time
	LastLoginIP  string
	LastLoginAt  *time.Time
	IsActiveFlag bool
}

// ID returns the primary key.
func (u *User) ID() int64 { return u.IDValue }

// IsOperator reports staff or administrator access.
func (u *User) IsOperator() bool { return u.IsStaff || u.Role == RoleAdmin }

// IsActiveStatus reports the account status flag (not is_active).
func (u *User) IsActiveStatus() bool { return u.Status == StatusActive }

// DisplayRole is the localised role label used by /api/account/settings.
func (u *User) DisplayRole() string {
	switch u.Role {
	case RoleUser:
		return "普通用户"
	case RoleAdmin:
		return "管理员"
	}
	return u.Role
}

// DisplayStatus is the localised status label used by /api/account/settings.
func (u *User) DisplayStatus() string {
	switch u.Status {
	case StatusActive:
		return "正常"
	case StatusDisabled:
		return "禁用"
	case StatusLocked:
		return "锁定"
	}
	return u.Status
}

// Principal adapts *User to httpapi.Principal.
type Principal struct{ User *User }

func (p Principal) ID() int64            { return p.User.IDValue }
func (p Principal) Username() string     { return p.User.Username }
func (p Principal) Email() string        { return p.User.Email }
func (p Principal) IsOperator() bool     { return p.User.IsOperator() }
func (p Principal) IsActiveStatus() bool { return p.User.IsActiveStatus() }

// IsActiveFlag reports Django's is_active flag. The middleware checks it on
// every request because ModelBackend.get_user does: an account that may no
// longer authenticate must not keep working through an existing session.
func (p Principal) IsActiveFlag() bool { return p.User.IsActiveFlag }

// SessionAuthHash carries the password-hash fingerprint the middleware compares
// on every request, the role _auth_user_hash plays in Django: changing the
// password invalidates every session minted under the old one.
func (p Principal) SessionAuthHash() string { return SessionAuthHash(p.User.Password) }

// SessionAuthHash is the stored session fingerprint: a salted SHA-256 over the
// password hash column. It is not the password hash itself and leaks nothing on
// its own; equality only proves the session was minted under this hash.
func SessionAuthHash(passwordHash string) string {
	sum := sha256.Sum256([]byte("mirror-studio:session-auth:" + passwordHash))
	return hex.EncodeToString(sum[:])
}

// Profile is the users_userprofile row.
type Profile struct {
	Nickname       string
	Avatar         string
	RegisterIP     string
	RegisterSource string
	Remark         string
}

// Errors the handlers map onto contract messages.
var (
	ErrUserNotFound       = errors.New("用户不存在")
	ErrUsernameTaken      = errors.New("账号已存在")
	ErrEmailTaken         = errors.New("邮箱已存在")
	ErrInvalidCredentials = errors.New("账号或密码错误")
	ErrInactiveAccount    = errors.New("账号状态不可用")
)

// QuotaProvider is the slice of internal/quota this package needs. main.go
// assigns the real implementation to Service.Quota.
type QuotaProvider interface {
	Summary(ctx context.Context, userID int64) (map[string]any, error)
}

// RiskGateway is the slice of internal/risk this package calls. It is declared
// here so auth does not import risk while the two packages are written in
// parallel; *risk.Service satisfies it structurally. EnsureLoginAllowed returns
// an error carrying the user-facing lockout message.
type RiskGateway interface {
	EnsureLoginAllowed(ctx context.Context, username, ip string) error
	RecordLoginFailure(ctx context.Context, username, ip string) int64
	ClearLoginFailures(ctx context.Context, username, ip string)
}

// AuditLogger is the slice of internal/audit this package writes.
type AuditLogger interface {
	LogCallbackEvent(ctx context.Context, eventType, source string, payload map[string]any, success bool, remark string) error
}

// RecentTasksProvider supplies the three most recent generation tasks for
// /api/account/summary, already passed through the generator's runtime-state
// reconciliation. main.go assigns the real implementation to Service.Tasks.
type RecentTasksProvider interface {
	RecentTasks(ctx context.Context, userID int64, limit int) ([]map[string]any, error)
}

// Service is the auth domain entry point.
type Service struct {
	DB     *db.DB
	Cfg    *config.Config
	Site   *siteconfig.Service
	Cipher *crypt.FieldCipher
	Log    *slog.Logger

	// Quota, Risk, Audit and Tasks are injected by main.go after construction;
	// a nil value degrades to a no-op so unit tests stay independent.
	Quota QuotaProvider
	Risk  RiskGateway
	Audit AuditLogger
	Tasks RecentTasksProvider

	emailCodes *emailCodeStore
	location   *time.Location
}

// NewService builds the auth service. The trailing dependencies are the ones
// main.go resolves at boot: a *time.Location (TIME_ZONE) and a *slog.Logger;
// they are accepted in either order so the frozen five-argument call
// (…, log) and the wired call (…, location, log) both compile.
func NewService(handle *db.DB, cfg *config.Config, cipher *crypt.FieldCipher, site *siteconfig.Service, deps ...any) *Service {
	service := &Service{
		DB:         handle,
		Cfg:        cfg,
		Site:       site,
		Cipher:     cipher,
		emailCodes: newEmailCodeStore(),
	}
	for _, dep := range deps {
		switch typed := dep.(type) {
		case *slog.Logger:
			service.Log = typed
		case *time.Location:
			if typed != nil {
				service.location = typed
			}
		}
	}
	if service.location == nil {
		if location, err := time.LoadLocation(cfg.TimeZone); err == nil && location != nil {
			service.location = location
		}
	}
	return service
}

// ── password hashing ──

const (
	argon2TimeCost    = 2
	argon2MemoryCost  = 102400
	argon2Parallelism = 8
	argon2KeyLength   = 32
	argon2SaltLength  = 20
	argon2Prefix      = "argon2$argon2id$v=19$m=102400,t=2,p=8$"
)

// HashPassword produces a Django-compatible argon2 hash:
// argon2$argon2id$v=19$m=102400,t=2,p=8$<b64 salt>$<b64 hash> with an
// unpadded 20-byte random salt.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argon2SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, argon2TimeCost, argon2MemoryCost, argon2Parallelism, argon2KeyLength)
	return argon2Prefix +
		base64.RawStdEncoding.EncodeToString(salt) + "$" +
		base64.RawStdEncoding.EncodeToString(hash), nil
}

// VerifyPassword checks a password against a Django hash. It understands the
// argon2 hashes this service writes and the pbkdf2_sha256 hashes the reference
// implementation may still hold. needsRehash reports that the stored hash is
// not the current default (argon2) and the caller that just verified a login
// must re-encode it, which is what Django's check_password does through
// hasher.must_update on every successful login.
func VerifyPassword(encoded, password string) (ok bool, needsRehash bool) {
	encoded = strings.TrimSpace(encoded)
	switch {
	case strings.HasPrefix(encoded, "argon2$"):
		ok, needsRehash = verifyArgon2(encoded, password)
		return ok, ok && needsRehash
	case strings.HasPrefix(encoded, "pbkdf2_sha256$"), strings.HasPrefix(encoded, "pbkdf2-sha256$"):
		ok, _ = verifyPBKDF2SHA256(encoded, password)
		// Argon2 is the preferred hasher, so a valid PBKDF2 hash should be
		// upgraded the next time a password is written.
		return ok, ok
	}
	return false, false
}

func verifyArgon2(encoded, password string) (ok bool, needsRehash bool) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "argon2" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, false
	}
	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return false, false
	}
	memory, errMemory := parseParam(params[0], "m=")
	timeCost, errTime := parseParam(params[1], "t=")
	parallelism, errThreads := parseParam(params[2], "p=")
	if errMemory != nil || errTime != nil || errThreads != nil {
		return false, false
	}
	if memory <= 0 || timeCost <= 0 || parallelism <= 0 {
		return false, false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return false, false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, false
	}
	got := argon2.IDKey([]byte(password), salt, uint32(timeCost), uint32(memory), uint8(parallelism), uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, false
	}
	needsRehash = memory != argon2MemoryCost || timeCost != argon2TimeCost || parallelism != argon2Parallelism
	return true, needsRehash
}

func parseParam(value, prefix string) (int, error) {
	if !strings.HasPrefix(value, prefix) {
		return 0, fmt.Errorf("argon2 parameter %q is malformed", value)
	}
	return strconv.Atoi(value[len(prefix):])
}

func verifyPBKDF2SHA256(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 {
		return false, errors.New("malformed pbkdf2 hash")
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations <= 0 {
		return false, errors.New("malformed pbkdf2 iteration count")
	}
	salt := parts[2]
	want, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return false, err
	}
	got := pbkdf2.Key([]byte(password), []byte(salt), iterations, len(want), sha256.New)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// ── password validation (AUTH_PASSWORD_VALIDATORS) ──

const (
	msgPasswordTooSimilar = "密码跟 %s 太相似了。"
	msgPasswordTooShort   = "This password is too short. It must contain at least 8 characters."
	msgPasswordTooCommon  = "这个密码太常见了。"
	msgPasswordNumeric    = "密码只包含数字。"
)

// ValidatePassword reproduces django.contrib.auth.password_validation
// .validate_password with the project's validator list and zh-hans messages.
// A nil user skips UserAttributeSimilarityValidator, which is what registration
// and password reset do; the account password change passes the user.
func ValidatePassword(password string, user *User) []httpapi.FieldError {
	errs := []httpapi.FieldError{}
	if user != nil {
		if name, similar := similarToUser(password, user); similar {
			errs = append(errs, httpapi.FieldError{Message: fmt.Sprintf(msgPasswordTooSimilar, name), Code: "password_too_similar"})
		}
	}
	if utf8.RuneCountInString(password) < 8 {
		errs = append(errs, httpapi.FieldError{Message: msgPasswordTooShort, Code: "password_too_short"})
	}
	if _, common := commonPasswordSet[strings.ToLower(strings.TrimSpace(password))]; common {
		errs = append(errs, httpapi.FieldError{Message: msgPasswordTooCommon, Code: "password_too_common"})
	}
	if isAllDigits(password) {
		errs = append(errs, httpapi.FieldError{Message: msgPasswordNumeric, Code: "password_entirely_numeric"})
	}
	return errs
}

// similarToUser ports UserAttributeSimilarityValidator with the default
// attributes and max_similarity=0.7. first_name/last_name are empty for every
// account in this service, so username and email are the only candidates.
func similarToUser(password string, user *User) (string, bool) {
	candidates := []struct {
		value string
		label string
	}{
		{user.Username, "用户名"},
		{user.Email, "邮箱"},
	}
	for _, candidate := range candidates {
		if candidate.value == "" {
			continue
		}
		parts := splitNonWord(candidate.value)
		parts = append(parts, candidate.value)
		for _, part := range parts {
			if quickRatio(password, part) >= 0.7 {
				return candidate.label, true
			}
		}
	}
	return "", false
}

// splitNonWord mirrors python's re.split(r"\W+", value) for str patterns:
// runs of non-word characters (letters, digits, underscore) separate parts.
func splitNonWord(value string) []string {
	parts := []string{}
	current := strings.Builder{}
	for _, char := range value {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || char == '_' {
			current.WriteRune(char)
			continue
		}
		parts = append(parts, current.String())
		current.Reset()
	}
	parts = append(parts, current.String())
	return parts
}

// quickRatio mirrors difflib.SequenceMatcher.quick_ratio: 2*matches/length over
// the character multisets.
func quickRatio(left, right string) float64 {
	if left == "" || right == "" {
		return 0
	}
	leftCounts := map[rune]int{}
	for _, char := range strings.ToLower(left) {
		leftCounts[char]++
	}
	matches := 0
	rightLength := 0
	for _, char := range strings.ToLower(right) {
		rightLength++
		if leftCounts[char] > 0 {
			leftCounts[char]--
			matches++
		}
	}
	return 2 * float64(matches) / float64(utf8.RuneCountInString(left)+rightLength)
}

// isAllDigits mirrors python's str.isdigit() closely enough for ASCII and
// unicode decimal digits; python also counts superscript digits, which cannot
// appear in a password that a user types.
func isAllDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if !unicode.IsDigit(char) {
			return false
		}
	}
	return true
}

// ── email normalisation ──

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// ── account store ──

const userColumns = `id, password, username, email, is_superuser, is_staff, is_active, role, status, date_joined, last_login, last_login_at, last_login_ip`

type rowScanner interface{ Scan(dest ...any) error }

func scanUser(row rowScanner) (*User, error) {
	var (
		user                           User
		email                          sql.NullString
		dateJoined                     sql.NullString
		lastLogin, lastLoginAt, ip     sql.NullString
		isSuperuser, isStaff, isActive int
	)
	if err := row.Scan(
		&user.IDValue, &user.Password, &user.Username, &email,
		&isSuperuser, &isStaff, &isActive, &user.Role, &user.Status,
		&dateJoined, &lastLogin, &lastLoginAt, &ip,
	); err != nil {
		return nil, err
	}
	user.Email = email.String
	user.IsSuperuser = isSuperuser == 1
	user.IsStaff = isStaff == 1
	user.IsActiveFlag = isActive == 1
	if parsed, ok := db.ParseTime(dateJoined.String); ok {
		user.DateJoined = parsed
	}
	if parsed, ok := db.ParseTime(lastLogin.String); ok {
		user.LastLogin = &parsed
	}
	if parsed, ok := db.ParseTime(lastLoginAt.String); ok {
		user.LastLoginAt = &parsed
	}
	user.LastLoginIP = ip.String
	return &user, nil
}

// ByID loads an account by primary key.
func (s *Service) ByID(ctx context.Context, id int64) (*User, error) {
	user, err := scanUser(s.DB.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users_user WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return user, nil
}

// ByUsername loads an account by its exact username.
func (s *Service) ByUsername(ctx context.Context, username string) (*User, error) {
	user, err := scanUser(s.DB.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users_user WHERE username = ?`, username))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return user, nil
}

// UsernameExists reports whether a username is taken.
func (s *Service) UsernameExists(ctx context.Context, username string) (bool, error) {
	return s.exists(ctx, `SELECT COUNT(1) FROM users_user WHERE username = ?`, username)
}

// ResolvePrincipal loads the transport principal for a session's user_id.
func (s *Service) ResolvePrincipal(ctx context.Context, userID int64) (httpapi.Principal, error) {
	user, err := s.ByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return Principal{User: user}, nil
}

// EmailExists reports whether an email address is taken.
func (s *Service) EmailExists(ctx context.Context, email string) (bool, error) {
	return s.exists(ctx, `SELECT COUNT(1) FROM users_user WHERE email = ?`, normalizeEmail(email))
}

func (s *Service) exists(ctx context.Context, query string, args ...any) (bool, error) {
	var count int
	if err := s.DB.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// Authenticate reproduces ModelBackend.authenticate: the username must match
// exactly, the password must verify and is_active must be true. The account
// status check that produces 账号状态不可用 happens one layer up, in the login
// form, exactly like the reference. A legacy hash that still verifies
// (pbkdf2_sha256, or argon2 written under weaker parameters) is re-encoded to
// the current argon2 parameters right here, which is what Django's
// check_password does through hasher.must_update on every successful login.
func (s *Service) Authenticate(ctx context.Context, username, password string) (*User, error) {
	user, err := s.ByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	ok, needsRehash := VerifyPassword(user.Password, password)
	if !ok || !user.IsActiveFlag {
		return nil, ErrInvalidCredentials
	}
	if needsRehash {
		if hashed, hashErr := HashPassword(password); hashErr == nil {
			if _, updateErr := s.DB.ExecContext(ctx,
				`UPDATE users_user SET password = ? WHERE id = ?`, hashed, user.IDValue,
			); updateErr != nil {
				// The login itself stands; the upgrade retries on the next one.
				if s.Log != nil {
					s.Log.Warn("password rehash on login failed", "user_id", user.IDValue, "error", updateErr)
				}
			} else {
				user.Password = hashed
			}
		}
	}
	return user, nil
}

// Register creates an account plus its profile and quota account. It returns
// ErrUsernameTaken/ErrEmailTaken when the unique indexes reject the row.
func (s *Service) Register(ctx context.Context, username, email, password, ip string) (*User, error) {
	email = normalizeEmail(email)
	hashed, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var userID int64
	err = s.DB.WithImmediate(ctx, func(tx *db.Tx) error {
		row := tx.QueryRow(
			`INSERT INTO users_user
			   (password, last_login, is_superuser, username, first_name, last_name, is_staff, is_active,
			    date_joined, email, mobile, role, status, last_login_ip, last_login_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 RETURNING id`,
			hashed, nil, db.Bool(false), username, "", "", db.Bool(false), db.Bool(true),
			db.FormatTime(now), db.NullableString(email), nil, RoleUser, StatusActive, nil, nil,
		)
		if err := row.Scan(&userID); err != nil {
			return registerError(err)
		}
		if _, err := tx.Exec(
			`INSERT INTO users_userprofile
			   (created_at, updated_at, nickname, avatar, register_ip, register_source, remark, user_id)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			db.FormatTime(now), db.FormatTime(now), username, "", db.NullableString(ip), "website", "", userID,
		); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	user, err := s.ByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := s.EnsureQuotaAccount(ctx, userID); err != nil {
		return nil, err
	}
	return user, nil
}

func registerError(err error) error {
	message := err.Error()
	switch {
	case strings.Contains(message, "users_user.username"):
		return ErrUsernameTaken
	case strings.Contains(message, "users_user.email"):
		return ErrEmailTaken
	}
	return err
}

// SetPassword re-hashes and stores a new password.
func (s *Service) SetPassword(ctx context.Context, userID int64, password string) error {
	hashed, err := HashPassword(password)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE users_user SET password = ? WHERE id = ?`, hashed, userID)
	return err
}

// UpdateLogin writes the two fields the 1.x views maintain next to Django's
// user_logged_in signal: last_login_at and last_login_ip.
func (s *Service) UpdateLogin(ctx context.Context, userID int64, ip string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE users_user SET last_login_at = ?, last_login_ip = ? WHERE id = ?`,
		db.FormatTime(time.Now()), db.NullableString(ip), userID,
	)
	return err
}

// Login performs the session side of django.contrib.auth.login: the key is
// cycled for an anonymous session (a session already bound to a different user
// is re-keyed here too, the closest this session store gets to a flush) and
// user_id is stored. It also writes users_user.last_login, which the reference
// does through the user_logged_in signal; last_login_at/last_login_ip are
// written separately by UpdateLogin. The CSRF token rotation Django performs
// inside login() happens at the call sites, which own the response writer the
// new cookie needs.
func (s *Service) Login(ctx context.Context, session *httpapi.Session, user *User) error {
	if session == nil || user == nil {
		return errors.New("login requires a session and a user")
	}
	if current := session.GetInt(SessionUserIDKey); current != user.IDValue {
		session.CycleKey()
	}
	session.SetInt(SessionUserIDKey, user.IDValue)
	session.Set("session_auth_hash", SessionAuthHash(user.Password))
	_, err := s.DB.ExecContext(ctx,
		`UPDATE users_user SET last_login = ? WHERE id = ?`,
		db.FormatTime(time.Now()), user.IDValue,
	)
	return err
}

// ── profile store ──

// ProfileOf returns the profile, creating the row the way
// UserProfile.objects.get_or_create does when it is missing.
func (s *Service) ProfileOf(ctx context.Context, userID int64) (Profile, error) {
	profile, found, err := s.readProfile(ctx, userID)
	if err != nil {
		return Profile{}, err
	}
	if found {
		return profile, nil
	}
	user, err := s.ByID(ctx, userID)
	if err != nil {
		return Profile{}, err
	}
	return s.createProfile(ctx, userID, user.Username, "")
}

func (s *Service) readProfile(ctx context.Context, userID int64) (Profile, bool, error) {
	var (
		profile      Profile
		registerIP   sql.NullString
		registerFrom sql.NullString
	)
	err := s.DB.QueryRowContext(ctx,
		`SELECT nickname, avatar, register_ip, register_source, remark FROM users_userprofile WHERE user_id = ?`,
		userID,
	).Scan(&profile.Nickname, &profile.Avatar, &registerIP, &registerFrom, &profile.Remark)
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, false, nil
	}
	if err != nil {
		return Profile{}, false, err
	}
	profile.RegisterIP = registerIP.String
	profile.RegisterSource = registerFrom.String
	return profile, true, nil
}

func (s *Service) createProfile(ctx context.Context, userID int64, nickname, ip string) (Profile, error) {
	now := db.FormatTime(time.Now())
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO users_userprofile
		   (created_at, updated_at, nickname, avatar, register_ip, register_source, remark, user_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, nickname, "", db.NullableString(ip), "website", "", userID,
	)
	if err != nil {
		return Profile{}, err
	}
	return Profile{Nickname: nickname, RegisterSource: "website", RegisterIP: ip}, nil
}

// Summary is the account payload of /api/account/summary, which carries no
// quota keys.
func (s *Service) Summary(ctx context.Context, user *User) (map[string]any, error) {
	profile, err := s.ProfileOf(ctx, user.IDValue)
	if err != nil {
		return nil, err
	}
	return profileSummary(user, profile), nil
}

func profileSummary(user *User, profile Profile) map[string]any {
	return map[string]any{
		"username": user.Username,
		"nickname": firstNonEmpty(profile.Nickname, user.Username),
		"email":    user.Email,
		"avatar":   strings.TrimSpace(profile.Avatar),
		"remark":   profile.Remark,
	}
}

// ── quota bridge ──

// EnsureQuotaAccount makes sure the account's quota row exists. The quota
// service ensures (and refreshes) the account as part of every Summary read,
// which is why no separate call is needed.
func (s *Service) EnsureQuotaAccount(ctx context.Context, userID int64) error {
	if s.Quota == nil {
		return nil
	}
	_, err := s.Quota.Summary(ctx, userID)
	return err
}

// QuotaSummary returns the quota object for the account endpoints. A missing
// provider (unit tests, partially wired boot) yields zeroed keys, not an error.
func (s *Service) QuotaSummary(ctx context.Context, userID int64) (map[string]any, error) {
	if s.Quota == nil {
		return zeroQuotaSummary(), nil
	}
	summary, err := s.Quota.Summary(ctx, userID)
	if err != nil {
		return nil, err
	}
	return summary, nil
}

func zeroQuotaSummary() map[string]any {
	return map[string]any{
		"daily_quota":           0,
		"available_quota":       0,
		"frozen_quota":          0,
		"total_generated_count": 0,
		"last_reset_date":       "",
	}
}

// ── bootstrap administrator ──

// EnsureAdminUser creates or updates the bootstrap administrator. It is
// idempotent and runs on every boot. An empty username or password is a no-op,
// which is how deployments without a seeded admin behave.
func (s *Service) EnsureAdminUser(ctx context.Context, username, password, email string) (created bool, err error) {
	if strings.TrimSpace(username) == "" || password == "" {
		return false, nil
	}
	if strings.TrimSpace(email) == "" {
		email = "admin@example.com"
	}
	email = normalizeEmail(email)
	hashed, err := HashPassword(password)
	if err != nil {
		return false, err
	}
	now := time.Now()

	existing, lookupErr := s.ByUsername(ctx, username)
	switch {
	case errors.Is(lookupErr, ErrUserNotFound):
		var userID int64
		err = s.DB.WithImmediate(ctx, func(tx *db.Tx) error {
			row := tx.QueryRow(
				`INSERT INTO users_user
				   (password, last_login, is_superuser, username, first_name, last_name, is_staff, is_active,
				    date_joined, email, mobile, role, status, last_login_ip, last_login_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				 RETURNING id`,
				hashed, nil, db.Bool(true), username, "", "", db.Bool(true), db.Bool(true),
				db.FormatTime(now), db.NullableString(email), nil, RoleAdmin, StatusActive, nil, nil,
			)
			if err := row.Scan(&userID); err != nil {
				return registerError(err)
			}
			if _, err := tx.Exec(
				`INSERT INTO users_userprofile
				   (created_at, updated_at, nickname, avatar, register_ip, register_source, remark, user_id)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				db.FormatTime(now), db.FormatTime(now), username, "", nil, "website", "", userID,
			); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return false, err
		}
		// The reference's post_save signal also creates the quota account for a
		// freshly created user.
		if err := s.EnsureQuotaAccount(ctx, userID); err != nil {
			return true, err
		}
		return true, nil
	case lookupErr != nil:
		return false, lookupErr
	}

	if _, err := s.DB.ExecContext(ctx,
		`UPDATE users_user SET password = ?, email = ?, is_staff = ?, is_superuser = ?, role = ? WHERE id = ?`,
		hashed, db.NullableString(email), db.Bool(true), db.Bool(true), RoleAdmin, existing.IDValue,
	); err != nil {
		return false, err
	}
	if _, err := s.ProfileOf(ctx, existing.IDValue); err != nil {
		return false, err
	}
	return false, nil
}

// ── small helpers ──

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// pythonISO renders a timestamp like python's datetime.isoformat(timespec=
// "auto"): microseconds included only when non-zero, never a timezone suffix.
func pythonISO(value time.Time) string {
	microseconds := value.Nanosecond() / 1000
	if microseconds == 0 {
		return value.Format("2006-01-02T15:04:05")
	}
	return fmt.Sprintf("%s.%06d", value.Format("2006-01-02T15:04:05"), microseconds)
}

// truncateRunes mirrors python slicing text[:n] by code point.
func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
