package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/api"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
	"github.com/gorilla/mux"
)

func TestInternalMetricsRequiresTokenWhenConfigured(t *testing.T) {
	app := App{Config: testHandlerConfig()}
	app.Config.InternalMetricsEnabled = true
	app.Config.InternalMetricsToken = "secret-token"
	app.Metrics = api.NewMetrics()
	app.Cache = api.NewResponseCache(app.Config, app.Metrics)

	r := httptest.NewRequest(http.MethodGet, "/internal/metrics", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	app.internalMetricsHandler(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status without token = %d, want 401", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Internal Metrics") {
		t.Fatalf("WWW-Authenticate = %q, want Internal Metrics realm", got)
	}

	r = httptest.NewRequest(http.MethodGet, "/internal/metrics", nil)
	r.RemoteAddr = "203.0.113.10:1234"
	r.Header.Set("Authorization", "Bearer secret-token")
	w = httptest.NewRecorder()
	app.internalMetricsHandler(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status with token = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestInternalMetricsRejectsUnsetToken(t *testing.T) {
	app := App{Config: testHandlerConfig()}
	app.Config.InternalMetricsEnabled = true
	app.Metrics = api.NewMetrics()
	app.Cache = api.NewResponseCache(app.Config, app.Metrics)

	r := httptest.NewRequest(http.MethodGet, "/internal/metrics", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	app.internalMetricsHandler(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unset token status = %d, want 401", w.Code)
	}
}

func TestInternalMetricsPanelServesShellWithBasicAuth(t *testing.T) {
	app := App{Config: testHandlerConfig()}
	app.Config.InternalMetricsEnabled = true
	app.Config.InternalMetricsToken = "secret-token"

	r := httptest.NewRequest(http.MethodGet, "/internal/metrics/panel", nil)
	r.RemoteAddr = "203.0.113.10:1234"
	r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("admin:secret-token")))
	w := httptest.NewRecorder()
	app.internalMetricsPanelHandler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("panel status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Internal Metrics") {
		t.Fatal("panel body did not include title")
	}
	if !strings.Contains(w.Body.String(), `rel="icon"`) {
		t.Fatal("panel body did not include favicon link")
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestRefreshJobHandlerReturnsSafeJobStatus(t *testing.T) {
	app := App{Config: testHandlerConfig()}
	app.Metrics = api.NewMetrics()
	app.Cache = api.NewResponseCache(app.Config, app.Metrics)
	release := make(chan struct{})

	req := httptest.NewRequest(http.MethodGet, "/v1/mods", nil)
	recorder := httptest.NewRecorder()
	app.Cache.Serve(recorder, req, "v1:mods:job-route", time.Minute, time.Minute, func(ctx context.Context) api.CachedResponse {
		select {
		case <-release:
			return api.CachedResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":true}`)}
		case <-ctx.Done():
			return api.CachedResponse{Err: ctx.Err()}
		}
	})
	defer close(release)

	var accepted api.RefreshJobSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("failed to decode accepted job: %v", err)
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/refresh/jobs/"+accepted.ID, nil)
	r = mux.SetURLVars(r, map[string]string{"id": accepted.ID})
	w := httptest.NewRecorder()
	app.RefreshJobHandler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("job status code = %d, want 200", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("running/queued job response did not include Retry-After")
	}
	var body api.RefreshJobSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode job response: %v", err)
	}
	if body.ID != accepted.ID || body.ResourceURL != "/v1/mods" {
		t.Fatalf("job body = %+v, want same id and resource URL", body)
	}
}

func testHandlerConfig() config.Config {
	return config.Config{
		AnonymousRateLimitPerMinute: 60,
		AnonymousRateBurst:          20,
		RateLimitClientTTL:          time.Minute,
		MaxBodyBytes:                1024,
		MaxQueryLength:              256,
		CacheMaxEntries:             10,
		CacheRefreshTimeout:         time.Second,
		CacheRefreshParallel:        2,
	}
}
