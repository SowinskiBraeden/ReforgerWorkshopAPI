package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Account lifecycle statuses.
const (
	AccountStatusActive    = "active"
	AccountStatusSuspended = "suspended"
	AccountStatusDeleted   = "deleted"
)

// API client / key environments.
const (
	EnvProduction  = "production"
	EnvStaging     = "staging"
	EnvDevelopment = "development"
	EnvTest        = "test"
)

// APIClient is a registered application or integration owned by an account.
// Keys attach to a client; verified client identification in telemetry
// requires the key's client to match the self-reported client name.
type APIClient struct {
	ID              string     `json:"id"`
	AccountID       string     `json:"accountId"`
	Name            string     `json:"name"`
	Slug            string     `json:"slug"`
	Description     string     `json:"description,omitempty"`
	Environment     string     `json:"environment"`
	ClientType      string     `json:"clientType,omitempty"`
	Status          string     `json:"status"`
	WebsiteURL      string     `json:"websiteUrl,omitempty"`
	MonthlyQuota    int64      `json:"monthlyQuota,omitempty"`
	RateLimitPerMin int        `json:"rateLimitPerMinute,omitempty"`
	IsInternal      bool       `json:"isInternal"`
	PubliclyNamable bool       `json:"publiclyNameable"`
	Notes           string     `json:"notes,omitempty"`
	Tags            string     `json:"tags,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	FirstRequestAt  *time.Time `json:"firstRequestAt,omitempty"`
	LastRequestAt   *time.Time `json:"lastRequestAt,omitempty"`
}

// AdminAccountUpdate carries the editable admin fields for an account.
// Nil pointers leave the current value untouched.
type AdminAccountUpdate struct {
	Status     *string
	Notes      *string
	Tags       *string
	IsInternal *bool
	IsTest     *bool
	Plan       *string
}

// migrateAdmin applies the additive schema used by the admin platform. All
// statements are idempotent (duplicate-column errors are ignored).
func (s *BillingStore) migrateAdmin(ctx context.Context) error {
	alter := []string{
		`ALTER TABLE accounts ADD COLUMN status TEXT NOT NULL DEFAULT 'active';`,
		`ALTER TABLE accounts ADD COLUMN notes TEXT NOT NULL DEFAULT '';`,
		`ALTER TABLE accounts ADD COLUMN tags TEXT NOT NULL DEFAULT '';`,
		`ALTER TABLE accounts ADD COLUMN is_internal BOOLEAN NOT NULL DEFAULT false;`,
		`ALTER TABLE accounts ADD COLUMN is_test BOOLEAN NOT NULL DEFAULT false;`,
		`ALTER TABLE accounts ADD COLUMN suspended_at TIMESTAMP;`,
		`ALTER TABLE api_keys ADD COLUMN client_id TEXT NOT NULL DEFAULT '';`,
		`ALTER TABLE api_keys ADD COLUMN environment TEXT NOT NULL DEFAULT 'production';`,
		`ALTER TABLE api_keys ADD COLUMN expires_at TIMESTAMP;`,
		`ALTER TABLE api_keys ADD COLUMN disabled_at TIMESTAMP;`,
		`ALTER TABLE api_keys ADD COLUMN scopes TEXT NOT NULL DEFAULT '';`,
		`ALTER TABLE api_keys ADD COLUMN monthly_quota INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE api_keys ADD COLUMN first_used_at TIMESTAMP;`,
		`ALTER TABLE api_keys ADD COLUMN admin_notes TEXT NOT NULL DEFAULT '';`,
	}
	for _, stmt := range alter {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	create := []string{
		`CREATE TABLE IF NOT EXISTS api_clients (
			id TEXT PRIMARY KEY,
			account_id TEXT NOT NULL,
			name TEXT NOT NULL,
			slug TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			environment TEXT NOT NULL DEFAULT 'production',
			client_type TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			website_url TEXT NOT NULL DEFAULT '',
			monthly_quota INTEGER NOT NULL DEFAULT 0,
			rate_limit_per_minute INTEGER NOT NULL DEFAULT 0,
			is_internal BOOLEAN NOT NULL DEFAULT false,
			publicly_nameable BOOLEAN NOT NULL DEFAULT false,
			notes TEXT NOT NULL DEFAULT '',
			tags TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			FOREIGN KEY(account_id) REFERENCES accounts(id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_api_clients_account ON api_clients(account_id);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_api_clients_slug ON api_clients(slug);`,
	}
	for _, stmt := range create {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// AccountAdminView extends Account with the admin-managed fields.
type AccountAdminView struct {
	Account
	Status      string     `json:"status"`
	Notes       string     `json:"notes,omitempty"`
	Tags        string     `json:"tags,omitempty"`
	IsInternal  bool       `json:"isInternal"`
	IsTest      bool       `json:"isTest"`
	SuspendedAt *time.Time `json:"suspendedAt,omitempty"`
}

// SearchAccounts filters accounts by email/ID substring and status.
func (s *BillingStore) SearchAccounts(ctx context.Context, query string, status string, limit int, offset int) ([]AccountAdminView, int, error) {
	if s == nil || s.db == nil {
		return nil, 0, nil
	}
	where := `WHERE 1=1`
	args := []any{}
	if q := strings.TrimSpace(query); q != "" {
		where += ` AND (email LIKE ? OR id LIKE ? OR stripe_customer_id LIKE ?)`
		like := "%" + q + "%"
		args = append(args, like, like, like)
	}
	if status != "" {
		where += ` AND status = ?`
		args = append(args, status)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM accounts `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, COALESCE(email, ''), COALESCE(stripe_customer_id, ''),
		COALESCE(stripe_subscription_id, ''), plan, subscription_status, created_at, updated_at,
		status, notes, tags, is_internal, is_test, COALESCE(suspended_at, '')
		FROM accounts `+where+` ORDER BY updated_at DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []AccountAdminView
	for rows.Next() {
		view, err := scanAccountAdmin(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, view)
	}
	return out, total, rows.Err()
}

func (s *BillingStore) GetAccountAdmin(ctx context.Context, id string) (AccountAdminView, bool, error) {
	var out AccountAdminView
	if s == nil || s.db == nil {
		return out, false, nil
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, COALESCE(email, ''), COALESCE(stripe_customer_id, ''),
		COALESCE(stripe_subscription_id, ''), plan, subscription_status, created_at, updated_at,
		status, notes, tags, is_internal, is_test, COALESCE(suspended_at, '')
		FROM accounts WHERE id = ?`, id)
	out, err := scanAccountAdmin(row)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	return out, true, nil
}

func scanAccountAdmin(row apiKeyScanner) (AccountAdminView, error) {
	var out AccountAdminView
	var createdAt, updatedAt, suspendedAt sqliteTime
	err := row.Scan(&out.ID, &out.Email, &out.StripeCustomerID, &out.StripeSubscriptionID,
		&out.Plan, &out.SubscriptionStatus, &createdAt, &updatedAt,
		&out.Status, &out.Notes, &out.Tags, &out.IsInternal, &out.IsTest, &suspendedAt)
	if err != nil {
		return out, err
	}
	out.CreatedAt, out.UpdatedAt = createdAt.Time, updatedAt.Time
	if !suspendedAt.Time.IsZero() {
		t := suspendedAt.Time
		out.SuspendedAt = &t
	}
	return out, nil
}

// UpdateAccountAdmin applies admin edits to an account.
func (s *BillingStore) UpdateAccountAdmin(ctx context.Context, id string, update AdminAccountUpdate) error {
	if s == nil || s.db == nil {
		return nil
	}
	now := s.now().UTC()
	if update.Status != nil {
		status := strings.ToLower(strings.TrimSpace(*update.Status))
		switch status {
		case AccountStatusActive, AccountStatusSuspended, AccountStatusDeleted:
		default:
			return fmt.Errorf("invalid account status %q", status)
		}
		var suspendedAt any
		if status == AccountStatusSuspended {
			suspendedAt = now
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE accounts SET status = ?, suspended_at = ?, updated_at = ? WHERE id = ?`,
			status, suspendedAt, now, id); err != nil {
			return err
		}
	}
	set := func(column string, value any) error {
		_, err := s.db.ExecContext(ctx, `UPDATE accounts SET `+column+` = ?, updated_at = ? WHERE id = ?`, value, now, id)
		return err
	}
	if update.Notes != nil {
		if err := set("notes", strings.TrimSpace(*update.Notes)); err != nil {
			return err
		}
	}
	if update.Tags != nil {
		if err := set("tags", normalizeTags(*update.Tags)); err != nil {
			return err
		}
	}
	if update.IsInternal != nil {
		if err := set("is_internal", *update.IsInternal); err != nil {
			return err
		}
	}
	if update.IsTest != nil {
		if err := set("is_test", *update.IsTest); err != nil {
			return err
		}
	}
	if update.Plan != nil {
		plan := strings.ToLower(strings.TrimSpace(*update.Plan))
		switch plan {
		case PlanFree, PlanDeveloper, PlanPro, PlanInternal:
		default:
			return fmt.Errorf("invalid plan %q", plan)
		}
		if err := set("plan", plan); err != nil {
			return err
		}
	}
	return nil
}

func normalizeTags(raw string) string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		tag := strings.ToLower(strings.TrimSpace(part))
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return strings.Join(out, ",")
}

// AccountStatusFor reports the lifecycle status used during authentication.
func (s *BillingStore) AccountStatusFor(ctx context.Context, accountID string) string {
	if s == nil || s.db == nil {
		return AccountStatusActive
	}
	var status string
	if err := s.db.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id = ?`, accountID).Scan(&status); err != nil {
		return AccountStatusActive
	}
	if status == "" {
		return AccountStatusActive
	}
	return status
}

// --- API clients ---

func (s *BillingStore) CreateAPIClient(ctx context.Context, client APIClient) (APIClient, error) {
	if s == nil || s.db == nil {
		return client, errors.New("billing store unavailable")
	}
	client.Name = strings.TrimSpace(client.Name)
	if client.Name == "" {
		return client, errors.New("client name is required")
	}
	if client.AccountID == "" {
		return client, errors.New("client owner account is required")
	}
	if client.ID == "" {
		client.ID = newID("cli")
	}
	if client.Slug == "" {
		client.Slug = slugify(client.Name)
	}
	if client.Environment == "" {
		client.Environment = EnvProduction
	}
	if client.Status == "" {
		client.Status = "active"
	}
	now := s.now().UTC()
	client.CreatedAt, client.UpdatedAt = now, now
	_, err := s.db.ExecContext(ctx, `INSERT INTO api_clients (
		id, account_id, name, slug, description, environment, client_type,
		status, website_url, monthly_quota, rate_limit_per_minute, is_internal,
		publicly_nameable, notes, tags, created_at, updated_at
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		client.ID, client.AccountID, client.Name, client.Slug, client.Description,
		client.Environment, client.ClientType, client.Status, client.WebsiteURL,
		client.MonthlyQuota, client.RateLimitPerMin, client.IsInternal,
		client.PubliclyNamable, client.Notes, normalizeTags(client.Tags), now, now)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return client, errors.New("client slug already exists")
	}
	return client, err
}

var apiClientColumns = `id, account_id, name, slug, description, environment,
	client_type, status, website_url, monthly_quota, rate_limit_per_minute,
	is_internal, publicly_nameable, notes, tags, created_at, updated_at`

func scanAPIClient(row apiKeyScanner) (APIClient, error) {
	var c APIClient
	var createdAt, updatedAt sqliteTime
	err := row.Scan(&c.ID, &c.AccountID, &c.Name, &c.Slug, &c.Description, &c.Environment,
		&c.ClientType, &c.Status, &c.WebsiteURL, &c.MonthlyQuota, &c.RateLimitPerMin,
		&c.IsInternal, &c.PubliclyNamable, &c.Notes, &c.Tags, &createdAt, &updatedAt)
	if err != nil {
		return c, err
	}
	c.CreatedAt, c.UpdatedAt = createdAt.Time, updatedAt.Time
	return c, nil
}

func (s *BillingStore) ListAPIClients(ctx context.Context, accountID string, limit int) ([]APIClient, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	where, args := "", []any{}
	if accountID != "" {
		where = `WHERE account_id = ?`
		args = append(args, accountID)
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+apiClientColumns+` FROM api_clients `+where+
		` ORDER BY created_at DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIClient
	for rows.Next() {
		client, err := scanAPIClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, client)
	}
	return out, rows.Err()
}

func (s *BillingStore) GetAPIClient(ctx context.Context, id string) (APIClient, bool, error) {
	var out APIClient
	if s == nil || s.db == nil {
		return out, false, nil
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+apiClientColumns+` FROM api_clients WHERE id = ? OR slug = ?`, id, id)
	out, err := scanAPIClient(row)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	return out, true, nil
}

// UpdateAPIClient applies non-empty fields; booleans use pointers upstream and
// are encoded here as tri-state strings by the handler.
func (s *BillingStore) UpdateAPIClient(ctx context.Context, id string, fields map[string]any) error {
	if s == nil || s.db == nil || len(fields) == 0 {
		return nil
	}
	allowed := map[string]string{
		"name": "name", "description": "description", "environment": "environment",
		"clientType": "client_type", "status": "status", "websiteUrl": "website_url",
		"monthlyQuota": "monthly_quota", "rateLimitPerMinute": "rate_limit_per_minute",
		"isInternal": "is_internal", "publiclyNameable": "publicly_nameable",
		"notes": "notes", "tags": "tags",
	}
	sets := []string{"updated_at = ?"}
	args := []any{s.now().UTC()}
	for field, value := range fields {
		column, ok := allowed[field]
		if !ok {
			return fmt.Errorf("unknown client field %q", field)
		}
		if field == "tags" {
			if text, ok := value.(string); ok {
				value = normalizeTags(text)
			}
		}
		sets = append(sets, column+" = ?")
		args = append(args, value)
	}
	args = append(args, id)
	_, err := s.db.ExecContext(ctx, `UPDATE api_clients SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	return err
}

func (s *BillingStore) DeleteAPIClient(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE api_keys SET client_id = '' WHERE client_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM api_clients WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func slugify(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == '.':
			b.WriteByte('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = newID("cli")[:12]
	}
	if len(slug) > 60 {
		slug = slug[:60]
	}
	return slug
}

// --- API key admin lifecycle ---

// AdminKeyDetail extends AdminKeySummary with lifecycle fields.
type AdminKeyDetail struct {
	AdminKeySummary
	AccountID    string     `json:"accountId"`
	AccountEmail string     `json:"accountEmail,omitempty"`
	ClientID     string     `json:"clientId,omitempty"`
	Environment  string     `json:"environment"`
	Scopes       string     `json:"scopes,omitempty"`
	MonthlyQuota int64      `json:"monthlyQuota,omitempty"`
	ExpiresAt    *time.Time `json:"expiresAt,omitempty"`
	DisabledAt   *time.Time `json:"disabledAt,omitempty"`
	FirstUsedAt  *time.Time `json:"firstUsedAt,omitempty"`
	AdminNotes   string     `json:"adminNotes,omitempty"`
}

func (s *BillingStore) ListKeysAdmin(ctx context.Context, accountID string, limit int) ([]AdminKeyDetail, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	where, args := "", []any{}
	if accountID != "" {
		where = `WHERE k.account_id = ?`
		args = append(args, accountID)
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT k.id, k.account_id, COALESCE(a.email, ''), k.key_prefix, COALESCE(k.last_four, ''),
		COALESCE(k.name, ''), k.plan, k.is_active, k.created_at, COALESCE(k.last_used_at, ''), COALESCE(k.revoked_at, ''),
		k.client_id, k.environment, k.scopes, k.monthly_quota,
		COALESCE(k.expires_at, ''), COALESCE(k.disabled_at, ''), COALESCE(k.first_used_at, ''), k.admin_notes
		FROM api_keys k LEFT JOIN accounts a ON a.id = k.account_id `+where+` ORDER BY k.created_at DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminKeyDetail
	for rows.Next() {
		var key AdminKeyDetail
		var createdAt, lastUsedAt, revokedAt, expiresAt, disabledAt, firstUsedAt sqliteTime
		if err := rows.Scan(&key.ID, &key.AccountID, &key.AccountEmail, &key.Prefix, &key.LastFour, &key.Name,
			&key.Plan, &key.IsActive, &createdAt, &lastUsedAt, &revokedAt,
			&key.ClientID, &key.Environment, &key.Scopes, &key.MonthlyQuota,
			&expiresAt, &disabledAt, &firstUsedAt, &key.AdminNotes); err != nil {
			return nil, err
		}
		key.CreatedAt = createdAt.Time
		key.LastUsedAt = timePtr(lastUsedAt.Time)
		key.RevokedAt = timePtr(revokedAt.Time)
		key.ExpiresAt = timePtr(expiresAt.Time)
		key.DisabledAt = timePtr(disabledAt.Time)
		key.FirstUsedAt = timePtr(firstUsedAt.Time)
		out = append(out, key)
	}
	return out, rows.Err()
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// SetKeyDisabled temporarily disables (or re-enables) a key without revoking.
func (s *BillingStore) SetKeyDisabled(ctx context.Context, keyID string, disabled bool) error {
	if s == nil || s.db == nil {
		return nil
	}
	var disabledAt any
	if disabled {
		disabledAt = s.now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE api_keys SET disabled_at = ? WHERE id = ?`, disabledAt, keyID)
	return err
}

// SetKeyMetadata updates admin-editable key fields.
func (s *BillingStore) SetKeyMetadata(ctx context.Context, keyID string, name *string, environment *string, scopes *string, quota *int64, expiresAt *time.Time, notes *string, clientID *string) error {
	if s == nil || s.db == nil {
		return nil
	}
	set := func(column string, value any) error {
		_, err := s.db.ExecContext(ctx, `UPDATE api_keys SET `+column+` = ? WHERE id = ?`, value, keyID)
		return err
	}
	if name != nil {
		if err := set("name", strings.TrimSpace(*name)); err != nil {
			return err
		}
	}
	if environment != nil {
		env := strings.ToLower(strings.TrimSpace(*environment))
		switch env {
		case EnvProduction, EnvStaging, EnvDevelopment, EnvTest:
		default:
			return fmt.Errorf("invalid environment %q", env)
		}
		if err := set("environment", env); err != nil {
			return err
		}
	}
	if scopes != nil {
		if err := set("scopes", normalizeTags(*scopes)); err != nil {
			return err
		}
	}
	if quota != nil {
		if err := set("monthly_quota", *quota); err != nil {
			return err
		}
	}
	if expiresAt != nil {
		var value any
		if !expiresAt.IsZero() {
			value = expiresAt.UTC()
		}
		if err := set("expires_at", value); err != nil {
			return err
		}
	}
	if notes != nil {
		if err := set("admin_notes", strings.TrimSpace(*notes)); err != nil {
			return err
		}
	}
	if clientID != nil {
		if err := set("client_id", strings.TrimSpace(*clientID)); err != nil {
			return err
		}
	}
	return nil
}

// KeyUsable reports whether a key may authenticate right now, with a reason
// code when it may not.
func KeyUsable(key APIKeyRecord, keyDisabledAt time.Time, keyExpiresAt time.Time, accountStatus string, now time.Time) (bool, string) {
	switch {
	case !key.IsActive || !key.RevokedAt.IsZero():
		return false, "revoked"
	case !keyDisabledAt.IsZero():
		return false, "disabled"
	case !keyExpiresAt.IsZero() && now.After(keyExpiresAt):
		return false, "expired"
	case accountStatus == AccountStatusSuspended:
		return false, "account_suspended"
	case accountStatus == AccountStatusDeleted:
		return false, "account_deleted"
	default:
		return true, ""
	}
}

// GetAPIKeyAuth loads everything the auth path needs for one key hash.
func (s *BillingStore) GetAPIKeyAuth(ctx context.Context, hash string) (APIKeyRecord, Account, string, time.Time, time.Time, string, bool, error) {
	var key APIKeyRecord
	var account Account
	if s == nil || s.db == nil || hash == "" {
		return key, account, "", time.Time{}, time.Time{}, "", false, nil
	}
	row := s.db.QueryRowContext(ctx, `SELECT k.id, k.account_id, k.key_hash, k.key_prefix, COALESCE(k.name, ''),
		k.plan, k.is_active, k.created_at, COALESCE(k.last_used_at, ''), COALESCE(k.revoked_at, ''), COALESCE(k.last_four, ''),
		COALESCE(k.disabled_at, ''), COALESCE(k.expires_at, ''), k.client_id, COALESCE(k.first_used_at, ''),
		a.id, COALESCE(a.email, ''), COALESCE(a.stripe_customer_id, ''), COALESCE(a.stripe_subscription_id, ''),
		a.plan, a.subscription_status, a.created_at, a.updated_at, a.status
		FROM api_keys k JOIN accounts a ON a.id = k.account_id WHERE k.key_hash = ?`, hash)
	var keyCreated, keyLastUsed, keyRevoked, keyDisabled, keyExpires, keyFirstUsed, accountCreated, accountUpdated sqliteTime
	var clientID, accountStatus string
	err := row.Scan(&key.ID, &key.AccountID, &key.KeyHash, &key.KeyPrefix, &key.Name, &key.Plan, &key.IsActive,
		&keyCreated, &keyLastUsed, &keyRevoked, &key.LastFour,
		&keyDisabled, &keyExpires, &clientID, &keyFirstUsed,
		&account.ID, &account.Email, &account.StripeCustomerID, &account.StripeSubscriptionID,
		&account.Plan, &account.SubscriptionStatus, &accountCreated, &accountUpdated, &accountStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return key, account, "", time.Time{}, time.Time{}, "", false, nil
	}
	if err != nil {
		return key, account, "", time.Time{}, time.Time{}, "", false, err
	}
	key.CreatedAt, key.LastUsedAt, key.RevokedAt = keyCreated.Time, keyLastUsed.Time, keyRevoked.Time
	account.CreatedAt, account.UpdatedAt = accountCreated.Time, accountUpdated.Time
	if accountStatus == "" {
		accountStatus = AccountStatusActive
	}
	return key, account, clientID, keyDisabled.Time, keyExpires.Time, accountStatus, true, nil
}

// TouchAPIKeyUsedAt updates last_used_at and stamps first_used_at once.
func (s *BillingStore) TouchAPIKeyUsedAt(ctx context.Context, keyID string) {
	if s == nil || s.db == nil || keyID == "" {
		return
	}
	now := s.now().UTC()
	_, _ = s.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at = ?,
		first_used_at = COALESCE(first_used_at, ?) WHERE id = ?`, now, now, keyID)
}
