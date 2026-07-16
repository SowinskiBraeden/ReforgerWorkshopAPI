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

func TestMetricsWindowLocationControlsTodayReset(t *testing.T) {
	location, err := time.LoadLocation("America/Vancouver")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	metrics := NewMetrics()
	metrics.SetWindowLocation(location)
	now := time.Date(2026, 7, 16, 23, 30, 0, 0, time.UTC)
	metrics.now = func() time.Time { return now }
	metrics.mu.Lock()
	metrics.resetRequestWindowsLocked(now)
	metrics.mu.Unlock()

	metrics.RecordRequestMetric(RequestMetricDetails{
		Duration:    50 * time.Millisecond,
		ClientIP:    "203.0.113.10",
		CountryCode: "CA",
		UserAgent:   "test-client",
		Source:      TrafficSourceExternal,
		Method:      "GET",
		Path:        "/v1/mods",
		StatusCode:  http.StatusOK,
	})

	now = time.Date(2026, 7, 17, 0, 30, 0, 0, time.UTC)
	snapshot := metrics.Snapshot(nil)
	if snapshot.Requests.Today != 1 {
		t.Fatalf("today requests after UTC midnight = %d, want 1 in Vancouver timezone", snapshot.Requests.Today)
	}
	if len(snapshot.Geography.Countries) != 1 || snapshot.Geography.Countries[0].Requests.Today != 1 {
		t.Fatalf("geography after UTC midnight = %+v, want same local-day country count", snapshot.Geography.Countries)
	}
	if len(snapshot.ClientSummaries) != 1 || snapshot.ClientSummaries[0].RequestsToday != 1 {
		t.Fatalf("client summaries after UTC midnight = %+v, want same local-day client count", snapshot.ClientSummaries)
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

func TestMetricsWindowSummariesAndSiteClient(t *testing.T) {
	metrics := NewMetrics()
	now := time.Date(2026, 7, 7, 12, 30, 0, 0, time.UTC)
	metrics.now = func() time.Time { return now }

	metrics.RecordRequestMetric(RequestMetricDetails{
		Duration:    120 * time.Millisecond,
		Source:      TrafficSourceInternalLoopback,
		UserAgent:   "Mozilla/5.0",
		Method:      "GET",
		Path:        "/v1/mods",
		StatusCode:  http.StatusOK,
		CacheStatus: "hit",
	})
	metrics.RecordRequestMetric(RequestMetricDetails{
		Duration:    480 * time.Millisecond,
		Source:      TrafficSourceExternal,
		UserAgent:   "curl/8.0",
		Method:      "GET",
		Path:        "/v1/mods",
		StatusCode:  http.StatusInternalServerError,
		CacheStatus: "MISS",
	})

	snapshot := metrics.Snapshot(nil)
	summary := snapshot.Retention.Day.Summary
	if summary.Requests != 2 || summary.Errors != 1 {
		t.Fatalf("day summary requests/errors = %d/%d, want 2/1", summary.Requests, summary.Errors)
	}
	if summary.ResponseTime.LowMs != 120 || summary.ResponseTime.HighMs != 480 {
		t.Fatalf("day summary response low/high = %d/%d, want 120/480", summary.ResponseTime.LowMs, summary.ResponseTime.HighMs)
	}
	if summary.CacheResponseTimes.Hit.HighMs != 120 {
		t.Fatalf("hit high = %d, want 120", summary.CacheResponseTimes.Hit.HighMs)
	}
	if summary.CacheResponseTimes.Miss.LowMs != 480 || summary.CacheResponseTimes.Miss.AverageMs != 480 {
		t.Fatalf("miss low/avg = %d/%f, want 480/480", summary.CacheResponseTimes.Miss.LowMs, summary.CacheResponseTimes.Miss.AverageMs)
	}
	if snapshot.Retention.Year.Summary.Requests != 2 {
		t.Fatalf("year summary requests = %d, want 2", snapshot.Retention.Year.Summary.Requests)
	}

	clients := make(map[string]bool)
	for _, client := range snapshot.ClientSummaries {
		clients[client.Name] = true
	}
	if !clients[SiteClientName] || !clients["curl/8.0"] {
		t.Fatalf("client summaries = %v, want %q and curl/8.0", clients, SiteClientName)
	}
	if len(snapshot.RequestLogs) != 2 || snapshot.RequestLogs[1].Client != SiteClientName {
		t.Fatalf("request logs = %+v, want newest-first with internal client %q", snapshot.RequestLogs, SiteClientName)
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

func TestMetricsStoreRoundTripsGeography(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metrics.json")
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	metrics := NewMetrics()
	metrics.now = func() time.Time { return now }
	metrics.RecordRequestMetric(RequestMetricDetails{
		Duration:    50 * time.Millisecond,
		ClientIP:    "203.0.113.10",
		CountryCode: "US",
		Source:      TrafficSourceExternal,
		Method:      "GET",
		Path:        "/v1/mods",
		StatusCode:  http.StatusOK,
	})
	metrics.RecordRequestMetric(RequestMetricDetails{
		Duration:    70 * time.Millisecond,
		ClientIP:    "198.51.100.20",
		CountryCode: "CA",
		Source:      TrafficSourceExternal,
		Method:      "GET",
		Path:        "/v1/mods",
		StatusCode:  http.StatusOK,
	})

	store, err := NewMetricsStore(path, time.Second)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Save(metrics); err != nil {
		t.Fatalf("save state: %v", err)
	}

	restored := NewMetrics()
	restored.now = func() time.Time { return now.Add(time.Hour) }
	if err := store.Load(restored); err != nil {
		t.Fatalf("load state: %v", err)
	}

	snapshot := restored.Snapshot(nil)
	countries := make(map[string]CountryMetric)
	for _, country := range snapshot.Geography.Countries {
		countries[country.CountryCode] = country
	}
	for _, code := range []string{"US", "CA"} {
		country, ok := countries[code]
		if !ok {
			t.Fatalf("geography after restart is missing %s: %+v", code, snapshot.Geography.Countries)
		}
		if country.Requests.Total != 1 || country.Requests.Today != 1 || country.Requests.ThisMonth != 1 {
			t.Fatalf("%s requests after restart = %+v, want total/today/month 1", code, country.Requests)
		}
		if country.UniqueClientNetworks.Today != 1 {
			t.Fatalf("%s unique networks after restart = %+v, want today 1", code, country.UniqueClientNetworks)
		}
	}
}

func TestImportRequestMetricsFromLogsRollsWindowsToCurrentDay(t *testing.T) {
	dir := t.TempDir()
	log := `{"ts":"2026-07-08T23:55:00Z","msg":"request completed","requestId":"old","clientIP":"203.0.113.10","countryCode":"US","method":"GET","path":"/v1/mods","status":200,"latencyMs":25,"userAgent":"old-client"}
`
	if err := os.WriteFile(filepath.Join(dir, "2026-07-08.log"), []byte(log), 0600); err != nil {
		t.Fatalf("write log: %v", err)
	}

	metrics := NewMetrics()
	metrics.now = func() time.Time {
		return time.Date(2026, 7, 9, 1, 0, 0, 0, time.UTC)
	}

	imported, err := ImportRequestMetricsFromLogs(metrics, dir)
	if err != nil {
		t.Fatalf("import logs: %v", err)
	}
	if imported != 1 {
		t.Fatalf("imported = %d, want 1", imported)
	}

	snapshot := metrics.Snapshot(nil)
	if snapshot.Requests.Total != 1 {
		t.Fatalf("total requests = %d, want 1", snapshot.Requests.Total)
	}
	if snapshot.Requests.Today != 0 {
		t.Fatalf("today requests = %d, want 0 after rolling to current day", snapshot.Requests.Today)
	}
	if len(snapshot.Geography.Countries) != 1 || snapshot.Geography.Countries[0].Requests.Today != 0 {
		t.Fatalf("geography = %+v, want historical country retained with current-day count reset", snapshot.Geography.Countries)
	}
}

func TestImportRequestMetricsFromLogsUsesCursor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "2026-07-08.log")
	first := `{"ts":"2026-07-08T23:55:00Z","msg":"request completed","requestId":"first","clientIP":"203.0.113.10","countryCode":"US","method":"GET","path":"/v1/mods","status":200,"latencyMs":25,"userAgent":"old-client"}
`
	if err := os.WriteFile(path, []byte(first), 0600); err != nil {
		t.Fatalf("write first log: %v", err)
	}

	metrics := NewMetrics()
	now := time.Date(2026, 7, 9, 1, 0, 0, 0, time.UTC)
	metrics.now = func() time.Time { return now }
	metrics.startedAt = now

	imported, err := ImportRequestMetricsFromLogs(metrics, dir)
	if err != nil {
		t.Fatalf("first import logs: %v", err)
	}
	if imported != 1 {
		t.Fatalf("first imported = %d, want 1", imported)
	}

	imported, err = ImportRequestMetricsFromLogs(metrics, dir)
	if err != nil {
		t.Fatalf("second import logs: %v", err)
	}
	if imported != 0 {
		t.Fatalf("second imported = %d, want 0", imported)
	}

	appended := `{"ts":"2026-07-08T23:56:00Z","msg":"request completed","requestId":"second","clientIP":"203.0.113.11","countryCode":"CA","method":"GET","path":"/v1/search","status":200,"latencyMs":30,"userAgent":"old-client"}
{"ts":"2026-07-09T01:01:00Z","msg":"request completed","requestId":"live","clientIP":"203.0.113.12","countryCode":"US","method":"GET","path":"/v1/health","status":200,"latencyMs":10,"userAgent":"live-client"}
`
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatalf("open append log: %v", err)
	}
	if _, err := file.WriteString(appended); err != nil {
		_ = file.Close()
		t.Fatalf("append log: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close log: %v", err)
	}

	imported, err = ImportRequestMetricsFromLogs(metrics, dir)
	if err != nil {
		t.Fatalf("third import logs: %v", err)
	}
	if imported != 1 {
		t.Fatalf("third imported = %d, want 1", imported)
	}
	if got := metrics.TotalRequests(); got != 2 {
		t.Fatalf("total requests = %d, want 2", got)
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
