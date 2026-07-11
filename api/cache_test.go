package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestResponseCacheFreshHitDoesNotInvokeScraper(t *testing.T) {
	cfg := testConfig()
	cache := NewResponseCache(cfg)
	key := "v1:mods:fresh"
	cache.storeResponse(key, CachedResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":true}`), TTL: time.Minute, Stale: time.Minute}, RefreshJobSucceeded, "seed")

	var calls int32
	fetch := func(context.Context) CachedResponse {
		atomic.AddInt32(&calls, 1)
		return CachedResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":false}`)}
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/mods", nil)
	w := httptest.NewRecorder()
	cache.Serve(w, r, key, time.Minute, time.Minute, fetch)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("X-Cache"); got != "HIT" {
		t.Fatalf("X-Cache = %q, want HIT", got)
	}
	if got := w.Header().Get("X-Refresh-Status"); got != "none" {
		t.Fatalf("X-Refresh-Status = %q, want none", got)
	}
	if calls != 0 {
		t.Fatalf("fetch calls = %d, want 0", calls)
	}
}

func TestResponseCacheColdMissReturnsAcceptedAndCompletesJob(t *testing.T) {
	cfg := testConfig()
	cache := NewResponseCache(cfg)
	key := "v1:mods:cold"
	var calls int32
	fetch := func(context.Context) CachedResponse {
		atomic.AddInt32(&calls, 1)
		return CachedResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":true}`)}
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/mods", nil)
	w := httptest.NewRecorder()
	cache.Serve(w, r, key, time.Minute, time.Minute, fetch)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}
	if got := w.Header().Get("X-Cache"); got != "MISS" {
		t.Fatalf("X-Cache = %q, want MISS", got)
	}
	if w.Header().Get("Location") == "" {
		t.Fatal("Location header was not set")
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After header was not set")
	}

	var body RefreshJobSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode job body: %v", err)
	}
	if body.ID == "" || body.ResourceURL != "/v1/mods" {
		t.Fatalf("job body = %+v, want id and resource URL", body)
	}
	job := waitForRefreshStatus(t, cache, body.ID, RefreshJobSucceeded)
	if job.CompletedAt == nil {
		t.Fatal("completed job did not include completed_at")
	}

	w = httptest.NewRecorder()
	cache.Serve(w, r, key, time.Minute, time.Minute, fetch)
	if w.Code != http.StatusOK {
		t.Fatalf("post-refresh status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("X-Cache"); got != "HIT" {
		t.Fatalf("post-refresh X-Cache = %q, want HIT", got)
	}
	if calls != 1 {
		t.Fatalf("fetch calls = %d, want 1", calls)
	}
}

func TestResponseCacheFreshnessHeaders(t *testing.T) {
	cfg := testConfig()
	cache := NewResponseCache(cfg)
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	cache.now = func() time.Time { return now }
	cache.refresh.now = cache.now
	key := "v1:mods:freshness"
	cache.storeResponse(key, CachedResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":true}`), TTL: time.Minute, Stale: time.Hour}, RefreshJobSucceeded, "seed")

	r := httptest.NewRequest(http.MethodGet, "/v1/mods", nil)
	w := httptest.NewRecorder()
	cache.Serve(w, r, key, time.Minute, time.Hour, func(context.Context) CachedResponse {
		t.Fatal("fresh cache should not invoke fetch")
		return CachedResponse{}
	})

	if got := w.Header().Get("X-Cache"); got != "HIT" {
		t.Fatalf("X-Cache = %q, want HIT", got)
	}
	if got := w.Header().Get("Age"); got != "0" {
		t.Fatalf("Age = %q, want 0", got)
	}
	if got := w.Header().Get("X-Cache-Created-At"); got != "2026-07-04T12:00:00Z" {
		t.Fatalf("X-Cache-Created-At = %q", got)
	}
	if got := w.Header().Get("X-Cache-Expires-At"); got != "2026-07-04T12:01:00Z" {
		t.Fatalf("X-Cache-Expires-At = %q", got)
	}
	if got := w.Header().Get("X-Cache-Stale-At"); got != "2026-07-04T13:01:00Z" {
		t.Fatalf("X-Cache-Stale-At = %q", got)
	}

	now = now.Add(30 * time.Second)
	w = httptest.NewRecorder()
	cache.Serve(w, r, key, time.Minute, time.Hour, func(context.Context) CachedResponse {
		t.Fatal("fresh cache should not invoke fetch")
		return CachedResponse{}
	})
	if got := w.Header().Get("Age"); got != "30" {
		t.Fatalf("Age = %q, want 30", got)
	}
	if got := w.Header().Get("X-Cache-Fresh-Seconds"); got != "30" {
		t.Fatalf("X-Cache-Fresh-Seconds = %q, want 30", got)
	}
}

func TestResponseCacheServesStaleImmediatelyAndSchedulesRefresh(t *testing.T) {
	cfg := testConfig()
	cache := NewResponseCache(cfg)
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	cache.now = func() time.Time { return now }
	cache.refresh.now = cache.now
	key := "v1:mods:stale"
	cache.storeResponse(key, CachedResponse{StatusCode: http.StatusOK, Body: []byte(`{"version":1}`), TTL: time.Second, Stale: time.Hour}, RefreshJobSucceeded, "seed")
	now = now.Add(2 * time.Second)

	refreshStarted := make(chan struct{})
	release := make(chan struct{})
	var calls int32
	fetch := func(context.Context) CachedResponse {
		atomic.AddInt32(&calls, 1)
		close(refreshStarted)
		<-release
		return CachedResponse{StatusCode: http.StatusOK, Body: []byte(`{"version":2}`)}
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/mods", nil)
	w := httptest.NewRecorder()
	cache.Serve(w, r, key, time.Second, time.Hour, fetch)
	if w.Code != http.StatusOK {
		t.Fatalf("stale status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("X-Cache"); got != "STALE" {
		t.Fatalf("X-Cache = %q, want STALE", got)
	}
	if strings.TrimSpace(w.Body.String()) != `{"version":1}` {
		t.Fatalf("body = %s, want stale version 1", w.Body.String())
	}
	select {
	case <-refreshStarted:
	case <-time.After(time.Second):
		t.Fatal("background refresh did not start")
	}
	close(release)
	if calls != 1 {
		t.Fatalf("fetch calls = %d, want 1", calls)
	}
}

func TestResponseCacheConcurrentStaleRequestsDeduplicateRefresh(t *testing.T) {
	cfg := testConfig()
	cache := NewResponseCache(cfg)
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	cache.now = func() time.Time { return now }
	cache.refresh.now = cache.now
	key := "v1:mods:stale-dedupe"
	cache.storeResponse(key, CachedResponse{StatusCode: http.StatusOK, Body: []byte(`{"version":1}`), TTL: time.Second, Stale: time.Hour}, RefreshJobSucceeded, "seed")
	now = now.Add(2 * time.Second)

	var calls int32
	release := make(chan struct{})
	fetch := func(context.Context) CachedResponse {
		atomic.AddInt32(&calls, 1)
		<-release
		return CachedResponse{StatusCode: http.StatusOK, Body: []byte(`{"version":2}`)}
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest(http.MethodGet, "/v1/mods", nil)
			w := httptest.NewRecorder()
			cache.Serve(w, r, key, time.Second, time.Hour, fetch)
			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", w.Code)
			}
		}()
	}
	for atomic.LoadInt32(&calls) == 0 {
		time.Sleep(time.Millisecond)
	}
	close(release)
	wg.Wait()
	waitForStoredBody(t, cache, key, `{"version":2}`)
	if calls != 1 {
		t.Fatalf("fetch calls = %d, want 1", calls)
	}
}

func TestResponseCacheStaleRemainsAvailableAfterRefreshFailure(t *testing.T) {
	cfg := testConfig()
	cache := NewResponseCache(cfg)
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	cache.now = func() time.Time { return now }
	cache.refresh.now = cache.now
	key := "v1:mods:stale-failure"
	cache.storeResponse(key, CachedResponse{StatusCode: http.StatusOK, Body: []byte(`{"version":1}`), TTL: time.Second, Stale: time.Hour}, RefreshJobSucceeded, "seed")
	now = now.Add(2 * time.Second)

	r := httptest.NewRequest(http.MethodGet, "/v1/mods", nil)
	w := httptest.NewRecorder()
	cache.Serve(w, r, key, time.Second, time.Hour, func(context.Context) CachedResponse {
		return CachedResponse{Err: errors.New("upstream internal detail"), Message: "Workshop data is temporarily unavailable."}
	})
	if w.Code != http.StatusOK {
		t.Fatalf("stale status = %d, want 200", w.Code)
	}
	locationID := w.Header().Get("X-Refresh-Job-Id")
	if locationID == "" {
		t.Fatal("X-Refresh-Job-Id was not set")
	}
	waitForRefreshStatus(t, cache, locationID, RefreshJobFailed)

	w = httptest.NewRecorder()
	cache.Serve(w, r, key, time.Second, time.Hour, func(context.Context) CachedResponse {
		return CachedResponse{Err: errors.New("still down")}
	})
	if w.Code != http.StatusOK {
		t.Fatalf("post-failure stale status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("X-Cache"); got != "STALE" {
		t.Fatalf("X-Cache = %q, want STALE", got)
	}
	if w.Header().Get("X-Refresh-Failed-At") == "" {
		t.Fatal("X-Refresh-Failed-At was not set after failed refresh")
	}
	if strings.Contains(w.Body.String(), "upstream internal detail") {
		t.Fatal("stale response leaked upstream error")
	}
}

func TestResponseCacheQueueSaturationReturnsServiceUnavailable(t *testing.T) {
	cfg := testConfig()
	cfg.CacheRefreshParallel = 1
	cfg.CacheRefreshQueueSize = 1
	cache := NewResponseCache(cfg)
	release := make(chan struct{})
	started := make(chan struct{})
	var startedOnce sync.Once
	fetch := func(ctx context.Context) CachedResponse {
		startedOnce.Do(func() { close(started) })
		select {
		case <-release:
			return CachedResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":true}`)}
		case <-ctx.Done():
			return CachedResponse{Err: ctx.Err()}
		}
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/mods", nil)
	w := httptest.NewRecorder()
	cache.Serve(w, r, "v1:mods:saturated-1", time.Minute, time.Minute, fetch)
	if w.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202", w.Code)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first refresh did not start")
	}

	r = httptest.NewRequest(http.MethodGet, "/v1/mods", nil)
	w = httptest.NewRecorder()
	cache.Serve(w, r, "v1:mods:saturated-2", time.Minute, time.Minute, fetch)
	if w.Code != http.StatusAccepted {
		t.Fatalf("second status = %d, want 202", w.Code)
	}

	r = httptest.NewRequest(http.MethodGet, "/v1/mods", nil)
	w = httptest.NewRecorder()
	cache.Serve(w, r, "v1:mods:saturated-3", time.Minute, time.Minute, fetch)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("saturated status = %d, want 503", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got == "" {
		t.Fatal("saturated response did not include Retry-After")
	}
	close(release)
}

func TestResponseCacheColdMissDoesNotWaitForScraperTimeout(t *testing.T) {
	cfg := testConfig()
	cfg.CacheRefreshTimeout = 25 * time.Millisecond
	cache := NewResponseCache(cfg)
	start := time.Now()
	r := httptest.NewRequest(http.MethodGet, "/v1/mods", nil)
	w := httptest.NewRecorder()
	cache.Serve(w, r, "v1:mods:timeout", time.Minute, time.Minute, func(ctx context.Context) CachedResponse {
		<-ctx.Done()
		return CachedResponse{Err: ctx.Err()}
	})
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("cold response took %s, want quick 202", elapsed)
	}
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}
	var body RefreshJobSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode job body: %v", err)
	}
	waitForRefreshStatus(t, cache, body.ID, RefreshJobFailed)
}

func TestResponseCacheFailedJobDoesNotExposeRawError(t *testing.T) {
	cfg := testConfig()
	cache := NewResponseCache(cfg)
	r := httptest.NewRequest(http.MethodGet, "/v1/mods", nil)
	w := httptest.NewRecorder()
	cache.Serve(w, r, "v1:mods:failed-job", time.Minute, time.Minute, func(context.Context) CachedResponse {
		return CachedResponse{Err: errors.New("dial tcp 192.0.2.10:443: private upstream detail")}
	})
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}
	var body RefreshJobSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode job body: %v", err)
	}
	job := waitForRefreshStatus(t, cache, body.ID, RefreshJobFailed)
	if strings.Contains(job.Message, "192.0.2.10") || strings.Contains(job.Message, "dial tcp") {
		t.Fatalf("job message leaked raw error: %q", job.Message)
	}
}

func TestResponseCacheJobExpiresAfterRetention(t *testing.T) {
	cfg := testConfig()
	cfg.CacheRefreshJobRetention = time.Second
	cache := NewResponseCache(cfg)
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	cache.now = func() time.Time { return now }
	cache.refresh.now = cache.now

	r := httptest.NewRequest(http.MethodGet, "/v1/mods", nil)
	w := httptest.NewRecorder()
	cache.Serve(w, r, "v1:mods:expires", time.Minute, time.Minute, func(context.Context) CachedResponse {
		return CachedResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":true}`)}
	})
	var body RefreshJobSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode job body: %v", err)
	}
	waitForRefreshStatus(t, cache, body.ID, RefreshJobSucceeded)
	now = now.Add(2 * time.Second)
	cache.refresh.cleanup()
	job, ok := cache.RefreshJob(body.ID)
	if !ok {
		t.Fatal("job was deleted before expired state was observable")
	}
	if job.Status != RefreshJobExpired {
		t.Fatalf("job status = %s, want expired", job.Status)
	}
}

func TestResponseCacheShutdownCancelsWorkersWithoutPanic(t *testing.T) {
	cfg := testConfig()
	cfg.CacheRefreshTimeout = time.Minute
	cache := NewResponseCache(cfg)
	started := make(chan struct{})

	r := httptest.NewRequest(http.MethodGet, "/v1/mods", nil)
	w := httptest.NewRecorder()
	cache.Serve(w, r, "v1:mods:shutdown", time.Minute, time.Minute, func(ctx context.Context) CachedResponse {
		close(started)
		<-ctx.Done()
		return CachedResponse{Err: ctx.Err()}
	})
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := cache.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown error = %v", err)
	}
}

func TestRefreshManagerRunsHighPriorityBeforeLowPriority(t *testing.T) {
	cfg := testConfig()
	cfg.CacheRefreshParallel = 1
	cfg.CacheRefreshQueueSize = 4
	cache := NewResponseCache(cfg)

	releaseFirst := make(chan struct{})
	firstStarted := make(chan struct{})
	var firstOnce sync.Once
	order := make(chan string, 2)
	blocking := func(ctx context.Context) CachedResponse {
		firstOnce.Do(func() { close(firstStarted) })
		select {
		case <-releaseFirst:
		case <-ctx.Done():
		}
		return CachedResponse{StatusCode: http.StatusOK, Body: []byte(`{"first":true}`)}
	}
	_, created, err := cache.EnqueueRefresh("v1:mods:first", "/v1/mods/1", time.Minute, time.Minute, RefreshPriorityNormal, blocking)
	if err != nil || !created {
		t.Fatalf("first enqueue created=%v err=%v", created, err)
	}
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first job did not start")
	}

	_, created, err = cache.EnqueueRefresh("v1:mods:low", "/v1/mods/2", time.Minute, time.Minute, RefreshPriorityLow, func(context.Context) CachedResponse {
		order <- "low"
		return CachedResponse{StatusCode: http.StatusOK, Body: []byte(`{"low":true}`)}
	})
	if err != nil || !created {
		t.Fatalf("low enqueue created=%v err=%v", created, err)
	}
	_, created, err = cache.EnqueueRefresh("v1:mods:high", "/v1/mods/3", time.Minute, time.Minute, RefreshPriorityHigh, func(context.Context) CachedResponse {
		order <- "high"
		return CachedResponse{StatusCode: http.StatusOK, Body: []byte(`{"high":true}`)}
	})
	if err != nil || !created {
		t.Fatalf("high enqueue created=%v err=%v", created, err)
	}

	close(releaseFirst)
	select {
	case got := <-order:
		if got != "high" {
			t.Fatalf("first queued job = %q, want high", got)
		}
	case <-time.After(time.Second):
		t.Fatal("queued jobs did not run")
	}
}

func TestCacheKeyNormalization(t *testing.T) {
	search := NormalizeSearch("  Better   Hits   Effects  ", 120)
	if search != "Better Hits Effects" {
		t.Fatalf("normalized search = %q", search)
	}
	if got := NormalizeSearch("abcdef", 3); got != "abc" {
		t.Fatalf("capped search = %q, want abc", got)
	}
	key := CacheKey("V1", "MODS", "1", search, NormalizeSort("Newest", map[string]bool{"newest": true}))
	if key != "v1:mods:1:better hits effects:newest" {
		t.Fatalf("cache key = %q", key)
	}
	if got := ModsCacheKey(1, " hello ", "Popularity", nil); got != ModsCacheKey(0, "hello", "", nil) {
		t.Fatalf("canonical list keys differ: %q", got)
	}
	if got := ModsCacheKey(1, " hello ", "Popularity", nil); got != ModsCacheKey(1, "hello", "popularity", nil) {
		t.Fatalf("canonical sort keys differ: %q", got)
	}
	if got := ModCacheKey(" ABC123 "); got != "v1:mod:abc123" {
		t.Fatalf("mod key = %q", got)
	}
}

func TestSelectCacheTTL(t *testing.T) {
	cfg := testConfig()
	cfg.ListCacheTTL = time.Hour
	cfg.ListCacheStale = 24 * time.Hour
	cfg.SearchCacheTTL = 10 * time.Minute
	cfg.SearchCacheStale = 2 * time.Hour
	cfg.ModDetailCacheTTL = 30 * time.Minute
	cfg.ModDetailCacheStale = 24 * time.Hour
	cfg.NotFoundCacheTTL = 15 * time.Minute

	if got := SelectCacheTTL(cfg, "mods", "", http.StatusOK); got.Fresh != time.Hour || got.Stale != 24*time.Hour {
		t.Fatalf("list policy = %+v", got)
	}
	if got := SelectCacheTTL(cfg, "mods", "rhs", http.StatusOK); got.Fresh != 10*time.Minute || got.Stale != 2*time.Hour {
		t.Fatalf("search policy = %+v", got)
	}
	if got := SelectCacheTTL(cfg, "mod", "", http.StatusOK); got.Fresh != 30*time.Minute || got.Stale != 24*time.Hour {
		t.Fatalf("detail policy = %+v", got)
	}
	if got := SelectCacheTTL(cfg, "mod", "", http.StatusNotFound); got.Fresh != 15*time.Minute || got.Stale != 0 {
		t.Fatalf("not found policy = %+v", got)
	}
}

func waitForRefreshStatus(t *testing.T, cache *ResponseCache, id string, status RefreshJobStatus) RefreshJobSnapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job, ok := cache.RefreshJob(id)
		if ok && job.Status == status {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	job, ok := cache.RefreshJob(id)
	t.Fatalf("job %s status = %+v ok=%v, want %s", id, job, ok, status)
	return RefreshJobSnapshot{}
}

func waitForStoredBody(t *testing.T, cache *ResponseCache, key string, body string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entry, _ := cache.lookup(key, cache.now())
		if entry != nil && string(entry.response.Body) == body {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	entry, _ := cache.lookup(key, cache.now())
	if entry == nil {
		t.Fatalf("cache entry %s was not stored", key)
	}
	t.Fatalf("cache entry body = %s, want %s", entry.response.Body, body)
}
