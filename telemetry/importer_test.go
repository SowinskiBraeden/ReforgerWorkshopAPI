package telemetry

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleLogLines = `{"level":"info","ts":"2026-07-14T10:00:00.000Z","caller":"api/middleware.go:107","msg":"request completed","requestId":"req-1","clientIP":"203.0.113.10","countryCode":"US","method":"GET","path":"/v1/mods/1","query":"search=radio&api_key=leaked-secret","status":200,"latencyMs":15,"userAgent":"curl/8.0.1"}
{"level":"info","ts":"2026-07-14T10:00:01.000Z","caller":"api/refresh.go:203","msg":"refresh job queued","requestId":"req-1","jobId":"job-1","resourceKey":"v1:mods:1:radio:popularity:","resourceURL":"/v1/mods/1?search=radio","priority":"high","queueDepth":1}
{"level":"info","ts":"2026-07-14T10:00:04.000Z","caller":"api/refresh.go:323","msg":"refresh job finished","requestId":"req-1","jobId":"job-1","resourceKey":"v1:mods:1:radio:popularity:","status":"succeeded","statusCode":200,"durationMs":3000,"worker":2}
{"level":"info","ts":"2026-07-14T10:00:05.000Z","caller":"api/middleware.go:194","msg":"rate limit rejected","requestId":"req-2","clientIP":"203.0.113.99","path":"/v1/mods","bucket":"anonymous:203.0.113.99"}
{"level":"error","ts":"2026-07-14T10:00:06.000Z","caller":"handlers/api.go:152","msg":"index storage unavailable","path":"data/index.db","error":"disk gone"}
this line is not json at all
{"level":"info","ts":"2026-07-14T10:00:07.000Z","caller":"api/cache.go:209","msg":"cache served","requestId":"req-1","key":"v1:mods:1:radio:popularity:","status":"HIT"}
`

func writeSampleLog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "2026-07-14.log"), []byte(sampleLogLines), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestImporterEndToEnd(t *testing.T) {
	store := testStore(t)
	dir := writeSampleLog(t)
	importer := NewImporter(store, ImporterConfig{HashSecret: "import-secret", Rotation: "monthly"})
	ctx := context.Background()

	summary, err := importer.ImportDir(ctx, dir, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Requests != 1 {
		t.Fatalf("requests = %d, want 1 (cache/job lines must not create request events)", summary.Requests)
	}
	if summary.Jobs != 2 {
		t.Fatalf("job events = %d, want 2", summary.Jobs)
	}
	if summary.Malformed != 1 {
		t.Fatalf("malformed = %d, want 1", summary.Malformed)
	}
	if summary.Errors != 1 {
		t.Fatalf("errors = %d, want 1", summary.Errors)
	}

	// One request row, with the IP converted to a network id and the query
	// param redacted.
	var count int
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM request_events`).Scan(&count)
	if count != 1 {
		t.Fatalf("request_events rows = %d, want 1", count)
	}
	event, ok, err := store.GetRequest(ctx, "req-1")
	if err != nil || !ok {
		t.Fatal("request req-1 missing")
	}
	if event.NetworkID == "" {
		t.Error("network id missing (country enrichment without stored IP)")
	}
	if strings.Contains(event.Query, "leaked-secret") {
		t.Errorf("api_key value leaked into stored query: %s", event.Query)
	}
	if event.CountryCode != "US" {
		t.Errorf("country = %q, want US", event.CountryCode)
	}

	// Queued + finished correlate into a single job row.
	job, ok, err := store.GetJob(ctx, "job-1")
	if err != nil || !ok {
		t.Fatal("job-1 missing")
	}
	if job.Status != JobSucceeded || job.DurationMs != 3000 || job.EnqueuedAt.IsZero() || job.FinishedAt.IsZero() {
		t.Fatalf("job lifecycle not merged: %+v", job)
	}

	// No raw IP anywhere in the database, including the rate-limit bucket.
	rows, err := store.db.Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		_ = rows.Scan(&name)
		tables = append(tables, name)
	}
	rows.Close()
	for _, tableName := range tables {
		dataRows, err := store.db.Query(`SELECT * FROM ` + tableName)
		if err != nil {
			t.Fatal(err)
		}
		columns, _ := dataRows.Columns()
		for dataRows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			_ = dataRows.Scan(pointers...)
			for _, value := range values {
				text, isText := value.(string)
				if isText && (strings.Contains(text, "203.0.113.10") || strings.Contains(text, "203.0.113.99")) {
					t.Errorf("raw IP persisted in table %s: %s", tableName, text)
				}
			}
		}
		dataRows.Close()
	}

	// Re-running the import must not duplicate anything.
	second, err := importer.ImportDir(ctx, dir, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if second.Requests != 0 || second.Jobs != 0 || second.Logs != 0 {
		t.Fatalf("second import not idempotent: %+v", second)
	}
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM request_events`).Scan(&count)
	if count != 1 {
		t.Fatalf("request rows after rerun = %d, want 1", count)
	}
	// Even with cursors reset, dedupe keys prevent duplicates.
	third, err := importer.ImportDir(ctx, dir, ImportOptions{Fresh: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = third
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM request_events`).Scan(&count)
	if count != 1 {
		t.Fatalf("request rows after fresh rescan = %d, want 1 (dedupe keys)", count)
	}
}

func TestImporterDryRunWritesNothing(t *testing.T) {
	store := testStore(t)
	dir := writeSampleLog(t)
	importer := NewImporter(store, ImporterConfig{HashSecret: "s", Rotation: "monthly"})
	summary, err := importer.ImportDir(context.Background(), dir, ImportOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Requests != 1 || summary.Malformed != 1 {
		t.Fatalf("dry-run counts wrong: %+v", summary)
	}
	for _, tableName := range []string{"request_events", "structured_logs", "background_jobs", "import_files"} {
		var count int
		_ = store.db.QueryRow(`SELECT COUNT(*) FROM ` + tableName).Scan(&count)
		if count != 0 {
			t.Errorf("dry run wrote to %s: %d rows", tableName, count)
		}
	}
}

func TestImporterDateRange(t *testing.T) {
	store := testStore(t)
	dir := writeSampleLog(t)
	importer := NewImporter(store, ImporterConfig{HashSecret: "s", Rotation: "monthly"})
	summary, err := importer.ImportDir(context.Background(), dir, ImportOptions{FromDay: "2026-07-15", ToDay: "2026-07-16"})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Requests != 0 || summary.Skipped == 0 {
		t.Fatalf("date filter not applied: %+v", summary)
	}
}
