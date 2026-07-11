package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
	"go.uber.org/zap"
)

type CacheFetchFunc func(context.Context) CachedResponse
type LocalFallbackFunc func(context.Context) (CachedResponse, bool)

type CachedResponse struct {
	StatusCode     int
	Body           []byte
	TTL            time.Duration
	Stale          time.Duration
	Err            error
	ErrorCode      string
	Message        string
	PanicRecovered bool
}

type ResponseCache struct {
	cfg     config.Config
	metrics *Metrics
	store   *IndexStore
	mu      sync.Mutex
	entries map[string]*cacheEntry
	order   []string
	now     func() time.Time
	refresh *refreshManager
}

type cacheEntry struct {
	response        CachedResponse
	createdAt       time.Time
	expiresAt       time.Time
	staleAt         time.Time
	etag            string
	refreshStatus   RefreshJobStatus
	refreshJobID    string
	refreshFailedAt *time.Time
}

type CacheSnapshot struct {
	Entries       int                    `json:"entries"`
	MaxEntries    int                    `json:"maxEntries"`
	LatestEntries []CacheInfo            `json:"latestEntries"`
	Refresh       RefreshManagerSnapshot `json:"refresh"`
}

type CacheInfo struct {
	Key           string    `json:"key"`
	StatusCode    int       `json:"statusCode"`
	BodyBytes     int       `json:"bodyBytes"`
	CreatedAt     time.Time `json:"createdAt"`
	ExpiresAt     time.Time `json:"expiresAt"`
	StaleAt       time.Time `json:"staleAt"`
	AgeSeconds    int       `json:"ageSeconds"`
	FreshSeconds  int       `json:"freshSeconds"`
	StaleSeconds  int       `json:"staleSeconds"`
	CurrentStatus string    `json:"currentStatus"`
	RefreshStatus string    `json:"refreshStatus,omitempty"`
	RefreshJobID  string    `json:"refreshJobId,omitempty"`
}

func NewResponseCache(cfg config.Config, metrics ...*Metrics) *ResponseCache {
	parallel := cfg.CacheRefreshParallel
	if parallel <= 0 {
		parallel = 1
	}
	var collector *Metrics
	if len(metrics) > 0 {
		collector = metrics[0]
	}
	cache := &ResponseCache{
		cfg:     cfg,
		metrics: collector,
		entries: make(map[string]*cacheEntry),
		now:     time.Now,
	}
	cache.refresh = newRefreshManager(
		parallel,
		cfg.CacheRefreshQueueSize,
		cfg.CacheRefreshTimeout,
		cfg.CacheRefreshJobRetention,
		cfg.CacheRefreshRetryAfter,
		collector,
		cache.now,
		cache.finishRefresh,
	)
	return cache
}

func (c *ResponseCache) SetIndexStore(store *IndexStore) {
	c.store = store
}

func (c *ResponseCache) PreloadHotEntries(ctx context.Context, limit int) error {
	if c == nil || c.store == nil || limit <= 0 {
		return nil
	}
	entries, err := c.store.HotCacheEntries(ctx, limit)
	if err != nil {
		if c.metrics != nil {
			c.metrics.RecordIndexEvent("database_error", 0)
		}
		return err
	}
	now := c.now()
	for _, persisted := range entries {
		entry := cacheEntryFromPersistent(persisted)
		if now.After(entry.staleAt) {
			continue
		}
		c.mu.Lock()
		c.entries[persisted.CacheKey] = entry
		c.touchLocked(persisted.CacheKey)
		c.evictLocked()
		c.mu.Unlock()
	}
	return nil
}

func (c *ResponseCache) Serve(w http.ResponseWriter, r *http.Request, key string, ttl time.Duration, stale time.Duration, fetch CacheFetchFunc) {
	c.ServeWithFallback(w, r, key, ttl, stale, fetch, nil)
}

func (c *ResponseCache) ServeWithFallback(w http.ResponseWriter, r *http.Request, key string, ttl time.Duration, stale time.Duration, fetch CacheFetchFunc, fallback LocalFallbackFunc) {
	now := c.now()
	if entry, status := c.lookup(key, now); entry != nil {
		refreshStatus := RefreshJobStatus("none")
		if status == "STALE" {
			job, _, err := c.enqueueRefresh(r, key, ttl, stale, fetch)
			switch {
			case err == nil:
				refreshStatus = job.Status
				c.markRefreshQueued(key, job)
				if updated, _ := c.lookup(key, c.now()); updated != nil {
					entry = updated
				}
			case errors.Is(err, ErrRefreshQueueFull), errors.Is(err, ErrRefreshShutdown):
				refreshStatus = RefreshJobFailed
			default:
				refreshStatus = RefreshJobFailed
			}
		} else if entry.refreshStatus == RefreshJobFailed {
			refreshStatus = RefreshJobFailed
		}
		c.write(w, r, key, entry, status, refreshStatus)
		zap.S().Infow("cache served", "requestId", r.Header.Get("X-Request-Id"), "key", key, "status", status, "refreshStatus", refreshStatus)
		return
	}

	if entry, status := c.lookupPersistent(r.Context(), key, now); entry != nil {
		refreshStatus := RefreshJobStatus("none")
		if status == "STALE" {
			job, _, err := c.enqueueRefreshWithPriority(r, key, ttl, stale, fetch, RefreshPriorityNormal)
			if err == nil {
				refreshStatus = job.Status
				c.markRefreshQueued(key, job)
				if updated, _ := c.lookup(key, c.now()); updated != nil {
					entry = updated
				}
			} else {
				refreshStatus = RefreshJobFailed
			}
		}
		c.write(w, r, key, entry, status, refreshStatus)
		zap.S().Infow("persistent cache served", "requestId", r.Header.Get("X-Request-Id"), "key", key, "status", status, "refreshStatus", refreshStatus)
		return
	}

	if fallback != nil {
		if resp, ok := fallback(r.Context()); ok {
			if resp.TTL == 0 {
				resp.TTL = ttl
			}
			if resp.Stale == 0 {
				resp.Stale = stale
			}
			c.storeResponse(key, resp, RefreshJobSucceeded, "local-index")
			if entry, status := c.lookup(key, c.now()); entry != nil {
				_, _, _ = c.enqueueRefreshWithPriority(r, key, ttl, stale, fetch, RefreshPriorityLow)
				c.write(w, r, key, entry, status, RefreshJobQueued)
				return
			}
		}
	}

	job, _, err := c.enqueueRefresh(r, key, ttl, stale, fetch)
	if err != nil {
		c.writeRefreshSaturated(w, r, err)
		return
	}
	c.writeAccepted(w, r, key, job)
	zap.S().Infow("cache miss accepted for background refresh", "requestId", r.Header.Get("X-Request-Id"), "key", key, "jobId", job.ID, "refreshStatus", job.Status)
}

func (c *ResponseCache) lookup(key string, now time.Time) (*cacheEntry, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[key]
	if entry == nil {
		return nil, "MISS"
	}
	if now.Before(entry.expiresAt) {
		entryCopy := *entry
		return &entryCopy, "HIT"
	}
	if now.Before(entry.staleAt) {
		entryCopy := *entry
		return &entryCopy, "STALE"
	}
	delete(c.entries, key)
	return nil, "MISS"
}

func (c *ResponseCache) enqueueRefresh(r *http.Request, key string, ttl time.Duration, stale time.Duration, fetch CacheFetchFunc) (RefreshJobSnapshot, bool, error) {
	return c.enqueueRefreshWithPriority(r, key, ttl, stale, fetch, RefreshPriorityHigh)
}

func (c *ResponseCache) enqueueRefreshWithPriority(r *http.Request, key string, ttl time.Duration, stale time.Duration, fetch CacheFetchFunc, priority RefreshPriority) (RefreshJobSnapshot, bool, error) {
	return c.refresh.Enqueue(RefreshRequest{
		ResourceKey: key,
		ResourceURL: resourceURL(r),
		TTL:         ttl,
		Stale:       stale,
		Fetch:       RefreshFetchFunc(fetch),
		RequestID:   r.Header.Get("X-Request-Id"),
		Priority:    priority,
	})
}

func (c *ResponseCache) EnqueueRefresh(key string, resourceURL string, ttl time.Duration, stale time.Duration, priority RefreshPriority, fetch CacheFetchFunc) (RefreshJobSnapshot, bool, error) {
	if c == nil || c.refresh == nil {
		return RefreshJobSnapshot{}, false, ErrRefreshShutdown
	}
	return c.refresh.Enqueue(RefreshRequest{
		ResourceKey: key,
		ResourceURL: resourceURL,
		TTL:         ttl,
		Stale:       stale,
		Fetch:       RefreshFetchFunc(fetch),
		Priority:    priority,
	})
}

func (c *ResponseCache) finishRefresh(job *refreshJob, resp CachedResponse, duration time.Duration) {
	if resp.TTL == 0 {
		resp.TTL = job.ttl
	}
	if resp.Stale == 0 {
		resp.Stale = job.stale
	}
	if resp.Err != nil {
		c.markRefreshFailed(job)
		reason := ClassifyScrapeError(resp.Err, resp.StatusCode)
		if resp.PanicRecovered {
			reason = "panic_recovered"
		}
		c.metrics.RecordScrapeResult(job.resourceKey, resp.StatusCode, duration, resp.Err, reason)
		c.metrics.RecordRefreshCompletion(job.resourceKey, RefreshJobFailed, duration, reason, resp.PanicRecovered)
		zap.S().Warnw("cache refresh failed", "requestId", job.requestID, "jobId", job.id, "key", job.resourceKey, "durationMs", duration.Milliseconds())
		return
	}
	c.storeResponse(job.resourceKey, resp, RefreshJobSucceeded, job.id)
	c.metrics.RecordScrapeResult(job.resourceKey, resp.StatusCode, duration, nil, "")
	c.metrics.RecordRefreshCompletion(job.resourceKey, RefreshJobSucceeded, duration, "", false)
}

func (c *ResponseCache) storeResponse(key string, resp CachedResponse, refreshStatus RefreshJobStatus, refreshJobID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if resp.StatusCode == 0 {
		resp.StatusCode = http.StatusOK
	}
	entry := &cacheEntry{
		response:      resp,
		createdAt:     now,
		expiresAt:     now.Add(resp.TTL),
		staleAt:       now.Add(resp.TTL + resp.Stale),
		etag:          weakETag(resp.Body),
		refreshStatus: refreshStatus,
		refreshJobID:  refreshJobID,
	}
	c.entries[key] = entry
	c.touchLocked(key)
	c.evictLocked()
	if c.store != nil && refreshStatus == RefreshJobSucceeded {
		persisted := CacheEntryFromResponse(key, resp, now.UTC())
		persisted.LastRefreshStatus = string(refreshStatus)
		go func() {
			if err := c.store.PutCacheEntry(context.Background(), persisted); err != nil {
				if c.metrics != nil {
					c.metrics.RecordIndexEvent("database_error", 0)
				}
				zap.S().Warnw("failed to persist cache entry", "key", key, "error", err)
			}
		}()
	}
}

func (c *ResponseCache) StoreForTest(key string, resp CachedResponse, refreshStatus RefreshJobStatus, refreshJobID string) {
	c.storeResponse(key, resp, refreshStatus, refreshJobID)
}

func (c *ResponseCache) lookupPersistent(ctx context.Context, key string, now time.Time) (*cacheEntry, string) {
	if c.store == nil {
		return nil, "MISS"
	}
	persisted, ok, err := c.store.GetCacheEntry(ctx, key)
	if err != nil {
		if c.metrics != nil {
			c.metrics.RecordPersistentCache("ERROR")
			c.metrics.RecordIndexEvent("database_error", 0)
		}
		zap.S().Warnw("persistent cache lookup failed", "key", key, "error", err)
		return nil, "MISS"
	}
	if !ok {
		if c.metrics != nil {
			c.metrics.RecordPersistentCache("MISS")
		}
		return nil, "MISS"
	}
	entry := cacheEntryFromPersistent(persisted)
	if now.Before(entry.expiresAt) {
		c.promotePersistent(key, entry)
		if c.metrics != nil {
			c.metrics.RecordPersistentCache("HIT")
		}
		return cloneCacheEntry(entry), "HIT"
	}
	if now.Before(entry.staleAt) {
		c.promotePersistent(key, entry)
		if c.metrics != nil {
			c.metrics.RecordPersistentCache("STALE")
		}
		return cloneCacheEntry(entry), "STALE"
	}
	if c.metrics != nil {
		c.metrics.RecordPersistentCache("MISS")
	}
	return nil, "MISS"
}

func (c *ResponseCache) promotePersistent(key string, entry *cacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = entry
	c.touchLocked(key)
	c.evictLocked()
}

func (c *ResponseCache) markRefreshFailed(job *refreshJob) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[job.resourceKey]
	if entry == nil {
		return
	}
	now := c.now().UTC()
	entry.refreshStatus = RefreshJobFailed
	entry.refreshJobID = job.id
	entry.refreshFailedAt = &now
}

func (c *ResponseCache) markRefreshQueued(key string, job RefreshJobSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[key]
	if entry == nil {
		return
	}
	entry.refreshStatus = job.Status
	entry.refreshJobID = job.ID
}

func (c *ResponseCache) write(w http.ResponseWriter, r *http.Request, key string, entry *cacheEntry, cacheStatus string, refreshStatus RefreshJobStatus) {
	now := c.now()
	if match := r.Header.Get("If-None-Match"); match != "" && match == entry.etag {
		c.setCacheHeaders(w, entry, cacheStatus, refreshStatus, now)
		w.WriteHeader(http.StatusNotModified)
		c.metrics.RecordCache(key, cacheStatus, http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	c.setCacheHeaders(w, entry, cacheStatus, refreshStatus, now)
	w.WriteHeader(entry.response.StatusCode)
	_, _ = w.Write(entry.response.Body)
	c.metrics.RecordCache(key, cacheStatus, entry.response.StatusCode)
}

func (c *ResponseCache) setCacheHeaders(w http.ResponseWriter, entry *cacheEntry, cacheStatus string, refreshStatus RefreshJobStatus, now time.Time) {
	age := int(now.Sub(entry.createdAt).Seconds())
	if age < 0 {
		age = 0
	}
	freshSeconds := int(entry.expiresAt.Sub(now).Seconds())
	if freshSeconds < 0 {
		freshSeconds = 0
	}
	staleSeconds := int(entry.staleAt.Sub(now).Seconds())
	if staleSeconds < 0 {
		staleSeconds = 0
	}

	w.Header().Set("Age", strconv.Itoa(age))
	w.Header().Set("Cache-Control", cacheControl(entry, now))
	w.Header().Set("ETag", entry.etag)
	w.Header().Set("X-Cache", cacheStatus)
	w.Header().Set("X-Cache-Age", strconv.Itoa(age))
	w.Header().Set("X-Cache-Created-At", entry.createdAt.UTC().Format(time.RFC3339))
	w.Header().Set("X-Cache-Expires-At", entry.expiresAt.UTC().Format(time.RFC3339))
	w.Header().Set("X-Cache-Fresh-Seconds", strconv.Itoa(freshSeconds))
	w.Header().Set("X-Cache-Stale-At", entry.staleAt.UTC().Format(time.RFC3339))
	w.Header().Set("X-Cache-Stale-Seconds", strconv.Itoa(staleSeconds))
	w.Header().Set("X-Refresh-Status", string(refreshStatus))
	if entry.refreshJobID != "" {
		w.Header().Set("X-Refresh-Job-Id", entry.refreshJobID)
	}
	if entry.refreshFailedAt != nil {
		w.Header().Set("X-Refresh-Failed-At", entry.refreshFailedAt.UTC().Format(time.RFC3339))
	}
}

func (c *ResponseCache) writeAccepted(w http.ResponseWriter, r *http.Request, key string, job RefreshJobSnapshot) {
	location := jobLocation(job.ID)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", location)
	w.Header().Set("Retry-After", strconv.Itoa(job.RetryAfterSeconds))
	w.Header().Set("X-Cache", "MISS")
	w.Header().Set("X-Refresh-Status", string(job.Status))
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(job)
	c.metrics.RecordCache(key, "MISS", http.StatusAccepted)
}

func (c *ResponseCache) writeRefreshSaturated(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(c.cfg.CacheRefreshRetryAfter)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Cache", "MISS")
	w.Header().Set("X-Refresh-Status", "failed")
	status := http.StatusServiceUnavailable
	code := "REFRESH_QUEUE_FULL"
	message := "Refresh capacity is temporarily exhausted. Retry after the indicated delay."
	if errors.Is(err, ErrRefreshShutdown) {
		code = "REFRESH_SHUTTING_DOWN"
		message = "Refresh service is shutting down."
	}
	zap.S().Warnw("refresh request rejected", "requestId", r.Header.Get("X-Request-Id"), "path", r.URL.Path, "reason", code)
	config.WriteError(w, r, status, code, message)
	c.metrics.RecordCache(r.URL.Path, "MISS", status)
}

func (c *ResponseCache) touchLocked(key string) {
	for i, existing := range c.order {
		if existing == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	c.order = append(c.order, key)
}

func (c *ResponseCache) evictLocked() {
	limit := c.cfg.CacheMaxEntries
	if limit <= 0 {
		limit = 1000
	}
	for len(c.entries) > limit && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
}

func (c *ResponseCache) Snapshot(limit int) CacheSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	maxEntries := c.cfg.CacheMaxEntries
	if maxEntries <= 0 {
		maxEntries = 1000
	}
	if limit < 0 {
		limit = 0
	}
	now := c.now()
	out := CacheSnapshot{
		Entries:       len(c.entries),
		MaxEntries:    maxEntries,
		LatestEntries: make([]CacheInfo, 0, minInt(limit, len(c.entries))),
		Refresh:       c.refresh.Snapshot(),
	}
	if limit <= 0 {
		return out
	}
	for i := len(c.order) - 1; i >= 0 && len(out.LatestEntries) < limit; i-- {
		key := c.order[i]
		entry := c.entries[key]
		if entry == nil {
			continue
		}
		out.LatestEntries = append(out.LatestEntries, cacheInfo(key, entry, now))
	}
	return out
}

func (c *ResponseCache) RefreshJob(id string) (RefreshJobSnapshot, bool) {
	if c == nil || c.refresh == nil {
		return RefreshJobSnapshot{}, false
	}
	return c.refresh.Get(id)
}

func (c *ResponseCache) Shutdown(ctx context.Context) error {
	if c == nil || c.refresh == nil {
		return nil
	}
	return c.refresh.Shutdown(ctx)
}

func cacheInfo(key string, entry *cacheEntry, now time.Time) CacheInfo {
	age := int(now.Sub(entry.createdAt).Seconds())
	if age < 0 {
		age = 0
	}
	freshSeconds := int(entry.expiresAt.Sub(now).Seconds())
	if freshSeconds < 0 {
		freshSeconds = 0
	}
	staleSeconds := int(entry.staleAt.Sub(now).Seconds())
	if staleSeconds < 0 {
		staleSeconds = 0
	}
	status := "MISS"
	if now.Before(entry.expiresAt) {
		status = "HIT"
	} else if now.Before(entry.staleAt) {
		status = "STALE"
	}
	return CacheInfo{
		Key:           key,
		StatusCode:    entry.response.StatusCode,
		BodyBytes:     len(entry.response.Body),
		CreatedAt:     entry.createdAt.UTC(),
		ExpiresAt:     entry.expiresAt.UTC(),
		StaleAt:       entry.staleAt.UTC(),
		AgeSeconds:    age,
		FreshSeconds:  freshSeconds,
		StaleSeconds:  staleSeconds,
		CurrentStatus: status,
		RefreshStatus: string(entry.refreshStatus),
		RefreshJobID:  entry.refreshJobID,
	}
}

func cacheControl(entry *cacheEntry, now time.Time) string {
	maxAge := int(entry.expiresAt.Sub(now).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	stale := int(entry.staleAt.Sub(now).Seconds())
	if stale < 0 {
		stale = 0
	}
	staleWhileRevalidate := stale
	if now.After(entry.expiresAt) {
		staleWhileRevalidate = 0
	}
	return fmt.Sprintf("public, max-age=%d, stale-while-revalidate=%d, stale-if-error=%d", maxAge, staleWhileRevalidate, stale)
}

func resourceURL(r *http.Request) string {
	if r.URL == nil {
		return "/"
	}
	if r.URL.RawQuery == "" {
		return r.URL.Path
	}
	return r.URL.Path + "?" + r.URL.RawQuery
}

func jobLocation(id string) string {
	return "/v1/refresh/jobs/" + id
}

func weakETag(body []byte) string {
	sum := sha256.Sum256(body)
	return `W/"` + hex.EncodeToString(sum[:12]) + `"`
}

func cacheEntryFromPersistent(persisted PersistentCacheEntry) *cacheEntry {
	resp := CachedResponse{
		StatusCode: persisted.StatusCode,
		Body:       append([]byte(nil), persisted.Body...),
		TTL:        persisted.FreshUntil.Sub(persisted.CreatedAt),
		Stale:      persisted.StaleUntil.Sub(persisted.FreshUntil),
	}
	if resp.StatusCode == 0 {
		resp.StatusCode = http.StatusOK
	}
	status := RefreshJobStatus(persisted.LastRefreshStatus)
	if status == "" {
		status = RefreshJobSucceeded
	}
	return &cacheEntry{
		response:      resp,
		createdAt:     persisted.CreatedAt,
		expiresAt:     persisted.FreshUntil,
		staleAt:       persisted.StaleUntil,
		etag:          weakETag(persisted.Body),
		refreshStatus: status,
	}
}

func cloneCacheEntry(entry *cacheEntry) *cacheEntry {
	if entry == nil {
		return nil
	}
	copy := *entry
	copy.response.Body = append([]byte(nil), entry.response.Body...)
	return &copy
}

func CacheKey(parts ...string) string {
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		cleaned = append(cleaned, strings.ToLower(strings.TrimSpace(part)))
	}
	return strings.Join(cleaned, ":")
}

func CanonicalModID(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func ModsCacheKey(page int, search string, sort string, tags []string) string {
	if page <= 1 {
		page = 1
	}
	search = strings.ToLower(NormalizeSearch(search, 120))
	sort = NormalizeSort(sort, map[string]bool{
		"popularity":   true,
		"newest":       true,
		"subscribers":  true,
		"version_size": true,
	})
	if sort == "" {
		sort = "popularity"
	}
	tags = NormalizeTags(tags, 40)
	return CacheKey("v1", "mods", strconv.Itoa(page), search, sort, strings.Join(tags, ","))
}

func ModCacheKey(modID string) string {
	return CacheKey("v1", "mod", CanonicalModID(modID))
}

type CacheTTLPolicy struct {
	Fresh time.Duration
	Stale time.Duration
}

func SelectCacheTTL(cfg config.Config, resourceType string, search string, statusCode int) CacheTTLPolicy {
	if statusCode == http.StatusNotFound {
		return CacheTTLPolicy{Fresh: cfg.NotFoundCacheTTL, Stale: 0}
	}
	switch resourceType {
	case "mod":
		fresh := cfg.ModDetailCacheTTL
		stale := cfg.ModDetailCacheStale
		if fresh <= 0 {
			fresh = cfg.ModCacheTTL
		}
		if stale <= 0 {
			stale = cfg.ModCacheStale
		}
		return CacheTTLPolicy{Fresh: fresh, Stale: stale}
	case "mods":
		if strings.TrimSpace(search) != "" {
			return CacheTTLPolicy{Fresh: cfg.SearchCacheTTL, Stale: cfg.SearchCacheStale}
		}
		return CacheTTLPolicy{Fresh: cfg.ListCacheTTL, Stale: cfg.ListCacheStale}
	default:
		return CacheTTLPolicy{Fresh: cfg.ListCacheTTL, Stale: cfg.ListCacheStale}
	}
}

func NormalizeSearch(raw string, maxLen int) string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	if maxLen > 0 && len(normalized) > maxLen {
		normalized = normalized[:maxLen]
	}
	return normalized
}

func NormalizeSort(raw string, allowed map[string]bool) string {
	sortValue := strings.ToLower(strings.TrimSpace(raw))
	if allowed[sortValue] {
		return sortValue
	}
	return ""
}

func NormalizeTags(tags []string, maxLen int) []string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = NormalizeSearch(tag, maxLen)
		if tag != "" {
			out = append(out, tag)
		}
	}
	sort.Strings(out)
	return out
}
