package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

type CachedResponse struct {
	StatusCode int
	Body       []byte
	TTL        time.Duration
	Stale      time.Duration
	Err        error
	ErrorCode  string
	Message    string
}

type ResponseCache struct {
	cfg      config.Config
	metrics  *Metrics
	mu       sync.Mutex
	entries  map[string]*cacheEntry
	inflight map[string]*inflightFetch
	order    []string
	sem      chan struct{}
	now      func() time.Time
}

type cacheEntry struct {
	response  CachedResponse
	createdAt time.Time
	expiresAt time.Time
	staleAt   time.Time
	etag      string
}

type inflightFetch struct {
	done chan struct{}
	resp CachedResponse
}

type CacheSnapshot struct {
	Entries       int         `json:"entries"`
	MaxEntries    int         `json:"maxEntries"`
	LatestEntries []CacheInfo `json:"latestEntries"`
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
	return &ResponseCache{
		cfg:      cfg,
		metrics:  collector,
		entries:  make(map[string]*cacheEntry),
		inflight: make(map[string]*inflightFetch),
		sem:      make(chan struct{}, parallel),
		now:      time.Now,
	}
}

func (c *ResponseCache) Serve(w http.ResponseWriter, r *http.Request, key string, ttl time.Duration, stale time.Duration, fetch CacheFetchFunc) {
	now := c.now()
	if entry, status := c.lookup(key, now); entry != nil {
		if status == "STALE" {
			c.refreshAsync(key, ttl, stale, fetch, r.Header.Get("X-Request-Id"))
		}
		c.write(w, r, key, entry, status)
		zap.S().Infow("cache served", "requestId", r.Header.Get("X-Request-Id"), "key", key, "status", status)
		return
	}

	call, owner := c.beginFetch(key)
	if !owner {
		select {
		case <-call.done:
			if entry, status := c.lookup(key, c.now()); entry != nil {
				c.write(w, r, key, entry, status)
				zap.S().Infow("cache served after wait", "requestId", r.Header.Get("X-Request-Id"), "key", key, "status", status)
				return
			}
			c.writeFetchError(w, r, call.resp)
			return
		case <-r.Context().Done():
			config.WriteError(w, r, http.StatusGatewayTimeout, "REQUEST_CANCELLED", "Request was cancelled before the upstream response was available.")
			return
		}
	}

	resp := c.fetchWithLimit(r.Context(), key, fetch)
	if resp.TTL == 0 {
		resp.TTL = ttl
	}
	if resp.Stale == 0 {
		resp.Stale = stale
	}
	c.finishFetch(key, call, resp)
	if resp.Err != nil {
		c.writeFetchError(w, r, resp)
		return
	}
	if entry, status := c.lookup(key, c.now()); entry != nil {
		if status == "HIT" {
			status = "MISS"
		}
		c.write(w, r, key, entry, status)
		return
	}
	config.WriteError(w, r, http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE", "Workshop data is temporarily unavailable.")
}

func (c *ResponseCache) lookup(key string, now time.Time) (*cacheEntry, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[key]
	if entry == nil {
		return nil, "MISS"
	}
	if now.Before(entry.expiresAt) {
		return entry, "HIT"
	}
	if now.Before(entry.staleAt) {
		return entry, "STALE"
	}
	delete(c.entries, key)
	return nil, "MISS"
}

func (c *ResponseCache) beginFetch(key string) (*inflightFetch, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if call := c.inflight[key]; call != nil {
		return call, false
	}
	call := &inflightFetch{done: make(chan struct{})}
	c.inflight[key] = call
	return call, true
}

func (c *ResponseCache) finishFetch(key string, call *inflightFetch, resp CachedResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	call.resp = resp
	if resp.Err == nil {
		now := c.now()
		if resp.StatusCode == 0 {
			resp.StatusCode = http.StatusOK
		}
		entry := &cacheEntry{
			response:  resp,
			createdAt: now,
			expiresAt: now.Add(resp.TTL),
			staleAt:   now.Add(resp.TTL + resp.Stale),
			etag:      weakETag(resp.Body),
		}
		c.entries[key] = entry
		c.touchLocked(key)
		c.evictLocked()
	}
	delete(c.inflight, key)
	close(call.done)
}

func (c *ResponseCache) fetchWithLimit(ctx context.Context, key string, fetch CacheFetchFunc) CachedResponse {
	start := c.now()
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		resp := CachedResponse{Err: ctx.Err(), ErrorCode: "REQUEST_CANCELLED", Message: "Request was cancelled."}
		c.metrics.RecordScrape(key, resp.StatusCode, c.now().Sub(start), resp.Err)
		return resp
	}
	fetchCtx, cancel := context.WithTimeout(ctx, c.cfg.CacheRefreshTimeout)
	defer cancel()
	resp := fetch(fetchCtx)
	c.metrics.RecordScrape(key, resp.StatusCode, c.now().Sub(start), resp.Err)
	return resp
}

func (c *ResponseCache) refreshAsync(key string, ttl time.Duration, stale time.Duration, fetch CacheFetchFunc, requestID string) {
	call, owner := c.beginFetch(key)
	if !owner {
		return
	}
	go func() {
		resp := c.fetchWithLimit(context.Background(), key, fetch)
		if resp.TTL == 0 {
			resp.TTL = ttl
		}
		if resp.Stale == 0 {
			resp.Stale = stale
		}
		c.finishFetch(key, call, resp)
		if resp.Err != nil {
			zap.S().Warnw("cache background refresh failed", "requestId", requestID, "key", key, "error", resp.Err)
		}
	}()
}

func (c *ResponseCache) write(w http.ResponseWriter, r *http.Request, key string, entry *cacheEntry, cacheStatus string) {
	now := c.now()
	if match := r.Header.Get("If-None-Match"); match != "" && match == entry.etag {
		c.setCacheHeaders(w, entry, cacheStatus, now)
		w.WriteHeader(http.StatusNotModified)
		c.metrics.RecordCache(key, cacheStatus, http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	c.setCacheHeaders(w, entry, cacheStatus, now)
	w.WriteHeader(entry.response.StatusCode)
	_, _ = w.Write(entry.response.Body)
	c.metrics.RecordCache(key, cacheStatus, entry.response.StatusCode)
}

func (c *ResponseCache) setCacheHeaders(w http.ResponseWriter, entry *cacheEntry, cacheStatus string, now time.Time) {
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
}

func (c *ResponseCache) writeFetchError(w http.ResponseWriter, r *http.Request, resp CachedResponse) {
	code := resp.ErrorCode
	if code == "" {
		code = "UPSTREAM_UNAVAILABLE"
	}
	message := resp.Message
	if message == "" {
		message = "Workshop data is temporarily unavailable."
	}
	status := resp.StatusCode
	if status == 0 {
		status = http.StatusServiceUnavailable
	}
	config.WriteError(w, r, status, code, message)
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
	}
}

func cacheControl(entry *cacheEntry, now time.Time) string {
	maxAge := int(time.Until(entry.expiresAt).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	stale := int(time.Until(entry.staleAt).Seconds())
	if stale < 0 {
		stale = 0
	}
	return fmt.Sprintf("public, max-age=%d, stale-if-error=%d", maxAge, stale)
}

func weakETag(body []byte) string {
	sum := sha256.Sum256(body)
	return `W/"` + hex.EncodeToString(sum[:12]) + `"`
}

func CacheKey(parts ...string) string {
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		cleaned = append(cleaned, strings.ToLower(strings.TrimSpace(part)))
	}
	return strings.Join(cleaned, ":")
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
