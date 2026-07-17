package telemetry

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "telemetry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedEvents(t *testing.T, store *Store, events []RequestEvent) {
	t.Helper()
	recorder := NewRecorder(store)
	for _, e := range events {
		recorder.RecordRequest(e)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
}

func day(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.ParseInLocation("2006-01-02", value, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestAggregationIsIdempotent(t *testing.T) {
	store := testStore(t)
	base := day(t, "2026-07-10").Add(10 * time.Hour)
	seedEvents(t, store, []RequestEvent{
		{At: base, RouteTemplate: "/v1/mods", Method: "GET", EndpointGroup: "mod_list", Status: 200, DurationMs: 10, Source: SourceExternalAPI, AccountID: "acct_1", APIKeyID: "key_1", NetworkID: "n1", CountryCode: "US"},
		{At: base.Add(time.Minute), RouteTemplate: "/v1/mods", Method: "GET", EndpointGroup: "mod_list", Status: 500, DurationMs: 30, Source: SourceExternalAPI, AccountID: "acct_1", APIKeyID: "key_1", NetworkID: "n1", CountryCode: "US"},
		{At: base.Add(2 * time.Minute), RouteTemplate: "/v1/health", Method: "GET", EndpointGroup: "health", Status: 200, DurationMs: 1, Source: SourceHealth, NetworkID: "n2", CountryCode: "CA"},
	})
	aggregator := NewAggregator(store)
	aggregator.now = func() time.Time { return base.Add(2 * time.Hour) }

	snapshot := func() (int64, int64, int64) {
		var rows, requests, activity int64
		_ = store.db.QueryRow(`SELECT COUNT(*) FROM usage_daily`).Scan(&rows)
		_ = store.db.QueryRow(`SELECT COALESCE(SUM(requests),0) FROM usage_daily`).Scan(&requests)
		_ = store.db.QueryRow(`SELECT COUNT(*) FROM entity_activity`).Scan(&activity)
		return rows, requests, activity
	}
	if err := aggregator.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows1, requests1, activity1 := snapshot()
	if requests1 != 3 {
		t.Fatalf("aggregated requests = %d, want 3", requests1)
	}
	for i := 0; i < 3; i++ {
		if err := aggregator.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	rows2, requests2, activity2 := snapshot()
	if rows1 != rows2 || requests1 != requests2 || activity1 != activity2 {
		t.Fatalf("aggregation not idempotent: (%d,%d,%d) vs (%d,%d,%d)",
			rows1, requests1, activity1, rows2, requests2, activity2)
	}
}

func TestAggregationBucketsAndActivityExclusions(t *testing.T) {
	store := testStore(t)
	// One event 23:59 on day 1, one 00:01 on day 2: distinct daily buckets.
	seedEvents(t, store, []RequestEvent{
		{At: day(t, "2026-07-10").Add(23*time.Hour + 59*time.Minute), RouteTemplate: "/v1/mods", Method: "GET", EndpointGroup: "mod_list", Status: 200, DurationMs: 5, Source: SourceExternalAPI, AccountID: "acct_1", NetworkID: "n1", CountryCode: "US"},
		{At: day(t, "2026-07-11").Add(1 * time.Minute), RouteTemplate: "/v1/mods", Method: "GET", EndpointGroup: "mod_list", Status: 200, DurationMs: 5, Source: SourceExternalAPI, AccountID: "acct_1", NetworkID: "n1", CountryCode: "US"},
		// Crawler + health traffic on day 2 must not create activity rows.
		{At: day(t, "2026-07-11").Add(2 * time.Minute), RouteTemplate: "/v1/mods", Method: "GET", EndpointGroup: "mod_list", Status: 200, DurationMs: 5, Source: SourceCrawler, NetworkID: "n_crawler", CountryCode: "US"},
		{At: day(t, "2026-07-11").Add(3 * time.Minute), RouteTemplate: "/v1/health", Method: "GET", EndpointGroup: "health", Status: 200, DurationMs: 1, Source: SourceHealth, NetworkID: "n_health", CountryCode: "US"},
	})
	aggregator := NewAggregator(store)
	aggregator.now = func() time.Time { return day(t, "2026-07-12") }
	if err := aggregator.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var day1, day2 int64
	_ = store.db.QueryRow(`SELECT COALESCE(SUM(requests),0) FROM usage_daily WHERE bucket='2026-07-10'`).Scan(&day1)
	_ = store.db.QueryRow(`SELECT COALESCE(SUM(requests),0) FROM usage_daily WHERE bucket='2026-07-11'`).Scan(&day2)
	if day1 != 1 || day2 != 3 {
		t.Fatalf("bucket boundaries wrong: day1=%d day2=%d", day1, day2)
	}
	var networks int64
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM entity_activity WHERE entity_type='network' AND day='2026-07-11'`).Scan(&networks)
	if networks != 1 {
		t.Fatalf("crawler/health networks leaked into activity: %d rows, want 1", networks)
	}
	// User active both days → days_active 2 in the profile.
	profile, ok := store.GetEntityProfile(context.Background(), "user", "acct_1")
	if !ok || profile.DaysActive != 2 {
		t.Fatalf("profile days_active = %+v, want 2", profile)
	}
}

func TestPercentilesAreExact(t *testing.T) {
	durations := make([]float64, 100)
	for i := range durations {
		durations[i] = float64(i + 1) // 1..100
	}
	stats := durationStats(durations)
	if stats.p50 != 50 || stats.p95 != 95 || stats.p99 != 99 || stats.min != 1 || stats.max != 100 {
		t.Fatalf("percentiles wrong: %+v", stats)
	}
	single := durationStats([]float64{42})
	if single.p50 != 42 || single.p99 != 42 {
		t.Fatalf("single-sample percentiles wrong: %+v", single)
	}
}

func TestRetentionSummaryMath(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	// Client A first seen day 1, active day 2 (1-day retained).
	// Client B first seen day 1, never again (churn candidate).
	seedEvents(t, store, []RequestEvent{
		{At: day(t, "2026-07-01").Add(time.Hour), Source: SourceExternalAPI, Status: 200, AccountID: "A", RouteTemplate: "/v1/mods", Method: "GET", EndpointGroup: "mod_list", CountryCode: "US"},
		{At: day(t, "2026-07-02").Add(time.Hour), Source: SourceExternalAPI, Status: 200, AccountID: "A", RouteTemplate: "/v1/mods", Method: "GET", EndpointGroup: "mod_list", CountryCode: "US"},
		{At: day(t, "2026-07-01").Add(2 * time.Hour), Source: SourceExternalAPI, Status: 200, AccountID: "B", RouteTemplate: "/v1/mods", Method: "GET", EndpointGroup: "mod_list", CountryCode: "US"},
	})
	aggregator := NewAggregator(store)
	aggregator.now = func() time.Time { return day(t, "2026-07-20") }
	if err := aggregator.Run(ctx); err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return day(t, "2026-07-20") }
	summary, err := store.RetentionSummaryFor(ctx, "user", day(t, "2026-07-01"), day(t, "2026-07-05"))
	if err != nil {
		t.Fatal(err)
	}
	if summary.Cohort != 2 {
		t.Fatalf("cohort = %d, want 2", summary.Cohort)
	}
	if summary.Day1 != 0.5 {
		t.Fatalf("day1 retention = %v, want 0.5", summary.Day1)
	}
	if summary.New != 2 {
		t.Fatalf("new = %d, want 2", summary.New)
	}
}

func TestRebuildReproducesAggregates(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	base := day(t, "2026-07-10").Add(8 * time.Hour)
	seedEvents(t, store, []RequestEvent{
		{At: base, RouteTemplate: "/v1/mod/{id}", Method: "GET", EndpointGroup: "mod_detail", Status: 200, DurationMs: 12, Source: SourceExternalAPI, AccountID: "acct", NetworkID: "n1", CountryCode: "DE", ModID: "ABC123DEF"},
	})
	aggregator := NewAggregator(store)
	aggregator.now = func() time.Time { return base.Add(24 * time.Hour) }
	if err := aggregator.Run(ctx); err != nil {
		t.Fatal(err)
	}
	var before int64
	_ = store.db.QueryRow(`SELECT COALESCE(SUM(requests),0) FROM endpoint_daily`).Scan(&before)
	if err := aggregator.RebuildAll(ctx); err != nil {
		t.Fatal(err)
	}
	var after int64
	_ = store.db.QueryRow(`SELECT COALESCE(SUM(requests),0) FROM endpoint_daily`).Scan(&after)
	if before != after || before != 1 {
		t.Fatalf("rebuild changed aggregates: before=%d after=%d", before, after)
	}
	var mods int64
	_ = store.db.QueryRow(`SELECT COALESCE(SUM(requests),0) FROM mod_daily WHERE mod_id='ABC123DEF'`).Scan(&mods)
	if mods != 1 {
		t.Fatalf("mod_daily missing after rebuild: %d", mods)
	}
}
