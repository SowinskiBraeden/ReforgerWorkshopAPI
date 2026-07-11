package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/models"
	_ "github.com/mattn/go-sqlite3"
)

type sqliteTime struct {
	time.Time
	Valid bool
}

func (t *sqliteTime) Scan(value any) error {
	if value == nil {
		t.Valid = false
		t.Time = time.Time{}
		return nil
	}
	switch v := value.(type) {
	case time.Time:
		t.Time = v
		t.Valid = true
		return nil
	case string:
		return t.scanString(v)
	case []byte:
		return t.scanString(string(v))
	default:
		return fmt.Errorf("unsupported sqlite time type %T", value)
	}
}

func (t *sqliteTime) scanString(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		t.Valid = false
		t.Time = time.Time{}
		return nil
	}
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			t.Time = parsed
			t.Valid = true
			return nil
		}
	}
	return fmt.Errorf("invalid sqlite time %q", value)
}

type IndexStore struct {
	db     *sql.DB
	ftsOK  bool
	now    func() time.Time
	dbPath string
}

type PersistentCacheEntry struct {
	CacheKey          string
	ResourceType      string
	EndpointGroup     string
	StatusCode        int
	HeadersJSON       string
	Body              []byte
	FreshUntil        time.Time
	StaleUntil        time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
	LastAccessedAt    time.Time
	AccessCount       int64
	LastRefreshStatus string
	LastRefreshError  string
}

type ModIndexRecord struct {
	ID                string
	Name              string
	Author            string
	Summary           string
	Version           string
	Size              string
	Downloads         int
	Subscribers       int
	LastModified      string
	OfficialURL       string
	APIURL            string
	ImageURL          string
	TagsJSON          string
	FirstSeenAt       time.Time
	LastSeenAt        time.Time
	LastRefreshedAt   time.Time
	DetailRefreshedAt time.Time
	RefreshStatus     string
	RefreshError      string
	RawJSON           string
}

type ModDetailRecord struct {
	ModID            string
	Body             []byte
	DependenciesJSON string
	ScenariosJSON    string
	FreshUntil       time.Time
	StaleUntil       time.Time
	LastRefreshedAt  time.Time
	LastAccessedAt   time.Time
	RefreshStatus    string
	RefreshError     string
}

type ListPageRecord struct {
	CacheKey        string
	Page            int
	Sort            string
	Search          string
	ResourceType    string
	ModIDsJSON      string
	Body            []byte
	FreshUntil      time.Time
	StaleUntil      time.Time
	LastRefreshedAt time.Time
	LastAccessedAt  time.Time
	RefreshStatus   string
	RefreshError    string
}

func OpenIndexStore(path string) (*IndexStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "/var/lib/reforgermods-api/reforgermods-index.db"
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
	store := &IndexStore{db: db, now: time.Now, dbPath: path}
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

func (s *IndexStore) configure() error {
	if _, err := s.db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		return err
	}
	_, err := s.db.Exec(`PRAGMA synchronous=NORMAL;`)
	return err
}

func (s *IndexStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *IndexStore) Migrate(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS cache_entries (
			cache_key TEXT PRIMARY KEY,
			resource_type TEXT,
			endpoint_group TEXT,
			status_code INTEGER NOT NULL,
			headers_json TEXT,
			body_json BLOB NOT NULL,
			fresh_until TIMESTAMP NOT NULL,
			stale_until TIMESTAMP NOT NULL,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			last_accessed_at TIMESTAMP,
			access_count INTEGER NOT NULL DEFAULT 0,
			last_refresh_status TEXT,
			last_refresh_error TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_cache_entries_hot ON cache_entries(access_count DESC, last_accessed_at DESC);`,
		`CREATE TABLE IF NOT EXISTS mods (
			id TEXT PRIMARY KEY,
			name TEXT,
			author TEXT,
			summary TEXT,
			version TEXT,
			size TEXT,
			downloads INTEGER NOT NULL DEFAULT 0,
			subscribers INTEGER NOT NULL DEFAULT 0,
			last_modified TEXT,
			official_url TEXT,
			api_url TEXT,
			image_url TEXT,
			tags_json TEXT,
			first_seen_at TIMESTAMP NOT NULL,
			last_seen_at TIMESTAMP NOT NULL,
			last_refreshed_at TIMESTAMP,
			detail_refreshed_at TIMESTAMP,
			refresh_status TEXT,
			refresh_error TEXT,
			raw_json TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_mods_seen ON mods(last_seen_at DESC);`,
		`CREATE TABLE IF NOT EXISTS mod_details (
			mod_id TEXT PRIMARY KEY,
			body_json BLOB NOT NULL,
			dependencies_json TEXT,
			scenarios_json TEXT,
			fresh_until TIMESTAMP NOT NULL,
			stale_until TIMESTAMP NOT NULL,
			last_refreshed_at TIMESTAMP NOT NULL,
			last_accessed_at TIMESTAMP,
			refresh_status TEXT,
			refresh_error TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_mod_details_fresh ON mod_details(fresh_until, stale_until);`,
		`CREATE TABLE IF NOT EXISTS list_pages (
			cache_key TEXT PRIMARY KEY,
			page INTEGER NOT NULL,
			sort TEXT,
			search TEXT,
			resource_type TEXT,
			mod_ids_json TEXT,
			body_json BLOB NOT NULL,
			fresh_until TIMESTAMP NOT NULL,
			stale_until TIMESTAMP NOT NULL,
			last_refreshed_at TIMESTAMP NOT NULL,
			last_accessed_at TIMESTAMP,
			refresh_status TEXT,
			refresh_error TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_list_pages_lookup ON list_pages(page, sort, search);`,
		`CREATE TABLE IF NOT EXISTS refresh_queue_history (
			job_id TEXT,
			resource_key TEXT,
			resource_type TEXT,
			priority TEXT,
			status TEXT,
			queued_at TIMESTAMP,
			started_at TIMESTAMP,
			finished_at TIMESTAMP,
			duration_ms INTEGER,
			error_reason TEXT
		);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, `CREATE VIRTUAL TABLE IF NOT EXISTS mods_fts USING fts5(id UNINDEXED, name, author, tags, summary);`); err == nil {
		s.ftsOK = true
	}
	return nil
}

func (s *IndexStore) PutCacheEntry(ctx context.Context, entry PersistentCacheEntry) error {
	if s == nil || s.db == nil {
		return nil
	}
	now := s.now().UTC()
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = now
	}
	entry.UpdatedAt = now
	if entry.LastAccessedAt.IsZero() {
		entry.LastAccessedAt = now
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO cache_entries (
		cache_key, resource_type, endpoint_group, status_code, headers_json, body_json,
		fresh_until, stale_until, created_at, updated_at, last_accessed_at, access_count,
		last_refresh_status, last_refresh_error
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(cache_key) DO UPDATE SET
		resource_type=excluded.resource_type,
		endpoint_group=excluded.endpoint_group,
		status_code=excluded.status_code,
		headers_json=excluded.headers_json,
		body_json=excluded.body_json,
		fresh_until=excluded.fresh_until,
		stale_until=excluded.stale_until,
		updated_at=excluded.updated_at,
		last_accessed_at=excluded.last_accessed_at,
		access_count=cache_entries.access_count,
		last_refresh_status=excluded.last_refresh_status,
		last_refresh_error=excluded.last_refresh_error`,
		entry.CacheKey, entry.ResourceType, entry.EndpointGroup, entry.StatusCode, entry.HeadersJSON, entry.Body,
		entry.FreshUntil.UTC(), entry.StaleUntil.UTC(), entry.CreatedAt.UTC(), entry.UpdatedAt.UTC(), entry.LastAccessedAt.UTC(), entry.AccessCount,
		entry.LastRefreshStatus, entry.LastRefreshError,
	)
	return err
}

func (s *IndexStore) GetCacheEntry(ctx context.Context, key string) (PersistentCacheEntry, bool, error) {
	var entry PersistentCacheEntry
	if s == nil || s.db == nil {
		return entry, false, nil
	}
	row := s.db.QueryRowContext(ctx, `SELECT cache_key, resource_type, endpoint_group, status_code, headers_json, body_json,
		fresh_until, stale_until, created_at, updated_at, COALESCE(last_accessed_at, created_at), access_count,
		COALESCE(last_refresh_status, ''), COALESCE(last_refresh_error, '')
		FROM cache_entries WHERE cache_key = ?`, key)
	var freshUntil, staleUntil, createdAt, updatedAt, lastAccessedAt sqliteTime
	err := row.Scan(&entry.CacheKey, &entry.ResourceType, &entry.EndpointGroup, &entry.StatusCode, &entry.HeadersJSON, &entry.Body,
		&freshUntil, &staleUntil, &createdAt, &updatedAt, &lastAccessedAt, &entry.AccessCount,
		&entry.LastRefreshStatus, &entry.LastRefreshError)
	if errors.Is(err, sql.ErrNoRows) {
		return entry, false, nil
	}
	if err != nil {
		return entry, false, err
	}
	entry.FreshUntil = freshUntil.Time
	entry.StaleUntil = staleUntil.Time
	entry.CreatedAt = createdAt.Time
	entry.UpdatedAt = updatedAt.Time
	entry.LastAccessedAt = lastAccessedAt.Time
	_, _ = s.db.ExecContext(ctx, `UPDATE cache_entries SET last_accessed_at = ?, access_count = access_count + 1 WHERE cache_key = ?`, s.now().UTC(), key)
	return entry, true, nil
}

func (s *IndexStore) HotCacheEntries(ctx context.Context, limit int) ([]PersistentCacheEntry, error) {
	if s == nil || s.db == nil || limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT cache_key, resource_type, endpoint_group, status_code, headers_json, body_json,
		fresh_until, stale_until, created_at, updated_at, COALESCE(last_accessed_at, created_at), access_count,
		COALESCE(last_refresh_status, ''), COALESCE(last_refresh_error, '')
		FROM cache_entries WHERE stale_until > ? ORDER BY access_count DESC, last_accessed_at DESC LIMIT ?`, s.now().UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PersistentCacheEntry
	for rows.Next() {
		var entry PersistentCacheEntry
		var freshUntil, staleUntil, createdAt, updatedAt, lastAccessedAt sqliteTime
		if err := rows.Scan(&entry.CacheKey, &entry.ResourceType, &entry.EndpointGroup, &entry.StatusCode, &entry.HeadersJSON, &entry.Body,
			&freshUntil, &staleUntil, &createdAt, &updatedAt, &lastAccessedAt, &entry.AccessCount,
			&entry.LastRefreshStatus, &entry.LastRefreshError); err != nil {
			return nil, err
		}
		entry.FreshUntil = freshUntil.Time
		entry.StaleUntil = staleUntil.Time
		entry.CreatedAt = createdAt.Time
		entry.UpdatedAt = updatedAt.Time
		entry.LastAccessedAt = lastAccessedAt.Time
		out = append(out, entry)
	}
	return out, rows.Err()
}

func (s *IndexStore) PutListPage(ctx context.Context, page ListPageRecord) error {
	if s == nil || s.db == nil {
		return nil
	}
	now := s.now().UTC()
	if page.LastRefreshedAt.IsZero() {
		page.LastRefreshedAt = now
	}
	if page.LastAccessedAt.IsZero() {
		page.LastAccessedAt = now
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO list_pages (
		cache_key, page, sort, search, resource_type, mod_ids_json, body_json,
		fresh_until, stale_until, last_refreshed_at, last_accessed_at, refresh_status, refresh_error
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(cache_key) DO UPDATE SET
		page=excluded.page, sort=excluded.sort, search=excluded.search, resource_type=excluded.resource_type,
		mod_ids_json=excluded.mod_ids_json, body_json=excluded.body_json, fresh_until=excluded.fresh_until,
		stale_until=excluded.stale_until, last_refreshed_at=excluded.last_refreshed_at,
		last_accessed_at=excluded.last_accessed_at, refresh_status=excluded.refresh_status,
		refresh_error=excluded.refresh_error`,
		page.CacheKey, page.Page, page.Sort, page.Search, page.ResourceType, page.ModIDsJSON, page.Body,
		page.FreshUntil.UTC(), page.StaleUntil.UTC(), page.LastRefreshedAt.UTC(), page.LastAccessedAt.UTC(),
		page.RefreshStatus, page.RefreshError)
	return err
}

func (s *IndexStore) GetListPage(ctx context.Context, key string) (ListPageRecord, bool, error) {
	var page ListPageRecord
	if s == nil || s.db == nil {
		return page, false, nil
	}
	row := s.db.QueryRowContext(ctx, `SELECT cache_key, page, sort, search, resource_type, mod_ids_json, body_json,
		fresh_until, stale_until, last_refreshed_at, COALESCE(last_accessed_at, last_refreshed_at),
		COALESCE(refresh_status, ''), COALESCE(refresh_error, '') FROM list_pages WHERE cache_key = ?`, key)
	var freshUntil, staleUntil, lastRefreshedAt, lastAccessedAt sqliteTime
	err := row.Scan(&page.CacheKey, &page.Page, &page.Sort, &page.Search, &page.ResourceType, &page.ModIDsJSON, &page.Body,
		&freshUntil, &staleUntil, &lastRefreshedAt, &lastAccessedAt, &page.RefreshStatus, &page.RefreshError)
	if errors.Is(err, sql.ErrNoRows) {
		return page, false, nil
	}
	if err != nil {
		return page, false, err
	}
	page.FreshUntil = freshUntil.Time
	page.StaleUntil = staleUntil.Time
	page.LastRefreshedAt = lastRefreshedAt.Time
	page.LastAccessedAt = lastAccessedAt.Time
	_, _ = s.db.ExecContext(ctx, `UPDATE list_pages SET last_accessed_at = ? WHERE cache_key = ?`, s.now().UTC(), key)
	return page, true, nil
}

func (s *IndexStore) UpsertModPreview(ctx context.Context, mod models.ModPreview) error {
	if strings.TrimSpace(mod.ID) == "" {
		return nil
	}
	raw, _ := json.Marshal(mod)
	now := s.now().UTC()
	record := ModIndexRecord{
		ID:              strings.TrimSpace(mod.ID),
		Name:            mod.Name,
		Author:          mod.Author,
		Size:            mod.Size,
		OfficialURL:     mod.OriginalModURL,
		APIURL:          mod.APIModURL,
		ImageURL:        mod.ImageURL,
		FirstSeenAt:     now,
		LastSeenAt:      now,
		LastRefreshedAt: now,
		RefreshStatus:   string(RefreshJobSucceeded),
		RawJSON:         string(raw),
	}
	return s.UpsertMod(ctx, record)
}

func (s *IndexStore) UpsertModDetail(ctx context.Context, mod models.Mod) error {
	if strings.TrimSpace(mod.ID) == "" {
		return nil
	}
	tags, _ := json.Marshal(mod.Tags)
	raw, _ := json.Marshal(mod)
	now := s.now().UTC()
	record := ModIndexRecord{
		ID:                strings.TrimSpace(mod.ID),
		Name:              mod.Name,
		Author:            mod.Author,
		Summary:           mod.Summary,
		Version:           mod.Version,
		Size:              mod.Size,
		Downloads:         mod.Downloads,
		Subscribers:       mod.Subscribers,
		LastModified:      mod.LastModified,
		OfficialURL:       mod.OriginalModURL,
		APIURL:            mod.APIModURL,
		ImageURL:          mod.ImageURL,
		TagsJSON:          string(tags),
		FirstSeenAt:       now,
		LastSeenAt:        now,
		LastRefreshedAt:   now,
		DetailRefreshedAt: now,
		RefreshStatus:     string(RefreshJobSucceeded),
		RawJSON:           string(raw),
	}
	return s.UpsertMod(ctx, record)
}

func (s *IndexStore) UpsertMod(ctx context.Context, mod ModIndexRecord) error {
	if s == nil || s.db == nil || strings.TrimSpace(mod.ID) == "" {
		return nil
	}
	now := s.now().UTC()
	if mod.FirstSeenAt.IsZero() {
		mod.FirstSeenAt = now
	}
	if mod.LastSeenAt.IsZero() {
		mod.LastSeenAt = now
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO mods (
		id, name, author, summary, version, size, downloads, subscribers, last_modified,
		official_url, api_url, image_url, tags_json, first_seen_at, last_seen_at,
		last_refreshed_at, detail_refreshed_at, refresh_status, refresh_error, raw_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		name=COALESCE(NULLIF(excluded.name, ''), mods.name),
		author=COALESCE(NULLIF(excluded.author, ''), mods.author),
		summary=COALESCE(NULLIF(excluded.summary, ''), mods.summary),
		version=COALESCE(NULLIF(excluded.version, ''), mods.version),
		size=COALESCE(NULLIF(excluded.size, ''), mods.size),
		downloads=CASE WHEN excluded.downloads > 0 THEN excluded.downloads ELSE mods.downloads END,
		subscribers=CASE WHEN excluded.subscribers > 0 THEN excluded.subscribers ELSE mods.subscribers END,
		last_modified=COALESCE(NULLIF(excluded.last_modified, ''), mods.last_modified),
		official_url=COALESCE(NULLIF(excluded.official_url, ''), mods.official_url),
		api_url=COALESCE(NULLIF(excluded.api_url, ''), mods.api_url),
		image_url=COALESCE(NULLIF(excluded.image_url, ''), mods.image_url),
		tags_json=COALESCE(NULLIF(excluded.tags_json, ''), mods.tags_json),
		last_seen_at=excluded.last_seen_at,
		last_refreshed_at=COALESCE(excluded.last_refreshed_at, mods.last_refreshed_at),
		detail_refreshed_at=COALESCE(excluded.detail_refreshed_at, mods.detail_refreshed_at),
		refresh_status=excluded.refresh_status,
		refresh_error=excluded.refresh_error,
		raw_json=COALESCE(NULLIF(excluded.raw_json, ''), mods.raw_json)`,
		mod.ID, mod.Name, mod.Author, mod.Summary, mod.Version, mod.Size, mod.Downloads, mod.Subscribers, mod.LastModified,
		mod.OfficialURL, mod.APIURL, mod.ImageURL, mod.TagsJSON, mod.FirstSeenAt.UTC(), mod.LastSeenAt.UTC(),
		zeroTimeNil(mod.LastRefreshedAt), zeroTimeNil(mod.DetailRefreshedAt), mod.RefreshStatus, mod.RefreshError, mod.RawJSON)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if s.ftsOK {
		tagsText := tagsForSearch(mod.TagsJSON)
		if _, err = tx.ExecContext(ctx, `INSERT INTO mods_fts(rowid, id, name, author, tags, summary)
			VALUES ((SELECT rowid FROM mods WHERE id = ?), ?, ?, ?, ?, ?)
			ON CONFLICT(rowid) DO UPDATE SET id=excluded.id, name=excluded.name, author=excluded.author, tags=excluded.tags, summary=excluded.summary`,
			mod.ID, mod.ID, mod.Name, mod.Author, tagsText, mod.Summary); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *IndexStore) PutModDetail(ctx context.Context, detail ModDetailRecord) error {
	if s == nil || s.db == nil || strings.TrimSpace(detail.ModID) == "" {
		return nil
	}
	now := s.now().UTC()
	if detail.LastRefreshedAt.IsZero() {
		detail.LastRefreshedAt = now
	}
	if detail.LastAccessedAt.IsZero() {
		detail.LastAccessedAt = now
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO mod_details (
		mod_id, body_json, dependencies_json, scenarios_json, fresh_until, stale_until,
		last_refreshed_at, last_accessed_at, refresh_status, refresh_error
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(mod_id) DO UPDATE SET
		body_json=excluded.body_json,
		dependencies_json=excluded.dependencies_json,
		scenarios_json=excluded.scenarios_json,
		fresh_until=excluded.fresh_until,
		stale_until=excluded.stale_until,
		last_refreshed_at=excluded.last_refreshed_at,
		last_accessed_at=excluded.last_accessed_at,
		refresh_status=excluded.refresh_status,
		refresh_error=excluded.refresh_error`,
		strings.TrimSpace(detail.ModID), detail.Body, detail.DependenciesJSON, detail.ScenariosJSON,
		detail.FreshUntil.UTC(), detail.StaleUntil.UTC(), detail.LastRefreshedAt.UTC(), detail.LastAccessedAt.UTC(),
		detail.RefreshStatus, detail.RefreshError)
	return err
}

func (s *IndexStore) GetModDetail(ctx context.Context, modID string) (ModDetailRecord, bool, error) {
	var detail ModDetailRecord
	if s == nil || s.db == nil {
		return detail, false, nil
	}
	row := s.db.QueryRowContext(ctx, `SELECT mod_id, body_json, COALESCE(dependencies_json, ''), COALESCE(scenarios_json, ''),
		fresh_until, stale_until, last_refreshed_at, COALESCE(last_accessed_at, last_refreshed_at),
		COALESCE(refresh_status, ''), COALESCE(refresh_error, '') FROM mod_details WHERE lower(mod_id) = ?`, CanonicalModID(modID))
	var freshUntil, staleUntil, lastRefreshedAt, lastAccessedAt sqliteTime
	err := row.Scan(&detail.ModID, &detail.Body, &detail.DependenciesJSON, &detail.ScenariosJSON,
		&freshUntil, &staleUntil, &lastRefreshedAt, &lastAccessedAt,
		&detail.RefreshStatus, &detail.RefreshError)
	if errors.Is(err, sql.ErrNoRows) {
		return detail, false, nil
	}
	if err != nil {
		return detail, false, err
	}
	detail.FreshUntil = freshUntil.Time
	detail.StaleUntil = staleUntil.Time
	detail.LastRefreshedAt = lastRefreshedAt.Time
	detail.LastAccessedAt = lastAccessedAt.Time
	_, _ = s.db.ExecContext(ctx, `UPDATE mod_details SET last_accessed_at = ? WHERE lower(mod_id) = ?`, s.now().UTC(), CanonicalModID(modID))
	return detail, true, nil
}

func (s *IndexStore) SearchMods(ctx context.Context, search string, limit int, offset int) ([]models.ModPreview, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 16
	}
	search = NormalizeSearch(search, 120)
	var rows *sql.Rows
	var err error
	if search == "" {
		rows, err = s.db.QueryContext(ctx, `SELECT id, name, author, image_url, official_url, api_url, size FROM mods ORDER BY last_seen_at DESC LIMIT ? OFFSET ?`, limit, offset)
	} else if s.ftsOK {
		rows, err = s.db.QueryContext(ctx, `SELECT m.id, m.name, m.author, m.image_url, m.official_url, m.api_url, m.size
			FROM mods_fts f JOIN mods m ON m.id = f.id
			WHERE mods_fts MATCH ? ORDER BY rank LIMIT ? OFFSET ?`, ftsQuery(search), limit, offset)
	} else {
		like := "%" + strings.ToLower(search) + "%"
		rows, err = s.db.QueryContext(ctx, `SELECT id, name, author, image_url, official_url, api_url, size
			FROM mods WHERE lower(name) LIKE ? OR lower(author) LIKE ? OR lower(summary) LIKE ? OR lower(tags_json) LIKE ?
			ORDER BY last_seen_at DESC LIMIT ? OFFSET ?`, like, like, like, like, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.ModPreview
	for rows.Next() {
		var mod models.ModPreview
		if err := rows.Scan(&mod.ID, &mod.Name, &mod.Author, &mod.ImageURL, &mod.OriginalModURL, &mod.APIModURL, &mod.Size); err != nil {
			return nil, err
		}
		out = append(out, mod)
	}
	return out, rows.Err()
}

func CacheEntryFromResponse(key string, resp CachedResponse, createdAt time.Time) PersistentCacheEntry {
	status := resp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	fresh := createdAt.Add(resp.TTL)
	stale := fresh.Add(resp.Stale)
	return PersistentCacheEntry{
		CacheKey:          key,
		ResourceType:      ResourceTypeForCacheKey(key),
		EndpointGroup:     EndpointGroupForCacheKey(key),
		StatusCode:        status,
		HeadersJSON:       `{}`,
		Body:              resp.Body,
		FreshUntil:        fresh,
		StaleUntil:        stale,
		CreatedAt:         createdAt,
		UpdatedAt:         createdAt,
		LastAccessedAt:    createdAt,
		LastRefreshStatus: string(RefreshJobSucceeded),
	}
}

func ResourceTypeForCacheKey(key string) string {
	parts := strings.Split(strings.ToLower(key), ":")
	if len(parts) >= 2 {
		return parts[1]
	}
	return "unknown"
}

func zeroTimeNil(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

func tagsForSearch(raw string) string {
	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err == nil {
		return strings.Join(tags, " ")
	}
	return raw
}

func ftsQuery(search string) string {
	terms := strings.Fields(search)
	for i, term := range terms {
		terms[i] = strings.ReplaceAll(term, `"`, `""`) + "*"
	}
	return strings.Join(terms, " ")
}

func (s *IndexStore) Check() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("index store is not open")
	}
	return s.db.Ping()
}
