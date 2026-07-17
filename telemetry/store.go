package telemetry

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Store owns the telemetry SQLite database. All timestamps are stored as
// UTC unix milliseconds (INTEGER) unless noted otherwise.
type Store struct {
	db     *sql.DB
	dbPath string
	now    func() time.Time
}

func Open(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "data/telemetry.db"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		return nil, err
	}
	// One writer connection avoids SQLITE_BUSY churn; reads share it. The
	// recorder batches inserts so this is not a throughput bottleneck at the
	// service's request volumes.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &Store{db: db, dbPath: path, now: time.Now}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS request_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			request_id TEXT NOT NULL DEFAULT '',
			trace_id TEXT NOT NULL DEFAULT '',
			at INTEGER NOT NULL,
			method TEXT NOT NULL DEFAULT 'GET',
			route_template TEXT NOT NULL DEFAULT '',
			request_path TEXT NOT NULL DEFAULT '',
			query TEXT NOT NULL DEFAULT '',
			endpoint_group TEXT NOT NULL DEFAULT '',
			status INTEGER NOT NULL DEFAULT 0,
			duration_ms REAL NOT NULL DEFAULT 0,
			response_bytes INTEGER NOT NULL DEFAULT 0,
			request_bytes INTEGER NOT NULL DEFAULT 0,
			api_version TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT 'unknown',
			client_kind TEXT NOT NULL DEFAULT 'unknown',
			auth_type TEXT NOT NULL DEFAULT 'none',
			account_id TEXT NOT NULL DEFAULT '',
			api_key_id TEXT NOT NULL DEFAULT '',
			api_client_id TEXT NOT NULL DEFAULT '',
			client_name TEXT NOT NULL DEFAULT '',
			client_version TEXT NOT NULL DEFAULT '',
			client_verified INTEGER NOT NULL DEFAULT 0,
			user_agent TEXT NOT NULL DEFAULT '',
			country_code TEXT NOT NULL DEFAULT 'ZZ',
			asn TEXT NOT NULL DEFAULT '',
			network_name TEXT NOT NULL DEFAULT '',
			network_id TEXT NOT NULL DEFAULT '',
			is_hosting TEXT NOT NULL DEFAULT 'unknown',
			via_proxy INTEGER NOT NULL DEFAULT 0,
			cache_status TEXT NOT NULL DEFAULT '',
			refresh_result TEXT NOT NULL DEFAULT '',
			rate_limited INTEGER NOT NULL DEFAULT 0,
			rate_limit_limit INTEGER NOT NULL DEFAULT 0,
			rate_bucket TEXT NOT NULL DEFAULT '',
			error_category TEXT NOT NULL DEFAULT '',
			error_code TEXT NOT NULL DEFAULT '',
			search_term TEXT NOT NULL DEFAULT '',
			result_count INTEGER NOT NULL DEFAULT -1,
			mod_id TEXT NOT NULL DEFAULT '',
			app_version TEXT NOT NULL DEFAULT '',
			instance_id TEXT NOT NULL DEFAULT '',
			dedupe_key TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_re_at ON request_events(at);`,
		`CREATE INDEX IF NOT EXISTS idx_re_route_at ON request_events(route_template, at);`,
		`CREATE INDEX IF NOT EXISTS idx_re_account_at ON request_events(account_id, at) WHERE account_id <> '';`,
		`CREATE INDEX IF NOT EXISTS idx_re_key_at ON request_events(api_key_id, at) WHERE api_key_id <> '';`,
		`CREATE INDEX IF NOT EXISTS idx_re_network_at ON request_events(network_id, at) WHERE network_id <> '';`,
		`CREATE INDEX IF NOT EXISTS idx_re_source_at ON request_events(source, at);`,
		`CREATE INDEX IF NOT EXISTS idx_re_status_at ON request_events(status, at);`,
		`CREATE INDEX IF NOT EXISTS idx_re_request_id ON request_events(request_id) WHERE request_id <> '';`,
		`CREATE INDEX IF NOT EXISTS idx_re_duration_at ON request_events(at, duration_ms);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_re_dedupe ON request_events(dedupe_key) WHERE dedupe_key IS NOT NULL;`,

		`CREATE TABLE IF NOT EXISTS request_errors (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			error_id TEXT NOT NULL UNIQUE,
			request_id TEXT NOT NULL DEFAULT '',
			trace_id TEXT NOT NULL DEFAULT '',
			job_id TEXT NOT NULL DEFAULT '',
			at INTEGER NOT NULL,
			severity TEXT NOT NULL DEFAULT 'error',
			category TEXT NOT NULL DEFAULT '',
			code TEXT NOT NULL DEFAULT '',
			message TEXT NOT NULL DEFAULT '',
			stack TEXT NOT NULL DEFAULT '',
			method TEXT NOT NULL DEFAULT '',
			route_template TEXT NOT NULL DEFAULT '',
			request_path TEXT NOT NULL DEFAULT '',
			status INTEGER NOT NULL DEFAULT 0,
			source TEXT NOT NULL DEFAULT '',
			account_id TEXT NOT NULL DEFAULT '',
			api_key_id TEXT NOT NULL DEFAULT '',
			client_name TEXT NOT NULL DEFAULT '',
			country_code TEXT NOT NULL DEFAULT '',
			network_id TEXT NOT NULL DEFAULT '',
			app_version TEXT NOT NULL DEFAULT '',
			fingerprint TEXT NOT NULL DEFAULT '',
			resolution TEXT NOT NULL DEFAULT 'open',
			notes TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL DEFAULT 0,
			dedupe_key TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_err_at ON request_errors(at);`,
		`CREATE INDEX IF NOT EXISTS idx_err_fingerprint ON request_errors(fingerprint, at);`,
		`CREATE INDEX IF NOT EXISTS idx_err_resolution ON request_errors(resolution, at);`,
		`CREATE INDEX IF NOT EXISTS idx_err_request ON request_errors(request_id) WHERE request_id <> '';`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_err_dedupe ON request_errors(dedupe_key) WHERE dedupe_key IS NOT NULL;`,

		`CREATE TABLE IF NOT EXISTS background_jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id TEXT NOT NULL UNIQUE,
			request_id TEXT NOT NULL DEFAULT '',
			parent_job_id TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT '',
			resource_key TEXT NOT NULL DEFAULT '',
			resource_url TEXT NOT NULL DEFAULT '',
			queue TEXT NOT NULL DEFAULT '',
			priority TEXT NOT NULL DEFAULT '',
			enqueued_at INTEGER NOT NULL DEFAULT 0,
			started_at INTEGER NOT NULL DEFAULT 0,
			finished_at INTEGER NOT NULL DEFAULT 0,
			queue_wait_ms REAL NOT NULL DEFAULT 0,
			duration_ms REAL NOT NULL DEFAULT 0,
			worker INTEGER NOT NULL DEFAULT 0,
			attempt INTEGER NOT NULL DEFAULT 1,
			status TEXT NOT NULL DEFAULT 'queued',
			status_code INTEGER NOT NULL DEFAULT 0,
			failure_reason TEXT NOT NULL DEFAULT '',
			deduplicated INTEGER NOT NULL DEFAULT 0,
			panic_recovered INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_enqueued ON background_jobs(enqueued_at);`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_status ON background_jobs(status, enqueued_at);`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_request ON background_jobs(request_id) WHERE request_id <> '';`,

		`CREATE TABLE IF NOT EXISTS structured_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			at INTEGER NOT NULL,
			level TEXT NOT NULL DEFAULT 'info',
			caller TEXT NOT NULL DEFAULT '',
			message TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT '',
			trace_id TEXT NOT NULL DEFAULT '',
			job_id TEXT NOT NULL DEFAULT '',
			route TEXT NOT NULL DEFAULT '',
			path TEXT NOT NULL DEFAULT '',
			status INTEGER NOT NULL DEFAULT 0,
			error_category TEXT NOT NULL DEFAULT '',
			country_code TEXT NOT NULL DEFAULT '',
			network_id TEXT NOT NULL DEFAULT '',
			account_id TEXT NOT NULL DEFAULT '',
			client_name TEXT NOT NULL DEFAULT '',
			api_key_id TEXT NOT NULL DEFAULT '',
			cache_status TEXT NOT NULL DEFAULT '',
			instance_id TEXT NOT NULL DEFAULT '',
			app_version TEXT NOT NULL DEFAULT '',
			fields TEXT NOT NULL DEFAULT '',
			dedupe_key TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_logs_at ON structured_logs(at);`,
		`CREATE INDEX IF NOT EXISTS idx_logs_level_at ON structured_logs(level, at);`,
		`CREATE INDEX IF NOT EXISTS idx_logs_request ON structured_logs(request_id) WHERE request_id <> '';`,
		`CREATE INDEX IF NOT EXISTS idx_logs_job ON structured_logs(job_id) WHERE job_id <> '';`,
		`CREATE INDEX IF NOT EXISTS idx_logs_message ON structured_logs(message, at);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_logs_dedupe ON structured_logs(dedupe_key) WHERE dedupe_key IS NOT NULL;`,

		// bucket formats: hourly "2006-01-02T15", daily "2006-01-02" (UTC).
		`CREATE TABLE IF NOT EXISTS usage_hourly (
			bucket TEXT NOT NULL,
			source TEXT NOT NULL,
			endpoint_group TEXT NOT NULL,
			requests INTEGER NOT NULL DEFAULT 0,
			served INTEGER NOT NULL DEFAULT 0,
			accepted INTEGER NOT NULL DEFAULT 0,
			errors INTEGER NOT NULL DEFAULT 0,
			rate_limited INTEGER NOT NULL DEFAULT 0,
			cache_hit INTEGER NOT NULL DEFAULT 0,
			cache_stale INTEGER NOT NULL DEFAULT 0,
			cache_miss INTEGER NOT NULL DEFAULT 0,
			cache_bypass INTEGER NOT NULL DEFAULT 0,
			unique_networks INTEGER NOT NULL DEFAULT 0,
			unique_accounts INTEGER NOT NULL DEFAULT 0,
			unique_keys INTEGER NOT NULL DEFAULT 0,
			bytes_out INTEGER NOT NULL DEFAULT 0,
			dur_count INTEGER NOT NULL DEFAULT 0,
			dur_sum_ms REAL NOT NULL DEFAULT 0,
			dur_min_ms REAL NOT NULL DEFAULT 0,
			dur_max_ms REAL NOT NULL DEFAULT 0,
			p50_ms REAL NOT NULL DEFAULT 0,
			p75_ms REAL NOT NULL DEFAULT 0,
			p90_ms REAL NOT NULL DEFAULT 0,
			p95_ms REAL NOT NULL DEFAULT 0,
			p99_ms REAL NOT NULL DEFAULT 0,
			PRIMARY KEY (bucket, source, endpoint_group)
		);`,
		`CREATE TABLE IF NOT EXISTS usage_daily (
			bucket TEXT NOT NULL,
			source TEXT NOT NULL,
			endpoint_group TEXT NOT NULL,
			requests INTEGER NOT NULL DEFAULT 0,
			served INTEGER NOT NULL DEFAULT 0,
			accepted INTEGER NOT NULL DEFAULT 0,
			errors INTEGER NOT NULL DEFAULT 0,
			rate_limited INTEGER NOT NULL DEFAULT 0,
			cache_hit INTEGER NOT NULL DEFAULT 0,
			cache_stale INTEGER NOT NULL DEFAULT 0,
			cache_miss INTEGER NOT NULL DEFAULT 0,
			cache_bypass INTEGER NOT NULL DEFAULT 0,
			unique_networks INTEGER NOT NULL DEFAULT 0,
			unique_accounts INTEGER NOT NULL DEFAULT 0,
			unique_keys INTEGER NOT NULL DEFAULT 0,
			bytes_out INTEGER NOT NULL DEFAULT 0,
			dur_count INTEGER NOT NULL DEFAULT 0,
			dur_sum_ms REAL NOT NULL DEFAULT 0,
			dur_min_ms REAL NOT NULL DEFAULT 0,
			dur_max_ms REAL NOT NULL DEFAULT 0,
			p50_ms REAL NOT NULL DEFAULT 0,
			p75_ms REAL NOT NULL DEFAULT 0,
			p90_ms REAL NOT NULL DEFAULT 0,
			p95_ms REAL NOT NULL DEFAULT 0,
			p99_ms REAL NOT NULL DEFAULT 0,
			PRIMARY KEY (bucket, source, endpoint_group)
		);`,
		`CREATE TABLE IF NOT EXISTS endpoint_daily (
			day TEXT NOT NULL,
			route_template TEXT NOT NULL,
			method TEXT NOT NULL,
			requests INTEGER NOT NULL DEFAULT 0,
			served INTEGER NOT NULL DEFAULT 0,
			errors INTEGER NOT NULL DEFAULT 0,
			rate_limited INTEGER NOT NULL DEFAULT 0,
			unique_accounts INTEGER NOT NULL DEFAULT 0,
			unique_keys INTEGER NOT NULL DEFAULT 0,
			unique_networks INTEGER NOT NULL DEFAULT 0,
			cache_hit INTEGER NOT NULL DEFAULT 0,
			cache_stale INTEGER NOT NULL DEFAULT 0,
			cache_miss INTEGER NOT NULL DEFAULT 0,
			bytes_out INTEGER NOT NULL DEFAULT 0,
			dur_count INTEGER NOT NULL DEFAULT 0,
			dur_sum_ms REAL NOT NULL DEFAULT 0,
			dur_min_ms REAL NOT NULL DEFAULT 0,
			dur_max_ms REAL NOT NULL DEFAULT 0,
			p50_ms REAL NOT NULL DEFAULT 0,
			p90_ms REAL NOT NULL DEFAULT 0,
			p95_ms REAL NOT NULL DEFAULT 0,
			p99_ms REAL NOT NULL DEFAULT 0,
			first_at INTEGER NOT NULL DEFAULT 0,
			last_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (day, route_template, method)
		);`,
		`CREATE TABLE IF NOT EXISTS client_daily (
			day TEXT NOT NULL,
			client_key TEXT NOT NULL,
			api_client_id TEXT NOT NULL DEFAULT '',
			client_name TEXT NOT NULL DEFAULT '',
			verified INTEGER NOT NULL DEFAULT 0,
			requests INTEGER NOT NULL DEFAULT 0,
			errors INTEGER NOT NULL DEFAULT 0,
			rate_limited INTEGER NOT NULL DEFAULT 0,
			accepted INTEGER NOT NULL DEFAULT 0,
			cache_hit INTEGER NOT NULL DEFAULT 0,
			cache_stale INTEGER NOT NULL DEFAULT 0,
			cache_miss INTEGER NOT NULL DEFAULT 0,
			countries TEXT NOT NULL DEFAULT '',
			last_version TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (day, client_key)
		);`,
		`CREATE TABLE IF NOT EXISTS country_daily (
			day TEXT NOT NULL,
			country_code TEXT NOT NULL,
			requests INTEGER NOT NULL DEFAULT 0,
			errors INTEGER NOT NULL DEFAULT 0,
			rate_limited INTEGER NOT NULL DEFAULT 0,
			unique_networks INTEGER NOT NULL DEFAULT 0,
			dur_count INTEGER NOT NULL DEFAULT 0,
			dur_sum_ms REAL NOT NULL DEFAULT 0,
			PRIMARY KEY (day, country_code)
		);`,
		`CREATE TABLE IF NOT EXISTS network_daily (
			day TEXT NOT NULL,
			network_id TEXT NOT NULL,
			country_code TEXT NOT NULL DEFAULT '',
			asn TEXT NOT NULL DEFAULT '',
			network_name TEXT NOT NULL DEFAULT '',
			is_hosting TEXT NOT NULL DEFAULT 'unknown',
			requests INTEGER NOT NULL DEFAULT 0,
			errors INTEGER NOT NULL DEFAULT 0,
			rate_limited INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (day, network_id)
		);`,
		`CREATE TABLE IF NOT EXISTS search_daily (
			day TEXT NOT NULL,
			term TEXT NOT NULL,
			searches INTEGER NOT NULL DEFAULT 0,
			empty_results INTEGER NOT NULL DEFAULT 0,
			errors INTEGER NOT NULL DEFAULT 0,
			dur_count INTEGER NOT NULL DEFAULT 0,
			dur_sum_ms REAL NOT NULL DEFAULT 0,
			cache_hit INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (day, term)
		);`,
		`CREATE TABLE IF NOT EXISTS mod_daily (
			day TEXT NOT NULL,
			mod_id TEXT NOT NULL,
			requests INTEGER NOT NULL DEFAULT 0,
			unique_networks INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (day, mod_id)
		);`,
		`CREATE TABLE IF NOT EXISTS entity_activity (
			entity_type TEXT NOT NULL,
			entity_id TEXT NOT NULL,
			day TEXT NOT NULL,
			requests INTEGER NOT NULL DEFAULT 0,
			errors INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (entity_type, entity_id, day)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_activity_day ON entity_activity(entity_type, day);`,
		`CREATE TABLE IF NOT EXISTS entity_profiles (
			entity_type TEXT NOT NULL,
			entity_id TEXT NOT NULL,
			first_seen_at INTEGER NOT NULL DEFAULT 0,
			last_seen_at INTEGER NOT NULL DEFAULT 0,
			days_active INTEGER NOT NULL DEFAULT 0,
			last_country TEXT NOT NULL DEFAULT '',
			last_client_name TEXT NOT NULL DEFAULT '',
			last_version TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (entity_type, entity_id)
		);`,

		`CREATE TABLE IF NOT EXISTS admin_users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'viewer',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			disabled_at INTEGER NOT NULL DEFAULT 0,
			last_login_at INTEGER NOT NULL DEFAULT 0,
			created_by TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE TABLE IF NOT EXISTS admin_audit_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			at INTEGER NOT NULL,
			actor TEXT NOT NULL DEFAULT '',
			actor_role TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT '',
			target_type TEXT NOT NULL DEFAULT '',
			target_id TEXT NOT NULL DEFAULT '',
			details TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE INDEX IF NOT EXISTS idx_audit_at ON admin_audit_events(at);`,

		`CREATE TABLE IF NOT EXISTS aggregation_state (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE TABLE IF NOT EXISTS import_files (
			path TEXT PRIMARY KEY,
			offset INTEGER NOT NULL DEFAULT 0,
			size INTEGER NOT NULL DEFAULT 0,
			imported_lines INTEGER NOT NULL DEFAULT 0,
			malformed_lines INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE TABLE IF NOT EXISTS import_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			started_at INTEGER NOT NULL,
			finished_at INTEGER NOT NULL DEFAULT 0,
			dry_run INTEGER NOT NULL DEFAULT 0,
			from_day TEXT NOT NULL DEFAULT '',
			to_day TEXT NOT NULL DEFAULT '',
			files INTEGER NOT NULL DEFAULT 0,
			requests INTEGER NOT NULL DEFAULT 0,
			jobs INTEGER NOT NULL DEFAULT 0,
			errors_recorded INTEGER NOT NULL DEFAULT 0,
			logs INTEGER NOT NULL DEFAULT 0,
			malformed INTEGER NOT NULL DEFAULT 0,
			skipped INTEGER NOT NULL DEFAULT 0,
			note TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE TABLE IF NOT EXISTS telemetry_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL DEFAULT 0,
			updated_by TEXT NOT NULL DEFAULT ''
		);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// ms converts a time to stored unix milliseconds (0 for zero times).
func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().UnixMilli()
}

func fromMs(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// DayKey formats the UTC day bucket for a time.
func DayKey(t time.Time) string { return t.UTC().Format("2006-01-02") }

// HourKey formats the UTC hour bucket for a time.
func HourKey(t time.Time) string { return t.UTC().Format("2006-01-02T15") }
