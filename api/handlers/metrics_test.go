package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/api"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
	"github.com/gorilla/mux"
)

func TestInternalMetricsRequiresAdminLogin(t *testing.T) {
	app := App{Config: testHandlerConfig()}
	app.Config.InternalMetricsEnabled = true
	app.Config.InternalAdminUsername = "admin"
	app.Config.InternalAdminPassword = "secret-password"
	app.Config.InternalAdminSessionSecret = "session-secret"
	app.Metrics = api.NewMetrics()
	app.Cache = api.NewResponseCache(app.Config, app.Metrics)

	r := httptest.NewRequest(http.MethodGet, "/internal/metrics", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	app.internalMetricsHandler(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status without login = %d, want 401", w.Code)
	}

	r = httptest.NewRequest(http.MethodGet, "/internal/metrics", nil)
	r.RemoteAddr = "203.0.113.10:1234"
	r.AddCookie(adminLoginCookie(t, &app))
	w = httptest.NewRecorder()
	app.internalMetricsHandler(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status with admin cookie = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestInternalMetricsRejectsUnsetAdminCredentials(t *testing.T) {
	app := App{Config: testHandlerConfig()}
	app.Config.InternalMetricsEnabled = true
	app.Metrics = api.NewMetrics()
	app.Cache = api.NewResponseCache(app.Config, app.Metrics)

	r := httptest.NewRequest(http.MethodGet, "/internal/metrics", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	app.internalMetricsHandler(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unset admin status = %d, want 401", w.Code)
	}
}

func TestInternalMetricsImportLogsRequiresAdminAndImports(t *testing.T) {
	dir := t.TempDir()
	ts := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	log := `{"ts":"` + ts + `","msg":"request completed","requestId":"old","clientIP":"203.0.113.10","countryCode":"US","method":"GET","path":"/v1/mods","status":200,"latencyMs":25,"userAgent":"old-client"}
`
	if err := os.WriteFile(filepath.Join(dir, "2026-07-08.log"), []byte(log), 0600); err != nil {
		t.Fatalf("write log: %v", err)
	}

	app := App{Config: testHandlerConfig()}
	app.Config.InternalMetricsEnabled = true
	app.Config.InternalAdminUsername = "admin"
	app.Config.InternalAdminPassword = "secret-password"
	app.Config.InternalAdminSessionSecret = "session-secret"
	app.Config.LogDir = dir
	app.Metrics = api.NewMetrics()
	app.Cache = api.NewResponseCache(app.Config, app.Metrics)

	r := httptest.NewRequest(http.MethodPost, "/internal/metrics/import-logs", nil)
	w := httptest.NewRecorder()
	app.internalMetricsImportLogsHandler(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status without login = %d, want 401", w.Code)
	}

	r = httptest.NewRequest(http.MethodPost, "/internal/metrics/import-logs", nil)
	r.AddCookie(adminLoginCookie(t, &app))
	w = httptest.NewRecorder()
	app.internalMetricsImportLogsHandler(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status with admin cookie = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Imported      int    `json:"imported"`
		TotalRequests uint64 `json:"totalRequests"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode import response: %v", err)
	}
	if body.Imported != 1 || body.TotalRequests != 1 {
		t.Fatalf("import response = %+v, want imported/total 1", body)
	}
}

func TestInternalMetricsPanelServesLoginThenShell(t *testing.T) {
	app := App{Config: testHandlerConfig()}
	app.Config.InternalMetricsEnabled = true
	app.Config.InternalAdminUsername = "admin"
	app.Config.InternalAdminPassword = "secret-password"
	app.Config.InternalAdminSessionSecret = "session-secret"

	r := httptest.NewRequest(http.MethodGet, "/internal/metrics/panel", nil)
	r.RemoteAddr = "203.0.113.10:1234"
	w := httptest.NewRecorder()
	app.internalMetricsPanelHandler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("login page status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Admin Login") {
		t.Fatal("unauthenticated panel did not serve login page")
	}

	r = httptest.NewRequest(http.MethodGet, "/internal/metrics/panel", nil)
	r.RemoteAddr = "203.0.113.10:1234"
	r.AddCookie(adminLoginCookie(t, &app))
	w = httptest.NewRecorder()
	app.internalMetricsPanelHandler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("panel status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Admin Panel") {
		t.Fatal("panel body did not include title")
	}
	if !strings.Contains(w.Body.String(), `rel="icon"`) {
		t.Fatal("panel body did not include favicon link")
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func adminLoginCookie(t *testing.T, app *App) *http.Cookie {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/internal/login", strings.NewReader(`{"username":"admin","password":"secret-password"}`))
	w := httptest.NewRecorder()
	app.internalLoginHandler(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("admin login status = %d, body = %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("admin login did not set a cookie")
	}
	return cookies[0]
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
