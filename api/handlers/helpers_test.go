package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
)

// testHandlerConfig is the shared baseline for handler tests: everything
// external (billing, index, telemetry persistence) is off unless a test
// enables it, and rate limits are generous enough not to interfere.
func testHandlerConfig() config.Config {
	return config.Config{
		FullURL:                     "https://api.reforgermods.test",
		APIBaseURL:                  "https://api.reforgermods.test",
		PublicBaseURL:               "https://reforgermods.test",
		AnonymousRateLimitPerMinute: 600,
		AnonymousRateBurst:          100,
		DeveloperRateLimitPerMinute: 300,
		ProRateLimitPerMinute:       1200,
		InternalRateLimitPerMinute:  5000,
		RateLimitClientTTL:          time.Minute,
		MaxBodyBytes:                1 << 20,
		MaxQueryLength:              2048,
		CacheMaxEntries:             100,
		ModCacheTTL:                 time.Minute,
		ModCacheStale:               time.Minute,
		ListCacheTTL:                time.Minute,
		ListCacheStale:              time.Minute,
		NotFoundCacheTTL:            time.Minute,
		CacheRefreshTimeout:         time.Second,
		CacheRefreshParallel:        2,
		CacheRefreshQueueSize:       16,
		CacheRefreshJobRetention:    time.Minute,
		CacheRefreshRetryAfter:      time.Second,
		AppEnv:                      "sandbox",
		MetricsTimezone:             "UTC",
		MetricsInternalCIDRs:        "127.0.0.1/32,::1/128",
		TelemetryEnabled:            false,
		AnonIDRotation:              "monthly",
		TelemetrySlowRequestMs:      1500,
	}
}

// adminLoginCookie signs in with the app's configured env admin account and
// returns the session cookie.
func adminLoginCookie(t *testing.T, app *App) *http.Cookie {
	t.Helper()
	body := `{"username":"` + app.Config.InternalAdminUsername + `","password":"` + app.Config.InternalAdminPassword + `"}`
	req := httptest.NewRequest(http.MethodPost, "/internal/login", strings.NewReader(body))
	rec := httptest.NewRecorder()
	app.Router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin login status = %d, body = %s", rec.Code, rec.Body.String())
	}
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == internalAdminCookie {
			return cookie
		}
	}
	t.Fatal("admin session cookie was not set")
	return nil
}
