package console

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"mirror-studio/internal/db"
	"mirror-studio/internal/quota"
)

type profileRow struct {
	Nickname       string
	Avatar         string
	RegisterIP     sql.NullString
	RegisterSource string
	Remark         string
}

type userRow struct {
	ID          int64
	Username    string
	Email       sql.NullString
	Mobile      sql.NullString
	Role        string
	Status      string
	IsStaff     bool
	DateJoined  string
	LastLogin   sql.NullString
	LastLoginIP sql.NullString
	Quota       *quota.Account
	Profile     *profileRow
}

type userFilter struct {
	Keyword string
	Status  string
	Role    string
}

type taskRow struct {
	ID                int64
	TaskNo            string
	UserID            int64
	Username          sql.NullString
	Status            string
	Prompt            string
	StyleCode         string
	SizeCode          string
	QualityCode       string
	ChannelName       sql.NullString
	ChannelCode       sql.NullString
	QuotaCost         int
	ErrorCode         string
	ErrorMessage      string
	ProviderRequestID string
	ProviderResponse  sql.NullString
	CreatedAt         string
	FinishedAt        sql.NullString
}

type taskFilter struct {
	Keyword string
	Status  string
}

type channelRow struct {
	ID             int64
	CreatedAt      string
	UpdatedAt      string
	Name           string
	ChannelCode    string
	BaseURL        string
	APIPath        string
	APIKey         string
	ModelName      string
	TimeoutSeconds int
	MaxRetries     int
	IsActive       bool
	Priority       int
	ChannelType    string
	ProviderName   string
	EditAPIPath    string
	ProviderVendor string
}

const userColumns = `u.id, u.username, u.email, u.mobile, u.role, u.status, u.is_staff,
	u.date_joined, u.last_login, u.last_login_ip,
	q.id, q.daily_quota, q.available_quota, q.frozen_quota, q.total_generated_count, q.last_reset_date`

const userFrom = ` FROM users_user u LEFT JOIN billing_quotaaccount q ON q.user_id = u.id`

const taskColumns = `t.id, t.task_no, t.user_id, u.username, t.status, t.prompt, t.style_code, t.size_code,
	t.quality_code, c.name, c.channel_code, t.quota_cost, t.error_code, t.error_message, t.provider_request_id,
	t.provider_response_json, t.created_at, t.finished_at`

const taskFrom = ` FROM generator_generatetask t
	LEFT JOIN users_user u ON u.id = t.user_id
	LEFT JOIN generator_imagechannel c ON c.id = t.channel_id`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(scanner rowScanner) (userRow, error) {
	var (
		row         userRow
		email       sql.NullString
		mobile      sql.NullString
		lastLogin   sql.NullString
		lastLoginIP sql.NullString
		quotaID     sql.NullInt64
		daily       sql.NullInt64
		available   sql.NullInt64
		frozen      sql.NullInt64
		total       sql.NullInt64
		lastReset   sql.NullString
		isStaff     int
	)
	if err := scanner.Scan(
		&row.ID, &row.Username, &email, &mobile, &row.Role, &row.Status, &isStaff,
		&row.DateJoined, &lastLogin, &lastLoginIP,
		&quotaID, &daily, &available, &frozen, &total, &lastReset,
	); err != nil {
		return userRow{}, err
	}
	row.Email = email
	row.Mobile = mobile
	row.LastLogin = lastLogin
	row.LastLoginIP = lastLoginIP
	row.IsStaff = isStaff == 1
	if quotaID.Valid {
		row.Quota = &quota.Account{
			ID:                  quotaID.Int64,
			UserID:              row.ID,
			DailyQuota:          int(daily.Int64),
			AvailableQuota:      int(available.Int64),
			FrozenQuota:         int(frozen.Int64),
			TotalGeneratedCount: int(total.Int64),
			LastResetDate:       dateOnly(lastReset.String),
		}
	}
	return row, nil
}

func scanTask(scanner rowScanner) (taskRow, error) {
	var (
		row         taskRow
		username    sql.NullString
		channelName sql.NullString
		channelCode sql.NullString
		response    sql.NullString
		finished    sql.NullString
	)
	if err := scanner.Scan(
		&row.ID, &row.TaskNo, &row.UserID, &username, &row.Status, &row.Prompt, &row.StyleCode,
		&row.SizeCode, &row.QualityCode, &channelName, &channelCode, &row.QuotaCost, &row.ErrorCode,
		&row.ErrorMessage, &row.ProviderRequestID, &response, &row.CreatedAt, &finished,
	); err != nil {
		return taskRow{}, err
	}
	row.Username = username
	row.ChannelName = channelName
	row.ChannelCode = channelCode
	row.ProviderResponse = response
	row.FinishedAt = finished
	return row, nil
}

func scanChannel(scanner rowScanner) (channelRow, error) {
	var row channelRow
	var isActive int
	if err := scanner.Scan(
		&row.ID, &row.CreatedAt, &row.UpdatedAt, &row.Name, &row.ChannelCode, &row.BaseURL,
		&row.APIPath, &row.APIKey, &row.ModelName, &row.TimeoutSeconds, &row.MaxRetries,
		&isActive, &row.Priority, &row.ChannelType, &row.ProviderName, &row.EditAPIPath,
		&row.ProviderVendor,
	); err != nil {
		return channelRow{}, err
	}
	row.IsActive = isActive == 1
	return row, nil
}

func userConditions(filter userFilter, args *[]any) string {
	conditions := []string{}
	if filter.Keyword != "" {
		pattern := likePattern(filter.Keyword)
		conditions = append(conditions,
			`(u.username LIKE ? ESCAPE '\' OR u.email LIKE ? ESCAPE '\' OR u.mobile LIKE ? ESCAPE '\')`)
		*args = append(*args, pattern, pattern, pattern)
	}
	if filter.Status != "" {
		conditions = append(conditions, `u.status = ?`)
		*args = append(*args, filter.Status)
	}
	if filter.Role != "" {
		conditions = append(conditions, `u.role = ?`)
		*args = append(*args, filter.Role)
	}
	return whereClause(conditions)
}

func taskConditions(filter taskFilter, args *[]any) string {
	conditions := []string{}
	if filter.Keyword != "" {
		pattern := likePattern(filter.Keyword)
		conditions = append(conditions,
			`(t.task_no LIKE ? ESCAPE '\' OR u.username LIKE ? ESCAPE '\' OR t.prompt LIKE ? ESCAPE '\')`)
		*args = append(*args, pattern, pattern, pattern)
	}
	if filter.Status != "" {
		conditions = append(conditions, `t.status = ?`)
		*args = append(*args, filter.Status)
	}
	return whereClause(conditions)
}

func whereClause(conditions []string) string {
	if len(conditions) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(conditions, " AND ")
}

// likePattern escapes the LIKE wildcards the way Django's icontains does.
func likePattern(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + replacer.Replace(value) + "%"
}

func (s *Service) countUsers(ctx context.Context, filter userFilter) (int, error) {
	args := []any{}
	query := `SELECT COUNT(1)` + userFrom + userConditions(filter, &args)
	var total int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *Service) listUsers(ctx context.Context, filter userFilter, limit, offset int) ([]userRow, error) {
	args := []any{}
	query := `SELECT ` + userColumns + userFrom + userConditions(filter, &args) +
		` ORDER BY u.date_joined DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []userRow{}
	for rows.Next() {
		item, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Service) userByID(ctx context.Context, id int64) (userRow, bool, error) {
	query := `SELECT ` + userColumns + userFrom + ` WHERE u.id = ?`
	row, err := scanUser(s.db.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return userRow{}, false, nil
	}
	if err != nil {
		return userRow{}, false, err
	}
	return row, true, nil
}

func (s *Service) loadProfile(ctx context.Context, userID int64) (*profileRow, error) {
	var (
		profile  profileRow
		register sql.NullString
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT nickname, avatar, register_ip, register_source, remark FROM users_userprofile WHERE user_id = ?`,
		userID,
	).Scan(&profile.Nickname, &profile.Avatar, &register, &profile.RegisterSource, &profile.Remark)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	profile.RegisterIP = register
	return &profile, nil
}

func (s *Service) countTasks(ctx context.Context, filter taskFilter) (int, error) {
	args := []any{}
	query := `SELECT COUNT(1)` + taskFrom + taskConditions(filter, &args)
	var total int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *Service) listTasks(ctx context.Context, filter taskFilter, limit, offset int, order string) ([]taskRow, error) {
	args := []any{}
	query := `SELECT ` + taskColumns + taskFrom + taskConditions(filter, &args) +
		` ORDER BY ` + order + ` LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []taskRow{}
	for rows.Next() {
		item, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Service) listChannels(ctx context.Context) ([]channelRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, created_at, updated_at, name, channel_code, base_url, api_path, api_key_encrypted,
			model_name, timeout_seconds, max_retries, is_active, priority, channel_type, provider_name,
			edit_api_path, provider_vendor
		 FROM generator_imagechannel ORDER BY priority, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []channelRow{}
	for rows.Next() {
		item, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Service) channelByID(ctx context.Context, id int64) (channelRow, bool, error) {
	row, err := scanChannel(s.db.QueryRowContext(ctx,
		`SELECT id, created_at, updated_at, name, channel_code, base_url, api_path, api_key_encrypted,
			model_name, timeout_seconds, max_retries, is_active, priority, channel_type, provider_name,
			edit_api_path, provider_vendor
		 FROM generator_imagechannel WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return channelRow{}, false, nil
	}
	if err != nil {
		return channelRow{}, false, err
	}
	return row, true, nil
}

func (s *Service) channelCodeExists(ctx context.Context, code string) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM generator_imagechannel WHERE channel_code = ?`, code).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Service) insertChannel(ctx context.Context, channel channelRow) (channelRow, error) {
	now := s.now()
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO generator_imagechannel
			(created_at, updated_at, name, channel_code, base_url, api_path, api_key_encrypted, model_name,
			 timeout_seconds, max_retries, is_active, priority, channel_type, provider_name, edit_api_path, provider_vendor)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		db.FormatTime(now), db.FormatTime(now), channel.Name, channel.ChannelCode, channel.BaseURL,
		channel.APIPath, channel.APIKey, channel.ModelName, channel.TimeoutSeconds, channel.MaxRetries,
		db.Bool(channel.IsActive), channel.Priority, channel.ChannelType, channel.ProviderName,
		channel.EditAPIPath, channel.ProviderVendor,
	)
	if err != nil {
		return channelRow{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return channelRow{}, err
	}
	created, found, err := s.channelByID(ctx, id)
	if err != nil {
		return channelRow{}, err
	}
	if !found {
		return channelRow{}, sql.ErrNoRows
	}
	return created, nil
}

func (s *Service) updateChannel(ctx context.Context, channel channelRow) (channelRow, error) {
	now := s.now()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE generator_imagechannel SET
			updated_at = ?, name = ?, channel_type = ?, provider_name = ?, base_url = ?, api_path = ?,
			edit_api_path = ?, model_name = ?, timeout_seconds = ?, max_retries = ?, is_active = ?,
			priority = ?, api_key_encrypted = ?
		 WHERE id = ?`,
		db.FormatTime(now), channel.Name, channel.ChannelType, channel.ProviderName, channel.BaseURL,
		channel.APIPath, channel.EditAPIPath, channel.ModelName, channel.TimeoutSeconds,
		channel.MaxRetries, db.Bool(channel.IsActive), channel.Priority, channel.APIKey, channel.ID,
	); err != nil {
		return channelRow{}, err
	}
	updated, found, err := s.channelByID(ctx, channel.ID)
	if err != nil {
		return channelRow{}, err
	}
	if !found {
		return channelRow{}, sql.ErrNoRows
	}
	return updated, nil
}

func (s *Service) deleteChannel(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM generator_imagechannel WHERE id = ?`, id)
	return err
}

func (s *Service) channelHasTasks(ctx context.Context, id int64) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM generator_generatetask WHERE channel_id = ?`, id).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Service) counts(ctx context.Context) (map[string]any, map[string]any, error) {
	start, end := s.todayRange()
	today := map[string]any{}
	totals := map[string]any{}

	counts := []struct {
		query string
		args  []any
		into  map[string]any
		key   string
	}{
		{`SELECT COUNT(1) FROM users_user WHERE date_joined >= ? AND date_joined < ?`, []any{start, end}, today, "new_users"},
		{`SELECT COUNT(1) FROM generator_generatetask WHERE created_at >= ? AND created_at < ?`, []any{start, end}, today, "tasks"},
		{`SELECT COUNT(1) FROM generator_generatetask WHERE created_at >= ? AND created_at < ? AND status = 'failed'`, []any{start, end}, today, "failed_tasks"},
		{`SELECT COUNT(1) FROM generator_generatetask WHERE created_at >= ? AND created_at < ? AND status = 'succeeded'`, []any{start, end}, today, "succeeded_tasks"},
		{`SELECT COUNT(1) FROM users_user`, nil, totals, "users"},
		{`SELECT COUNT(1) FROM mediahub_generatedimage`, nil, totals, "images"},
		{`SELECT COUNT(1) FROM generator_generatetask`, nil, totals, "tasks"},
		{`SELECT COUNT(1) FROM generator_imagechannel WHERE is_active = ?`, []any{db.Bool(true)}, totals, "active_channels"},
	}
	for _, item := range counts {
		var value int
		if err := s.db.QueryRowContext(ctx, item.query, item.args...).Scan(&value); err != nil {
			return nil, nil, err
		}
		item.into[item.key] = value
	}
	return today, totals, nil
}
