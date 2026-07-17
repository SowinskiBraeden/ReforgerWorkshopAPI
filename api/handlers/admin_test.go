package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// testAdminApp is a billing-enabled app with telemetry on a temp DB and the
// env admin configured.
func testAdminApp(t *testing.T) *App {
	t.Helper()
	cfg := testHandlerConfig()
	cfg.BillingEnabled = true
	cfg.BillingDBPath = filepath.Join(t.TempDir(), "billing.db")
	cfg.StripeSecretKey = "sk_test_placeholder"
	cfg.StripeWebhookSecret = "whsec_test"
	cfg.StripeDeveloperPriceID = "price_dev"
	cfg.StripeProPriceID = "price_pro"
	cfg.APIKeyHashSecret = "admin-test-secret"
	cfg.InternalMetricsEnabled = true
	cfg.InternalAdminUsername = "root"
	cfg.InternalAdminPassword = "root-password-123"
	cfg.InternalAdminSessionSecret = "session-secret"
	cfg.TelemetryEnabled = true
	cfg.TelemetryDBPath = filepath.Join(t.TempDir(), "telemetry.db")
	app := &App{Config: cfg}
	app.Initialize()
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
	return app
}

func adminDo(t *testing.T, app *App, cookie *http.Cookie, method string, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("{}")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if method != http.MethodGet {
		req.Header.Set("X-Admin-CSRF", "1")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	app.Router.ServeHTTP(rec, req)
	return rec
}

func TestAdminEndpointsRequireAuth(t *testing.T) {
	app := testAdminApp(t)
	for _, path := range []string{
		"/internal/api/overview", "/internal/api/users", "/internal/api/logs",
		"/internal/api/audit", "/internal/api/settings", "/internal/metrics",
	} {
		rec := adminDo(t, app, nil, http.MethodGet, path, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without session = %d, want 401", path, rec.Code)
		}
	}
}

func TestAdminRoleEnforcementServerSide(t *testing.T) {
	app := testAdminApp(t)
	rootCookie := adminLoginCookie(t, app)

	// Create a viewer, sign in as viewer.
	rec := adminDo(t, app, rootCookie, http.MethodPost, "/internal/api/admin-users",
		`{"username":"vw","password":"viewer-password-123","role":"viewer"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create viewer = %d: %s", rec.Code, rec.Body.String())
	}
	login := httptest.NewRequest(http.MethodPost, "/internal/login", strings.NewReader(`{"username":"vw","password":"viewer-password-123"}`))
	loginRec := httptest.NewRecorder()
	app.Router.ServeHTTP(loginRec, login)
	var viewerCookie *http.Cookie
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == internalAdminCookie {
			viewerCookie = c
		}
	}
	if viewerCookie == nil {
		t.Fatal("viewer login failed")
	}

	// Viewer can read dashboards but not user management or mutations.
	if rec := adminDo(t, app, viewerCookie, http.MethodGet, "/internal/api/overview?range=today", ""); rec.Code != http.StatusOK {
		t.Errorf("viewer overview = %d, want 200", rec.Code)
	}
	if rec := adminDo(t, app, viewerCookie, http.MethodGet, "/internal/api/users", ""); rec.Code != http.StatusForbidden {
		t.Errorf("viewer user list = %d, want 403", rec.Code)
	}
	if rec := adminDo(t, app, viewerCookie, http.MethodPost, "/internal/api/users", `{"email":"x@example.com"}`); rec.Code != http.StatusForbidden {
		t.Errorf("viewer user create = %d, want 403", rec.Code)
	}
	if rec := adminDo(t, app, viewerCookie, http.MethodPost, "/internal/api/admin-users", `{"username":"h4x","password":"whatever-password","role":"administrator"}`); rec.Code != http.StatusForbidden {
		t.Errorf("viewer privilege escalation = %d, want 403", rec.Code)
	}
}

func TestAdminMutationsRequireCSRFHeader(t *testing.T) {
	app := testAdminApp(t)
	cookie := adminLoginCookie(t, app)
	req := httptest.NewRequest(http.MethodPost, "/internal/api/users", strings.NewReader(`{"email":"a@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	app.Router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "CSRF_REQUIRED") {
		t.Fatalf("mutation without CSRF header = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestKeyLifecycleSecretShownOnceAndRevocation(t *testing.T) {
	app := testAdminApp(t)
	cookie := adminLoginCookie(t, app)

	// Create a user, then a key for them.
	rec := adminDo(t, app, cookie, http.MethodPost, "/internal/api/users", `{"email":"dev@example.com","plan":"developer"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create user = %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	activateSubscription(t, app, created.ID)

	rec = adminDo(t, app, cookie, http.MethodPost, "/internal/api/users/"+created.ID+"/keys", `{"name":"ci key"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create key = %d: %s", rec.Code, rec.Body.String())
	}
	var keyResponse struct {
		APIKey string `json:"apiKey"`
		KeyID  string `json:"keyId"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &keyResponse)
	if !strings.HasPrefix(keyResponse.APIKey, "rfm_") {
		t.Fatalf("unexpected key %q", keyResponse.APIKey)
	}

	// The raw secret must not be stored anywhere in the billing DB.
	keys, err := app.BillingStore.ListKeysAdmin(context.Background(), created.ID, 10)
	if err != nil || len(keys) != 1 {
		t.Fatalf("keys = %v, err = %v", keys, err)
	}
	rec = adminDo(t, app, cookie, http.MethodGet, "/internal/api/keys?user="+created.ID, "")
	if strings.Contains(rec.Body.String(), keyResponse.APIKey) {
		t.Fatal("raw key secret returned after creation")
	}

	// The key authenticates.
	authed := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	authed.RemoteAddr = "203.0.113.5:1000"
	authed.Header.Set("X-API-Key", keyResponse.APIKey)
	authedRec := httptest.NewRecorder()
	app.Router.ServeHTTP(authedRec, authed)
	if authedRec.Code != http.StatusOK {
		t.Fatalf("key auth = %d: %s", authedRec.Code, authedRec.Body.String())
	}

	// Temporarily disable, then re-enable.
	rec = adminDo(t, app, cookie, "PATCH", "/internal/api/keys/"+keyResponse.KeyID, `{"disabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable = %d", rec.Code)
	}
	authedRec = httptest.NewRecorder()
	app.Router.ServeHTTP(authedRec, authed.Clone(context.Background()))
	if authedRec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled key auth = %d, want 401", authedRec.Code)
	}
	rec = adminDo(t, app, cookie, "PATCH", "/internal/api/keys/"+keyResponse.KeyID, `{"disabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable = %d", rec.Code)
	}

	// Suspend the user: the key must stop working.
	rec = adminDo(t, app, cookie, "PATCH", "/internal/api/users/"+created.ID, `{"status":"suspended"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("suspend = %d: %s", rec.Code, rec.Body.String())
	}
	authedRec = httptest.NewRecorder()
	app.Router.ServeHTTP(authedRec, authed.Clone(context.Background()))
	if authedRec.Code != http.StatusForbidden {
		t.Fatalf("suspended user key auth = %d, want 403", authedRec.Code)
	}
	rec = adminDo(t, app, cookie, "PATCH", "/internal/api/users/"+created.ID, `{"status":"active"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("reactivate = %d", rec.Code)
	}

	// Revoke: the key must never authenticate again.
	rec = adminDo(t, app, cookie, http.MethodPost, "/internal/api/keys/"+keyResponse.KeyID+"/revoke",
		`{"accountId":"`+created.ID+`","reason":"test","notify":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke = %d: %s", rec.Code, rec.Body.String())
	}
	authedRec = httptest.NewRecorder()
	app.Router.ServeHTTP(authedRec, authed.Clone(context.Background()))
	if authedRec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked key auth = %d, want 401", authedRec.Code)
	}

	// Every mutation above produced an audit event.
	rec = adminDo(t, app, cookie, http.MethodGet, "/internal/api/audit?range=today&limit=100", "")
	var audit struct {
		Total int `json:"total"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &audit)
	if audit.Total < 6 {
		t.Fatalf("audit events = %d, want at least 6", audit.Total)
	}
}

// activateSubscription marks an account's subscription active so paid key
// auth passes in tests.
func activateSubscription(t *testing.T, app *App, id string) {
	t.Helper()
	account, ok, err := app.BillingStore.GetAccount(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("account %s missing", id)
	}
	account.SubscriptionStatus = "active"
	if _, err := app.BillingStore.UpsertAccount(context.Background(), account); err != nil {
		t.Fatal(err)
	}
}

func TestAdminLoginIsRateLimited(t *testing.T) {
	app := testAdminApp(t)
	var lastCode int
	for i := 0; i < 8; i++ {
		req := httptest.NewRequest(http.MethodPost, "/internal/login",
			strings.NewReader(`{"username":"root","password":"wrong-password"}`))
		req.RemoteAddr = "203.0.113.200:1000"
		rec := httptest.NewRecorder()
		app.Router.ServeHTTP(rec, req)
		lastCode = rec.Code
	}
	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("8th login attempt = %d, want 429", lastCode)
	}
	// Correct credentials are throttled too while the window lasts.
	req := httptest.NewRequest(http.MethodPost, "/internal/login",
		strings.NewReader(`{"username":"root","password":"root-password-123"}`))
	req.RemoteAddr = "203.0.113.200:1000"
	rec := httptest.NewRecorder()
	app.Router.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("throttled window login = %d, want 429", rec.Code)
	}
	// A different address is unaffected.
	req = httptest.NewRequest(http.MethodPost, "/internal/login",
		strings.NewReader(`{"username":"root","password":"root-password-123"}`))
	req.RemoteAddr = "198.51.100.9:1000"
	rec = httptest.NewRecorder()
	app.Router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("other-address login = %d, want 200", rec.Code)
	}
}
