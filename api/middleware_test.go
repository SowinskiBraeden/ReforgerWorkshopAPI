package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
)

func testConfig() config.Config {
	return config.Config{
		AnonymousRateLimitPerMinute: 1,
		AnonymousRateBurst:          1,
		RateLimitClientTTL:          time.Minute,
		MaxBodyBytes:                1024,
		MaxQueryLength:              256,
		CacheMaxEntries:             10,
		CacheRefreshTimeout:         time.Second,
		CacheRefreshParallel:        2,
	}
}

func TestClientIPIgnoresSpoofedForwardedForFromUntrustedRemote(t *testing.T) {
	cfg := testConfig()
	m := NewMiddleware(cfg)

	r := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	r.RemoteAddr = "203.0.113.10:1234"
	r.Header.Set("X-Forwarded-For", "198.51.100.99")

	if got := m.ClientIP(r); got != "203.0.113.10" {
		t.Fatalf("ClientIP() = %q, want direct remote address", got)
	}
}

func TestClientIPUsesForwardedForFromTrustedProxy(t *testing.T) {
	cfg := testConfig()
	cfg.TrustedProxyCIDRs = "10.0.0.0/8"
	m := NewMiddleware(cfg)

	r := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	r.RemoteAddr = "10.1.2.3:1234"
	r.Header.Set("X-Forwarded-For", "198.51.100.99, 10.1.2.3")

	if got := m.ClientIP(r); got != "198.51.100.99" {
		t.Fatalf("ClientIP() = %q, want forwarded client", got)
	}
}

func TestCountryCodeUsesKnownHeadersFromTrustedProxy(t *testing.T) {
	cfg := testConfig()
	cfg.TrustedProxyCIDRs = "10.0.0.0/8"
	m := NewMiddleware(cfg)

	r := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	r.RemoteAddr = "10.1.2.3:1234"
	r.Header.Set("CF-IPCountry", "ca")

	if got := m.CountryCode(r); got != "CA" {
		t.Fatalf("CountryCode() = %q, want CA", got)
	}

	r.Header.Set("CF-IPCountry", "XX")
	r.Header.Set("X-Vercel-IP-Country", "US")
	if got := m.CountryCode(r); got != "US" {
		t.Fatalf("CountryCode() fallback = %q, want US", got)
	}
}

func TestCountryCodeIgnoresSpoofedHeadersFromUntrustedRemote(t *testing.T) {
	cfg := testConfig()
	m := NewMiddleware(cfg)

	r := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	r.RemoteAddr = "203.0.113.10:1234"
	r.Header.Set("CF-IPCountry", "CA")

	if got := m.CountryCode(r); got != "ZZ" {
		t.Fatalf("CountryCode() = %q, want unknown for untrusted remote", got)
	}
}

func TestMiddlewareReturnsRequestIDHeader(t *testing.T) {
	cfg := testConfig()
	m := NewMiddleware(cfg)
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	r.RemoteAddr = "203.0.113.10:1234"
	r.Header.Set("X-Request-Id", "test-request-id")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, r)

	if got := recorder.Header().Get("X-Request-Id"); got != "test-request-id" {
		t.Fatalf("X-Request-Id = %q, want test-request-id", got)
	}
}

func TestMiddlewareGeneratesRequestIDHeader(t *testing.T) {
	cfg := testConfig()
	m := NewMiddleware(cfg)
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	r.RemoteAddr = "203.0.113.10:1234"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, r)

	if got := recorder.Header().Get("X-Request-Id"); got == "" {
		t.Fatal("X-Request-Id header was not set")
	}
}

func TestRateLimitRejectsSpoofByUntrustedForwardedFor(t *testing.T) {
	cfg := testConfig()
	m := NewMiddleware(cfg)
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	first := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	first.RemoteAddr = "203.0.113.10:1234"
	first.Header.Set("X-Forwarded-For", "198.51.100.1")
	firstRecorder := httptest.NewRecorder()
	handler.ServeHTTP(firstRecorder, first)
	if firstRecorder.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", firstRecorder.Code)
	}

	second := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	second.RemoteAddr = "203.0.113.10:5678"
	second.Header.Set("X-Forwarded-For", "198.51.100.2")
	secondRecorder := httptest.NewRecorder()
	handler.ServeHTTP(secondRecorder, second)
	if secondRecorder.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", secondRecorder.Code)
	}
	if secondRecorder.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After header was not set")
	}
	if secondRecorder.Header().Get("RateLimit-Limit") != "1" {
		t.Fatalf("RateLimit-Limit = %q, want 1", secondRecorder.Header().Get("RateLimit-Limit"))
	}

	var body map[string]map[string]string
	if err := json.Unmarshal(secondRecorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode error envelope: %v", err)
	}
	if body["error"]["code"] != "RATE_LIMITED" {
		t.Fatalf("error code = %q, want RATE_LIMITED", body["error"]["code"])
	}
}

func TestQueryLengthRejectedWithErrorEnvelope(t *testing.T) {
	cfg := testConfig()
	cfg.MaxQueryLength = 3
	m := NewMiddleware(cfg)
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/v1/mods?search=abcdef", nil)
	r.RemoteAddr = "203.0.113.20:1234"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, r)

	if recorder.Code != http.StatusRequestURITooLong {
		t.Fatalf("status = %d, want 414", recorder.Code)
	}
	var body map[string]map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode error envelope: %v", err)
	}
	if body["error"]["code"] != "QUERY_TOO_LONG" {
		t.Fatalf("error code = %q, want QUERY_TOO_LONG", body["error"]["code"])
	}
}
