package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMetricsTracksRequestsAndResponseTimes(t *testing.T) {
	metrics := NewMetrics()
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	metrics.now = func() time.Time { return now }
	metrics.startedAt = now
	metrics.mu.Lock()
	metrics.resetRequestWindowsLocked(now)
	metrics.mu.Unlock()

	metrics.RecordRequest(100 * time.Millisecond)
	metrics.RecordRequest(300 * time.Millisecond)

	snapshot := metrics.Snapshot(nil)
	if snapshot.Requests.Total != 2 {
		t.Fatalf("total requests = %d, want 2", snapshot.Requests.Total)
	}
	if snapshot.Requests.Today != 2 || snapshot.Requests.ThisWeek != 2 || snapshot.Requests.ThisMonth != 2 {
		t.Fatalf("request windows = %+v, want all 2", snapshot.Requests)
	}
	if snapshot.ResponseTime.LowMs != 100 {
		t.Fatalf("low response time = %d, want 100", snapshot.ResponseTime.LowMs)
	}
	if snapshot.ResponseTime.HighMs != 300 {
		t.Fatalf("high response time = %d, want 300", snapshot.ResponseTime.HighMs)
	}
	if snapshot.ResponseTime.AverageMs != 200 {
		t.Fatalf("average response time = %f, want 200", snapshot.ResponseTime.AverageMs)
	}

	now = now.Add(24 * time.Hour)
	snapshot = metrics.Snapshot(nil)
	if snapshot.Requests.Today != 0 {
		t.Fatalf("next-day requests today = %d, want 0", snapshot.Requests.Today)
	}
	if snapshot.Requests.ThisWeek != 2 || snapshot.Requests.ThisMonth != 2 {
		t.Fatalf("next-day week/month windows = %+v, want retained week/month", snapshot.Requests)
	}
}

func TestMetricsRetentionWindows(t *testing.T) {
	metrics := NewMetrics()
	now := time.Date(2026, 7, 7, 12, 30, 0, 0, time.UTC)
	metrics.now = func() time.Time { return now }

	metrics.RecordRequest(100 * time.Millisecond)
	metrics.RecordCache("v1:mods:1", "MISS", http.StatusOK)
	metrics.RecordScrape("v1:mods:1", http.StatusOK, 250*time.Millisecond, nil)

	now = now.Add(-2 * time.Hour)
	metrics.RecordRequest(300 * time.Millisecond)

	now = time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	metrics.RecordRequest(500 * time.Millisecond)

	now = time.Date(2026, 7, 7, 12, 30, 0, 0, time.UTC)
	snapshot := metrics.Snapshot(nil)

	if len(snapshot.Retention.Day.Buckets) != 24 {
		t.Fatalf("day buckets = %d, want 24", len(snapshot.Retention.Day.Buckets))
	}
	if len(snapshot.Retention.Week.Buckets) != 7 {
		t.Fatalf("week buckets = %d, want 7", len(snapshot.Retention.Week.Buckets))
	}
	if len(snapshot.Retention.Month.Buckets) != 31 {
		t.Fatalf("month buckets = %d, want 31", len(snapshot.Retention.Month.Buckets))
	}
	if len(snapshot.Retention.Year.Buckets) != 12 {
		t.Fatalf("year buckets = %d, want 12", len(snapshot.Retention.Year.Buckets))
	}

	currentHour := snapshot.Retention.Day.Buckets[len(snapshot.Retention.Day.Buckets)-1]
	if currentHour.Requests != 1 {
		t.Fatalf("current hour requests = %d, want 1", currentHour.Requests)
	}
	if currentHour.Cache.Misses != 1 {
		t.Fatalf("current hour cache misses = %d, want 1", currentHour.Cache.Misses)
	}
	if currentHour.Scrapes.Total != 1 {
		t.Fatalf("current hour scrapes = %d, want 1", currentHour.Scrapes.Total)
	}
}

func TestMetricsTracksCoarseGeographyAndUniqueClientNetworks(t *testing.T) {
	metrics := NewMetrics()
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	metrics.now = func() time.Time { return now }
	metrics.startedAt = now
	metrics.mu.Lock()
	metrics.resetRequestWindowsLocked(now)
	metrics.mu.Unlock()

	metrics.RecordRequestDetails(100*time.Millisecond, "203.0.113.10", "CA")
	metrics.RecordRequestDetails(120*time.Millisecond, "203.0.113.42", "CA")
	metrics.RecordRequestDetails(80*time.Millisecond, "198.51.100.20", "US")

	snapshot := metrics.Snapshot(nil)
	if snapshot.Requests.UniqueClientNetworks.Today != 2 {
		t.Fatalf("unique networks today = %d, want 2", snapshot.Requests.UniqueClientNetworks.Today)
	}
	if len(snapshot.Geography.Countries) != 2 {
		t.Fatalf("countries = %d, want 2", len(snapshot.Geography.Countries))
	}
	if snapshot.Geography.Countries[0].CountryCode != "CA" {
		t.Fatalf("top country = %q, want CA", snapshot.Geography.Countries[0].CountryCode)
	}
	if snapshot.Geography.Countries[0].Requests.Today != 2 {
		t.Fatalf("CA requests today = %d, want 2", snapshot.Geography.Countries[0].Requests.Today)
	}
	if snapshot.Geography.Countries[0].UniqueClientNetworks.Today != 1 {
		t.Fatalf("CA unique networks today = %d, want 1", snapshot.Geography.Countries[0].UniqueClientNetworks.Today)
	}

	now = now.Add(24 * time.Hour)
	snapshot = metrics.Snapshot(nil)
	if snapshot.Requests.UniqueClientNetworks.Today != 0 {
		t.Fatalf("next-day unique networks today = %d, want 0", snapshot.Requests.UniqueClientNetworks.Today)
	}
	if snapshot.Requests.UniqueClientNetworks.ThisWeek != 2 {
		t.Fatalf("same-week unique networks = %d, want 2", snapshot.Requests.UniqueClientNetworks.ThisWeek)
	}
}

func TestMetricsTracksCacheAndScrapes(t *testing.T) {
	cfg := testConfig()
	metrics := NewMetrics()
	cache := NewResponseCache(cfg, metrics)
	fetch := func(context.Context) CachedResponse {
		return CachedResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":true}`)}
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/mods", nil)
	w := httptest.NewRecorder()
	cache.Serve(w, r, "v1:mods:metrics", time.Minute, time.Minute, fetch)
	if w.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202", w.Code)
	}
	var job RefreshJobSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
		t.Fatalf("failed to decode refresh job: %v", err)
	}
	waitForRefreshStatus(t, cache, job.ID, RefreshJobSucceeded)

	w = httptest.NewRecorder()
	cache.Serve(w, r, "v1:mods:metrics", time.Minute, time.Minute, fetch)
	if w.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("second X-Cache = %q, want HIT", w.Header().Get("X-Cache"))
	}

	snapshot := metrics.Snapshot(cache)
	if snapshot.Cache.Misses != 1 || snapshot.Cache.Hits != 1 {
		t.Fatalf("cache counts = %+v, want 1 miss and 1 hit", snapshot.Cache)
	}
	if snapshot.Scrapes.Total != 1 {
		t.Fatalf("scrape total = %d, want 1", snapshot.Scrapes.Total)
	}
	if snapshot.Cache.Entries != 1 || len(snapshot.Cache.LatestEntries) != 1 {
		t.Fatalf("cache entries = %d latest=%d, want 1 latest entry", snapshot.Cache.Entries, len(snapshot.Cache.LatestEntries))
	}
}
