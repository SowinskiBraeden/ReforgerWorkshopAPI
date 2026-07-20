package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/telemetry"
	"github.com/gorilla/mux"
)

// testPipeline builds the full outer wrapper (telemetry middleware + rate
// limiting Wrap) around a mux router the way the app does, returning both the
// handler and the backing store for assertions.
func testPipeline(t *testing.T, register func(router *mux.Router, chain *MiddlewareChain)) (http.Handler, *telemetry.Store) {
	t.Helper()
	cfg := testConfig()
	cfg.APIKeyHashSecret = "pipeline-secret"
	cfg.AnonIDRotation = "monthly"
	// Generous limits so rate limiting only interferes where a test opts in.
	cfg.AnonymousRateLimitPerMinute = 600
	cfg.AnonymousRateBurst = 100
	store, err := telemetry.Open(filepath.Join(t.TempDir(), "telemetry.db"))
	if err != nil {
		t.Fatal(err)
	}
	recorder := telemetry.NewRecorder(store)
	t.Cleanup(func() {
		_ = recorder.Close()
		_ = store.Close()
	})
	router := mux.NewRouter()
	chain := NewMiddleware(cfg)
	register(router, chain)
	router.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	handler := NewTelemetryMiddleware(cfg, recorder, router, "test").Handler()
	return handler, store
}

func countEvents(t *testing.T, store *telemetry.Store, deadline time.Duration, want int) int {
	t.Helper()
	end := time.Now().Add(deadline)
	count := 0
	for time.Now().Before(end) {
		_ = store.DB().QueryRow(`SELECT COUNT(*) FROM request_events`).Scan(&count)
		if count >= want {
			return count
		}
		time.Sleep(50 * time.Millisecond)
	}
	return count
}

func TestOneRequestProducesExactlyOneEvent(t *testing.T) {
	handler, store := testPipeline(t, func(router *mux.Router, chain *MiddlewareChain) {
		router.Handle("/v1/mod/{id}", chain.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Simulate cache activity on the same request: only headers, no
			// extra telemetry events.
			w.Header().Set("X-Cache", "HIT")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))).Methods("GET")
	})
	for i := 0; i < 3; i++ {
		r := httptest.NewRequest(http.MethodGet, "/v1/mod/ABC123DEF0", nil)
		r.RemoteAddr = "203.0.113.10:1234"
		handler.ServeHTTP(httptest.NewRecorder(), r)
	}
	if got := countEvents(t, store, 5*time.Second, 3); got != 3 {
		t.Fatalf("request events = %d, want exactly 3", got)
	}
	var cacheHits int
	_ = store.DB().QueryRow(`SELECT COUNT(*) FROM request_events WHERE cache_status='HIT'`).Scan(&cacheHits)
	if cacheHits != 3 {
		t.Fatalf("cache annotation missing: %d", cacheHits)
	}
	var route string
	var modID string
	_ = store.DB().QueryRow(`SELECT route_template, mod_id FROM request_events LIMIT 1`).Scan(&route, &modID)
	if route != "/v1/mod/{id}" {
		t.Errorf("route template = %q", route)
	}
	if modID != "ABC123DEF0" {
		t.Errorf("mod id = %q", modID)
	}
}

func TestUnmatchedRequestsAreCountedOnce(t *testing.T) {
	handler, store := testPipeline(t, func(router *mux.Router, chain *MiddlewareChain) {})
	r := httptest.NewRequest(http.MethodGet, "/no-such-page", nil)
	r.RemoteAddr = "203.0.113.10:1234"
	handler.ServeHTTP(httptest.NewRecorder(), r)
	if got := countEvents(t, store, 5*time.Second, 1); got != 1 {
		t.Fatalf("events for 404 = %d, want 1", got)
	}
	var status int
	var route string
	_ = store.DB().QueryRow(`SELECT status, route_template FROM request_events`).Scan(&status, &route)
	if status != http.StatusNotFound || route != "(unmatched)" {
		t.Fatalf("404 recorded as status=%d route=%q", status, route)
	}
}

func TestInternalRequestsAreNotRecorded(t *testing.T) {
	handler, store := testPipeline(t, func(router *mux.Router, chain *MiddlewareChain) {
		router.HandleFunc("/internal/api/overview", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}).Methods("GET")
		router.Handle("/v1/mods", chain.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))).Methods("GET")
	})

	internal := httptest.NewRequest(http.MethodGet, "/internal/api/overview?range=today", nil)
	internal.RemoteAddr = "203.0.113.10:1234"
	handler.ServeHTTP(httptest.NewRecorder(), internal)

	public := httptest.NewRequest(http.MethodGet, "/v1/mods", nil)
	public.RemoteAddr = "203.0.113.10:1234"
	handler.ServeHTTP(httptest.NewRecorder(), public)

	if got := countEvents(t, store, 5*time.Second, 1); got != 1 {
		t.Fatalf("request events = %d, want only the public request", got)
	}
	var path string
	_ = store.DB().QueryRow(`SELECT request_path FROM request_events`).Scan(&path)
	if strings.HasPrefix(path, "/internal") {
		t.Fatalf("internal request was recorded: %q", path)
	}
}

func TestStaticRequestsAreNotRecorded(t *testing.T) {
	handler, store := testPipeline(t, func(router *mux.Router, chain *MiddlewareChain) {
		router.PathPrefix("/static/").Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("asset"))
		}))
		router.Handle("/v1/mods", chain.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))).Methods("GET")
	})

	static := httptest.NewRequest(http.MethodGet, "/static/index.css", nil)
	static.RemoteAddr = "203.0.113.10:1234"
	handler.ServeHTTP(httptest.NewRecorder(), static)

	public := httptest.NewRequest(http.MethodGet, "/v1/mods", nil)
	public.RemoteAddr = "203.0.113.10:1234"
	handler.ServeHTTP(httptest.NewRecorder(), public)

	if got := countEvents(t, store, 5*time.Second, 1); got != 1 {
		t.Fatalf("request events = %d, want only the public request", got)
	}
	var path string
	_ = store.DB().QueryRow(`SELECT request_path FROM request_events`).Scan(&path)
	if strings.HasPrefix(path, "/static") {
		t.Fatalf("static request was recorded: %q", path)
	}
}

func TestPanicIsRecoveredAndRecorded(t *testing.T) {
	handler, store := testPipeline(t, func(router *mux.Router, chain *MiddlewareChain) {
		router.HandleFunc("/v1/boom", func(w http.ResponseWriter, r *http.Request) {
			panic("kaboom")
		})
	})
	recorder := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/boom", nil)
	r.RemoteAddr = "203.0.113.10:1234"
	handler.ServeHTTP(recorder, r)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("panic status = %d, want 500", recorder.Code)
	}
	if got := countEvents(t, store, 5*time.Second, 1); got != 1 {
		t.Fatalf("panic events = %d, want 1", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	errorCount := 0
	for time.Now().Before(deadline) && errorCount == 0 {
		_ = store.DB().QueryRow(`SELECT COUNT(*) FROM request_errors WHERE severity='fatal'`).Scan(&errorCount)
		time.Sleep(50 * time.Millisecond)
	}
	if errorCount != 1 {
		t.Fatalf("panic error events = %d, want 1", errorCount)
	}
}

func TestNoRawIPPersistedFromLiveTraffic(t *testing.T) {
	handler, store := testPipeline(t, func(router *mux.Router, chain *MiddlewareChain) {
		router.Handle("/v1/health", chain.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))).Methods("GET")
	})
	r := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	r.RemoteAddr = "198.51.100.77:4444"
	handler.ServeHTTP(httptest.NewRecorder(), r)
	if got := countEvents(t, store, 5*time.Second, 1); got != 1 {
		t.Fatalf("events = %d", got)
	}
	rows, err := store.DB().Query(`SELECT name FROM sqlite_master WHERE type='table'`)
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
		dataRows, err := store.DB().Query(`SELECT * FROM ` + tableName)
		if err != nil {
			continue
		}
		columns, _ := dataRows.Columns()
		for dataRows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			_ = dataRows.Scan(pointers...)
			for i, value := range values {
				if text, ok := value.(string); ok && strings.Contains(text, "198.51.100.77") {
					t.Errorf("raw IP stored in %s.%s: %s", tableName, columns[i], text)
				}
			}
		}
		dataRows.Close()
	}
	var networkID string
	_ = store.DB().QueryRow(`SELECT network_id FROM request_events`).Scan(&networkID)
	if networkID == "" {
		t.Error("network id was not derived")
	}
}

func TestRateLimitedRequestsAreFlagged(t *testing.T) {
	handler, store := testPipeline(t, func(router *mux.Router, chain *MiddlewareChain) {
		strict := NewMiddleware(testConfig()) // 1/min, burst 1
		router.Handle("/v1/mods", strict.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))).Methods("GET")
	})
	// The strict chain allows 1/min with burst 1: the second request is limited.
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest(http.MethodGet, "/v1/mods", nil)
		r.RemoteAddr = "203.0.113.10:1234"
		handler.ServeHTTP(httptest.NewRecorder(), r)
	}
	if got := countEvents(t, store, 5*time.Second, 2); got != 2 {
		t.Fatalf("events = %d, want 2 (limited requests are still counted once)", got)
	}
	var limited int
	var bucket string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && limited == 0 {
		_ = store.DB().QueryRow(`SELECT COUNT(*), COALESCE(MAX(rate_bucket),'') FROM request_events WHERE rate_limited=1`).Scan(&limited, &bucket)
		time.Sleep(50 * time.Millisecond)
	}
	if limited != 1 {
		t.Fatalf("rate-limited events = %d, want 1", limited)
	}
	if bucket != "anonymous" {
		t.Fatalf("stored bucket = %q, must not embed the IP", bucket)
	}
}
