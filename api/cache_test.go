package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestResponseCacheHit(t *testing.T) {
	cfg := testConfig()
	cache := NewResponseCache(cfg)
	var calls int32

	fetch := func(context.Context) CachedResponse {
		atomic.AddInt32(&calls, 1)
		return CachedResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":true}`)}
	}

	for i := 0; i < 2; i++ {
		r := httptest.NewRequest(http.MethodGet, "/v1/mods", nil)
		w := httptest.NewRecorder()
		cache.Serve(w, r, "v1:mods:1", time.Minute, time.Minute, fetch)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
	}
	if calls != 1 {
		t.Fatalf("fetch calls = %d, want 1", calls)
	}
}

func TestResponseCacheServesStaleWhenRefreshFails(t *testing.T) {
	cfg := testConfig()
	cache := NewResponseCache(cfg)
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	cache.now = func() time.Time { return now }

	firstFetch := func(context.Context) CachedResponse {
		return CachedResponse{StatusCode: http.StatusOK, Body: []byte(`{"version":1}`)}
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/mods", nil)
	w := httptest.NewRecorder()
	cache.Serve(w, r, "v1:mods:stale", time.Second, time.Hour, firstFetch)

	now = now.Add(2 * time.Second)
	refreshStarted := make(chan struct{})
	refreshDone := make(chan struct{})
	failingFetch := func(context.Context) CachedResponse {
		close(refreshStarted)
		defer close(refreshDone)
		return CachedResponse{Err: errors.New("upstream down")}
	}
	w = httptest.NewRecorder()
	cache.Serve(w, r, "v1:mods:stale", time.Second, time.Hour, failingFetch)
	if w.Code != http.StatusOK {
		t.Fatalf("stale status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("X-Cache"); got != "STALE" {
		t.Fatalf("X-Cache = %q, want STALE", got)
	}
	select {
	case <-refreshStarted:
	case <-time.After(time.Second):
		t.Fatal("background refresh did not start")
	}
	<-refreshDone
}

func TestResponseCacheDeduplicatesConcurrentMiss(t *testing.T) {
	cfg := testConfig()
	cache := NewResponseCache(cfg)
	var calls int32
	release := make(chan struct{})
	fetch := func(context.Context) CachedResponse {
		atomic.AddInt32(&calls, 1)
		<-release
		return CachedResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":true}`)}
	}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest(http.MethodGet, "/v1/mods", nil)
			w := httptest.NewRecorder()
			cache.Serve(w, r, "v1:mods:dedupe", time.Minute, time.Minute, fetch)
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
	if calls != 1 {
		t.Fatalf("fetch calls = %d, want 1", calls)
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
}
