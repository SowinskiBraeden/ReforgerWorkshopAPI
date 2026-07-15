package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type BillingStore struct {
	db     *sql.DB
	now    func() time.Time
	dbPath string
}

type BillingResource struct {
	Key       string
	Value     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

const (
	PlanFree      = "free"
	PlanDeveloper = "developer"
	PlanPro       = "pro"

	SubscriptionStatusNone = "none"
)

type Account struct {
	ID                   string
	Email                string
	StripeCustomerID     string
	StripeSubscriptionID string
	Plan                 string
	SubscriptionStatus   string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type APIKeyRecord struct {
	ID         string
	AccountID  string
	KeyHash    string
	KeyPrefix  string
	Name       string
	Plan       string
	IsActive   bool
	CreatedAt  time.Time
	LastUsedAt time.Time
	RevokedAt  time.Time
	LastFour   string
}

type StripeEventRecord struct {
	ID          string
	EventType   string
	ReceivedAt  time.Time
	ProcessedAt time.Time
	Status      string
	Error       string
}

type CheckoutSessionRecord struct {
	SessionID         string
	Email             string
	ClientReferenceID string
	CustomerID        string
	PaymentIntentID   string
	PaymentStatus     string
	AmountTotal       int64
	Currency          string
	CheckoutURL       string
	CreatedAt         time.Time
	CompletedAt       time.Time
	RawEventJSON      []byte
}

func OpenBillingStore(path string) (*BillingStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "/var/lib/reforgermods-api/reforgermods-billing.db"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &BillingStore{db: db, now: time.Now, dbPath: path}
	if err := store.configure(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.Migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *BillingStore) configure() error {
	if _, err := s.db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		return err
	}
	_, err := s.db.Exec(`PRAGMA synchronous=NORMAL;`)
	return err
}

func (s *BillingStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *BillingStore) Migrate(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS stripe_resources (
			resource_key TEXT PRIMARY KEY,
			resource_value TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS stripe_checkout_sessions (
			session_id TEXT PRIMARY KEY,
			email TEXT,
			client_reference_id TEXT,
			customer_id TEXT,
			payment_intent_id TEXT,
			payment_status TEXT,
			amount_total INTEGER NOT NULL DEFAULT 0,
			currency TEXT,
			checkout_url TEXT,
			created_at TIMESTAMP NOT NULL,
			completed_at TIMESTAMP,
			raw_event_json BLOB
		);`,
		`CREATE INDEX IF NOT EXISTS idx_stripe_checkout_customer ON stripe_checkout_sessions(customer_id);`,
		`CREATE INDEX IF NOT EXISTS idx_stripe_checkout_email ON stripe_checkout_sessions(email);`,
		`CREATE TABLE IF NOT EXISTS accounts (
			id TEXT PRIMARY KEY,
			email TEXT,
			stripe_customer_id TEXT UNIQUE,
			stripe_subscription_id TEXT,
			plan TEXT NOT NULL DEFAULT 'free',
			subscription_status TEXT NOT NULL DEFAULT 'none',
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_accounts_subscription ON accounts(stripe_subscription_id);`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id TEXT PRIMARY KEY,
			account_id TEXT NOT NULL,
			key_hash TEXT NOT NULL UNIQUE,
			key_prefix TEXT NOT NULL,
			name TEXT,
			plan TEXT NOT NULL,
			is_active BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMP NOT NULL,
			last_used_at TIMESTAMP,
			revoked_at TIMESTAMP,
			last_four TEXT,
			FOREIGN KEY(account_id) REFERENCES accounts(id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_api_keys_account ON api_keys(account_id);`,
		`CREATE INDEX IF NOT EXISTS idx_api_keys_prefix ON api_keys(key_prefix);`,
		`CREATE TABLE IF NOT EXISTS login_tokens (
			id TEXT PRIMARY KEY,
			account_id TEXT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			created_at TIMESTAMP NOT NULL,
			expires_at TIMESTAMP NOT NULL,
			used_at TIMESTAMP,
			FOREIGN KEY(account_id) REFERENCES accounts(id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_login_tokens_account ON login_tokens(account_id);`,
		`CREATE TABLE IF NOT EXISTS stripe_events (
			id TEXT PRIMARY KEY,
			event_type TEXT NOT NULL,
			received_at TIMESTAMP NOT NULL,
			processed_at TIMESTAMP,
			status TEXT NOT NULL,
			error TEXT
		);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *BillingStore) UpsertAccount(ctx context.Context, account Account) (Account, error) {
	if s == nil || s.db == nil {
		return account, nil
	}
	now := s.now().UTC()
	if account.ID == "" {
		account.ID = newID("acct")
	}
	if account.Plan == "" {
		account.Plan = PlanFree
	}
	if account.SubscriptionStatus == "" {
		account.SubscriptionStatus = SubscriptionStatusNone
	}
	if account.CreatedAt.IsZero() {
		account.CreatedAt = now
	}
	account.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `INSERT INTO accounts (
		id, email, stripe_customer_id, stripe_subscription_id, plan, subscription_status, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		email=COALESCE(NULLIF(excluded.email, ''), accounts.email),
		stripe_customer_id=COALESCE(NULLIF(excluded.stripe_customer_id, ''), accounts.stripe_customer_id),
		stripe_subscription_id=COALESCE(NULLIF(excluded.stripe_subscription_id, ''), accounts.stripe_subscription_id),
		plan=excluded.plan,
		subscription_status=excluded.subscription_status,
		updated_at=excluded.updated_at`,
		account.ID, account.Email, account.StripeCustomerID, account.StripeSubscriptionID, account.Plan, account.SubscriptionStatus, account.CreatedAt.UTC(), account.UpdatedAt.UTC(),
	)
	if err != nil {
		return account, err
	}
	return account, nil
}

func (s *BillingStore) UpsertAccountByStripeCustomer(ctx context.Context, account Account) (Account, error) {
	if s == nil || s.db == nil {
		return account, nil
	}
	if account.StripeCustomerID == "" {
		return s.UpsertAccount(ctx, account)
	}
	existing, ok, err := s.GetAccountByStripeCustomer(ctx, account.StripeCustomerID)
	if err != nil {
		return account, err
	}
	if ok {
		account.ID = existing.ID
		account.CreatedAt = existing.CreatedAt
	}
	return s.UpsertAccount(ctx, account)
}

func (s *BillingStore) GetAccount(ctx context.Context, id string) (Account, bool, error) {
	return s.getAccount(ctx, `WHERE id = ?`, id)
}

func (s *BillingStore) GetAccountByStripeCustomer(ctx context.Context, customerID string) (Account, bool, error) {
	return s.getAccount(ctx, `WHERE stripe_customer_id = ?`, customerID)
}

func (s *BillingStore) GetAccountBySubscription(ctx context.Context, subscriptionID string) (Account, bool, error) {
	return s.getAccount(ctx, `WHERE stripe_subscription_id = ?`, subscriptionID)
}

func (s *BillingStore) GetAccountByEmail(ctx context.Context, email string) (Account, bool, error) {
	return s.getAccount(ctx, `WHERE email = ? COLLATE NOCASE ORDER BY updated_at DESC LIMIT 1`, strings.TrimSpace(email))
}

func (s *BillingStore) getAccount(ctx context.Context, where string, arg string) (Account, bool, error) {
	var out Account
	if s == nil || s.db == nil || strings.TrimSpace(arg) == "" {
		return out, false, nil
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, COALESCE(email, ''), COALESCE(stripe_customer_id, ''),
		COALESCE(stripe_subscription_id, ''), plan, subscription_status, created_at, updated_at FROM accounts `+where, arg)
	var createdAt, updatedAt sqliteTime
	err := row.Scan(&out.ID, &out.Email, &out.StripeCustomerID, &out.StripeSubscriptionID, &out.Plan, &out.SubscriptionStatus, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	out.CreatedAt = createdAt.Time
	out.UpdatedAt = updatedAt.Time
	return out, true, nil
}

func (s *BillingStore) CreateAPIKey(ctx context.Context, key APIKeyRecord) (APIKeyRecord, error) {
	if s == nil || s.db == nil {
		return key, nil
	}
	if key.ID == "" {
		key.ID = newID("key")
	}
	if key.Plan == "" {
		key.Plan = PlanFree
	}
	key.IsActive = true
	now := s.now().UTC()
	if key.CreatedAt.IsZero() {
		key.CreatedAt = now
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO api_keys (
		id, account_id, key_hash, key_prefix, name, plan, is_active, created_at, last_four
	) VALUES (?, ?, ?, ?, ?, ?, true, ?, ?)`,
		key.ID, key.AccountID, key.KeyHash, key.KeyPrefix, key.Name, key.Plan, key.CreatedAt.UTC(), key.LastFour,
	)
	return key, err
}

func (s *BillingStore) ActiveAPIKeysForAccount(ctx context.Context, accountID string) ([]APIKeyRecord, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, account_id, key_hash, key_prefix, COALESCE(name, ''), plan,
		is_active, created_at, COALESCE(last_used_at, ''), COALESCE(revoked_at, ''), COALESCE(last_four, '')
		FROM api_keys WHERE account_id = ? AND is_active = true AND revoked_at IS NULL ORDER BY created_at DESC`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIKeyRecord
	for rows.Next() {
		key, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, key)
	}
	return out, rows.Err()
}

func (s *BillingStore) GetAPIKeyByHash(ctx context.Context, hash string) (APIKeyRecord, Account, bool, error) {
	var key APIKeyRecord
	var account Account
	if s == nil || s.db == nil || hash == "" {
		return key, account, false, nil
	}
	row := s.db.QueryRowContext(ctx, `SELECT k.id, k.account_id, k.key_hash, k.key_prefix, COALESCE(k.name, ''),
		k.plan, k.is_active, k.created_at, COALESCE(k.last_used_at, ''), COALESCE(k.revoked_at, ''), COALESCE(k.last_four, ''),
		a.id, COALESCE(a.email, ''), COALESCE(a.stripe_customer_id, ''), COALESCE(a.stripe_subscription_id, ''),
		a.plan, a.subscription_status, a.created_at, a.updated_at
		FROM api_keys k JOIN accounts a ON a.id = k.account_id WHERE k.key_hash = ?`, hash)
	var keyCreated, keyLastUsed, keyRevoked, accountCreated, accountUpdated sqliteTime
	err := row.Scan(&key.ID, &key.AccountID, &key.KeyHash, &key.KeyPrefix, &key.Name, &key.Plan, &key.IsActive, &keyCreated, &keyLastUsed, &keyRevoked, &key.LastFour,
		&account.ID, &account.Email, &account.StripeCustomerID, &account.StripeSubscriptionID, &account.Plan, &account.SubscriptionStatus, &accountCreated, &accountUpdated)
	if errors.Is(err, sql.ErrNoRows) {
		return key, account, false, nil
	}
	if err != nil {
		return key, account, false, err
	}
	key.CreatedAt, key.LastUsedAt, key.RevokedAt = keyCreated.Time, keyLastUsed.Time, keyRevoked.Time
	account.CreatedAt, account.UpdatedAt = accountCreated.Time, accountUpdated.Time
	return key, account, true, nil
}

func (s *BillingStore) RevokeAPIKey(ctx context.Context, accountID string, keyID string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE api_keys SET is_active = false, revoked_at = ? WHERE id = ? AND account_id = ?`, s.now().UTC(), keyID, accountID)
	return err
}

func (s *BillingStore) RevokePaidKeysForAccount(ctx context.Context, accountID string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE api_keys SET is_active = false, revoked_at = ? WHERE account_id = ? AND plan <> 'free' AND is_active = true`, s.now().UTC(), accountID)
	return err
}

func (s *BillingStore) TouchAPIKeyUsed(ctx context.Context, keyID string) {
	if s == nil || s.db == nil || keyID == "" {
		return
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at = ? WHERE id = ?`, s.now().UTC(), keyID)
}

// CreateLoginToken stores a hashed single-use sign-in token. It reports
// whether the token was stored; it declines (without error) when another
// token was issued for the account within the cooldown window.
func (s *BillingStore) CreateLoginToken(ctx context.Context, accountID string, tokenHash string, ttl time.Duration, cooldown time.Duration) (bool, error) {
	if s == nil || s.db == nil || accountID == "" || tokenHash == "" {
		return false, nil
	}
	now := s.now().UTC()
	if cooldown > 0 {
		var recent int
		row := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM login_tokens WHERE account_id = ? AND created_at > ?`, accountID, now.Add(-cooldown))
		if err := row.Scan(&recent); err != nil {
			return false, err
		}
		if recent > 0 {
			return false, nil
		}
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM login_tokens WHERE expires_at < ?`, now.Add(-24*time.Hour))
	_, err := s.db.ExecContext(ctx, `INSERT INTO login_tokens (id, account_id, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		newID("lgn"), accountID, tokenHash, now, now.Add(ttl))
	if err != nil {
		return false, err
	}
	return true, nil
}

// ConsumeLoginToken marks an unexpired, unused token as used and returns the
// owning account ID.
func (s *BillingStore) ConsumeLoginToken(ctx context.Context, tokenHash string) (string, bool, error) {
	if s == nil || s.db == nil || tokenHash == "" {
		return "", false, nil
	}
	now := s.now().UTC()
	var id, accountID string
	row := s.db.QueryRowContext(ctx, `SELECT id, account_id FROM login_tokens WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`, tokenHash, now)
	err := row.Scan(&id, &accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE login_tokens SET used_at = ? WHERE id = ? AND used_at IS NULL`, now, id)
	if err != nil {
		return "", false, err
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		return "", false, err
	}
	return accountID, true, nil
}

func (s *BillingStore) BeginStripeEvent(ctx context.Context, id string, eventType string) (bool, error) {
	if s == nil || s.db == nil || id == "" {
		return true, nil
	}
	now := s.now().UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO stripe_events (id, event_type, received_at, status) VALUES (?, ?, ?, 'processing')`, id, eventType, now)
	if err == nil {
		return true, nil
	}
	if strings.Contains(err.Error(), "UNIQUE") {
		var status string
		row := s.db.QueryRowContext(ctx, `SELECT status FROM stripe_events WHERE id = ?`, id)
		if scanErr := row.Scan(&status); scanErr == nil && status == "processed" {
			return false, nil
		}
		return true, nil
	}
	return false, err
}

func (s *BillingStore) FinishStripeEvent(ctx context.Context, id string, status string, message string) error {
	if s == nil || s.db == nil || id == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE stripe_events SET processed_at = ?, status = ?, error = ? WHERE id = ?`, s.now().UTC(), status, message, id)
	return err
}

func (s *BillingStore) GetResource(ctx context.Context, key string) (BillingResource, bool, error) {
	var out BillingResource
	if s == nil || s.db == nil {
		return out, false, nil
	}
	row := s.db.QueryRowContext(ctx, `SELECT resource_key, resource_value, created_at, updated_at FROM stripe_resources WHERE resource_key = ?`, key)
	var createdAt, updatedAt sqliteTime
	err := row.Scan(&out.Key, &out.Value, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	out.CreatedAt = createdAt.Time
	out.UpdatedAt = updatedAt.Time
	return out, true, nil
}

func (s *BillingStore) PutResource(ctx context.Context, key string, value string) error {
	if s == nil || s.db == nil {
		return nil
	}
	now := s.now().UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO stripe_resources (
		resource_key, resource_value, created_at, updated_at
	) VALUES (?, ?, ?, ?)
	ON CONFLICT(resource_key) DO UPDATE SET
		resource_value=excluded.resource_value,
		updated_at=excluded.updated_at`,
		key, value, now, now,
	)
	return err
}

func (s *BillingStore) PutCheckoutSession(ctx context.Context, record CheckoutSessionRecord) error {
	if s == nil || s.db == nil || strings.TrimSpace(record.SessionID) == "" {
		return nil
	}
	now := s.now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	var completedAt any
	if !record.CompletedAt.IsZero() {
		completedAt = record.CompletedAt.UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO stripe_checkout_sessions (
		session_id, email, client_reference_id, customer_id, payment_intent_id,
		payment_status, amount_total, currency, checkout_url, created_at,
		completed_at, raw_event_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(session_id) DO UPDATE SET
		email=COALESCE(NULLIF(excluded.email, ''), stripe_checkout_sessions.email),
		client_reference_id=COALESCE(NULLIF(excluded.client_reference_id, ''), stripe_checkout_sessions.client_reference_id),
		customer_id=COALESCE(NULLIF(excluded.customer_id, ''), stripe_checkout_sessions.customer_id),
		payment_intent_id=COALESCE(NULLIF(excluded.payment_intent_id, ''), stripe_checkout_sessions.payment_intent_id),
		payment_status=COALESCE(NULLIF(excluded.payment_status, ''), stripe_checkout_sessions.payment_status),
		amount_total=CASE WHEN excluded.amount_total > 0 THEN excluded.amount_total ELSE stripe_checkout_sessions.amount_total END,
		currency=COALESCE(NULLIF(excluded.currency, ''), stripe_checkout_sessions.currency),
		checkout_url=COALESCE(NULLIF(excluded.checkout_url, ''), stripe_checkout_sessions.checkout_url),
		completed_at=COALESCE(excluded.completed_at, stripe_checkout_sessions.completed_at),
		raw_event_json=COALESCE(excluded.raw_event_json, stripe_checkout_sessions.raw_event_json)`,
		record.SessionID, record.Email, record.ClientReferenceID, record.CustomerID, record.PaymentIntentID,
		record.PaymentStatus, record.AmountTotal, record.Currency, record.CheckoutURL, record.CreatedAt.UTC(),
		completedAt, record.RawEventJSON,
	)
	return err
}

func (s *BillingStore) GetCheckoutSession(ctx context.Context, sessionID string) (CheckoutSessionRecord, bool, error) {
	var out CheckoutSessionRecord
	if s == nil || s.db == nil {
		return out, false, nil
	}
	row := s.db.QueryRowContext(ctx, `SELECT session_id, COALESCE(email, ''), COALESCE(client_reference_id, ''),
		COALESCE(customer_id, ''), COALESCE(payment_intent_id, ''), COALESCE(payment_status, ''),
		amount_total, COALESCE(currency, ''), COALESCE(checkout_url, ''),
		created_at, completed_at, raw_event_json
		FROM stripe_checkout_sessions WHERE session_id = ?`, sessionID)
	var createdAt, completedAt sqliteTime
	err := row.Scan(&out.SessionID, &out.Email, &out.ClientReferenceID, &out.CustomerID, &out.PaymentIntentID,
		&out.PaymentStatus, &out.AmountTotal, &out.Currency, &out.CheckoutURL,
		&createdAt, &completedAt, &out.RawEventJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	out.CreatedAt = createdAt.Time
	out.CompletedAt = completedAt.Time
	return out, true, nil
}

type apiKeyScanner interface {
	Scan(dest ...any) error
}

func scanAPIKey(row apiKeyScanner) (APIKeyRecord, error) {
	var key APIKeyRecord
	var createdAt, lastUsedAt, revokedAt sqliteTime
	err := row.Scan(&key.ID, &key.AccountID, &key.KeyHash, &key.KeyPrefix, &key.Name, &key.Plan,
		&key.IsActive, &createdAt, &lastUsedAt, &revokedAt, &key.LastFour)
	if err != nil {
		return key, err
	}
	key.CreatedAt = createdAt.Time
	key.LastUsedAt = lastUsedAt.Time
	key.RevokedAt = revokedAt.Time
	return key, nil
}

func newID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return prefix + "_" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}
