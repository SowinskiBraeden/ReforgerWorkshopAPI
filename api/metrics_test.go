package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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

func TestNormalizeUserAgent(t *testing.T) {
	cases := map[string]string{
		"GATZSteamModPlugin/1.0 (+https://gatzgamehosting.com)": "GATZSteamModPlugin/1.0",
		"node":                                 "node",
		"curl/8.21.0":                          "curl/8.21.0",
		"Mozilla/5.0 Chrome/120 Safari/537.36": "browser",
		"":                                     "unknown",
	}
	for raw, want := range cases {
		if got := NormalizeUserAgent(raw); got != want {
			t.Fatalf("NormalizeUserAgent(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestEndpointNormalizationAndGrouping(t *testing.T) {
	if got := NormalizeEndpointPath("/v1/mod/5965550F24A0C152"); got != "/v1/mod/{id}" {
		t.Fatalf("normalized mod path = %q", got)
	}
	if got := NormalizeEndpointPath("/v1/mods/12"); got != "/v1/mods/{page}" {
		t.Fatalf("normalized mods page path = %q", got)
	}
	if got := EndpointGroupForRequest("/v1/mods", "search=radio&sort=newest"); got != "search" {
		t.Fatalf("search group = %q", got)
	}
	if got := EndpointGroupForCacheKey("v1:mod:5965550F24A0C152"); got != "mod_detail" {
		t.Fatalf("cache group = %q", got)
	}
}

func TestMetricsAcceptedFlowAndClientSummaries(t *testing.T) {
	metrics := NewMetrics()
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	metrics.now = func() time.Time { return now }
	metrics.mu.Lock()
	metrics.resetRequestWindowsLocked(now)
	metrics.mu.Unlock()

	metrics.RecordRequestMetric(RequestMetricDetails{
		Duration: 50 * time.Millisecond, ClientIP: "203.0.113.10", CountryCode: "US",
		UserAgent: "GATZSteamModPlugin/1.0 (+https://gatzgamehosting.com)", Source: TrafficSourceExternal,
		Method: "GET", Path: "/v1/mods", RawQuery: "search=radio", StatusCode: http.StatusAccepted, CacheStatus: "MISS",
	})
	metrics.RecordCache("v1:mods:1:radio:newest", "MISS", http.StatusAccepted)
	metrics.RecordRefreshCompletion("v1:mods:1:radio:newest", RefreshJobSucceeded, 420*time.Millisecond, "", false)
	metrics.RecordCache("v1:mods:1:radio:newest", "HIT", http.StatusOK)

	snapshot := metrics.Snapshot(nil)
	if snapshot.AcceptedFlow.Accepted != 1 || snapshot.AcceptedFlow.Succeeded != 1 || snapshot.AcceptedFlow.LaterHit != 1 {
		t.Fatalf("accepted flow = %+v, want accepted/succeeded/laterHit", snapshot.AcceptedFlow)
	}
	if len(snapshot.Clients.TopUserAgentsToday) != 1 || snapshot.Clients.TopUserAgentsToday[0].Name != "GATZSteamModPlugin/1.0" {
		t.Fatalf("clients = %+v, want GATZ", snapshot.Clients.TopUserAgentsToday)
	}
	if snapshot.TrafficSources.External != 1 {
		t.Fatalf("traffic sources = %+v, want external=1", snapshot.TrafficSources)
	}
	if len(snapshot.ClientSummaries) != 1 || snapshot.ClientSummaries[0].Accepted202 != 1 {
		t.Fatalf("client summaries = %+v, want accepted202", snapshot.ClientSummaries)
	}
	if snapshot.CacheByEndpoint["search"].Misses != 1 || snapshot.CacheByEndpoint["search"].Hits != 1 {
		t.Fatalf("cache by endpoint = %+v, want search hit/miss", snapshot.CacheByEndpoint)
	}
}

func TestScrapeErrorReasonClassification(t *testing.T) {
	if got := ClassifyScrapeError(context.DeadlineExceeded, 0); got != "upstream_timeout" {
		t.Fatalf("deadline reason = %q", got)
	}
	if got := ClassifyScrapeError(nil, http.StatusNotFound); got != "not_found" {
		t.Fatalf("404 reason = %q", got)
	}
	if got := ClassifyScrapeError(assertErr("strconv.Atoi: parsing broken"), 0); got != "parser_error" {
		t.Fatalf("parser reason = %q", got)
	}
	metrics := NewMetrics()
	metrics.RecordScrapeResult("v1:mods:1", 0, time.Millisecond, assertErr("scraper panic while refreshing resource"), "panic_recovered")
	snapshot := metrics.Snapshot(nil)
	if snapshot.ScrapeErrorsByReason["panic_recovered"] != 1 {
		t.Fatalf("scrape reasons = %+v, want panic_recovered", snapshot.ScrapeErrorsByReason)
	}
}

func TestMetricsPrunesTrackedClients(t *testing.T) {
	metrics := NewMetrics()
	for i := 0; i < metricsMaxTrackedClients+20; i++ {
		metrics.RecordRequestMetric(RequestMetricDetails{UserAgent: "client-" + strconv.Itoa(i), Source: TrafficSourceExternal, Method: "GET", Path: "/v1/health", StatusCode: http.StatusOK})
	}
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	if len(metrics.userAgents) > metricsMaxTrackedClients {
		t.Fatalf("tracked clients = %d, want <= %d", len(metrics.userAgents), metricsMaxTrackedClients)
	}
}

func TestMetricsStoreLoadsOldStateWithoutNewFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metrics.json")
	oldState := `{
		"version": 1,
		"savedAt": "2026-07-08T00:00:00Z",
		"totalRequests": 7,
		"requestDayKey": "2026-07-08",
		"requestsToday": 7,
		"requestWeekKey": "2026-W28",
		"requestsThisWeek": 7,
		"requestMonthKey": "2026-07",
		"requestsThisMonth": 7,
		"cacheHits": 2,
		"cacheMisses": 3,
		"cacheStales": 1
	}`
	if err := os.WriteFile(path, []byte(oldState), 0600); err != nil {
		t.Fatalf("write old state: %v", err)
	}
	store, err := NewMetricsStore(path, time.Second)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	metrics := NewMetrics()
	metrics.now = func() time.Time { return time.Date(2026, 7, 8, 1, 0, 0, 0, time.UTC) }
	if err := store.Load(metrics); err != nil {
		t.Fatalf("load old state: %v", err)
	}
	snapshot := metrics.Snapshot(nil)
	if snapshot.Requests.Total != 7 || snapshot.Cache.Hits != 2 {
		t.Fatalf("snapshot = %+v, want old totals restored", snapshot)
	}
	if snapshot.Clients.TopUserAgentsToday == nil {
		t.Fatal("new client metrics should default to an empty slice/map, not crash")
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
