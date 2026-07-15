package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/api"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
)

func TestAccountLoginVerifyAndManageKeys(t *testing.T) {
	app := testBillingApp(t, "https://stripe.invalid")
	account, err := app.BillingStore.UpsertAccount(context.Background(), api.Account{
		Email: "buyer@example.com", StripeCustomerID: "cus_test", StripeSubscriptionID: "sub_test",
		Plan: api.PlanDeveloper, SubscriptionStatus: "active",
	})
	if err != nil {
		t.Fatal(err)
	}

	cookie := signInWithLoginToken(t, app, account.ID)

	// Session endpoint reports the signed-in account.
	sessionReq := httptest.NewRequest(http.MethodGet, "/account/session", nil)
	sessionReq.RemoteAddr = "203.0.113.30:1234"
	sessionReq.AddCookie(cookie)
	sessionRec := httptest.NewRecorder()
	app.Router.ServeHTTP(sessionRec, sessionReq)
	if sessionRec.Code != http.StatusOK {
		t.Fatalf("session status = %d, body = %s", sessionRec.Code, sessionRec.Body.String())
	}
	assertContains(t, sessionRec.Body.String(), `"authenticated":true`)
	assertContains(t, sessionRec.Body.String(), `"email":"buyer@example.com"`)

	// Create a key with the cookie session.
	createReq := httptest.NewRequest(http.MethodPost, "/account/api-keys", strings.NewReader(`{"name":"Panel"}`))
	createReq.RemoteAddr = "203.0.113.30:1234"
	createReq.AddCookie(cookie)
	createRec := httptest.NewRecorder()
	app.Router.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create key status = %d, body = %s", createRec.Code, createRec.Body.String())
	}
	assertContains(t, createRec.Body.String(), `"api_key":"rfm_test_`)

	// List keys.
	listReq := httptest.NewRequest(http.MethodGet, "/account/api-keys", nil)
	listReq.RemoteAddr = "203.0.113.30:1234"
	listReq.AddCookie(cookie)
	listRec := httptest.NewRecorder()
	app.Router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list keys status = %d, body = %s", listRec.Code, listRec.Body.String())
	}
	assertContains(t, listRec.Body.String(), `"name":"Panel"`)
	assertContains(t, listRec.Body.String(), `"rate_limit":{"plan":"developer","limit_per_minute":300`)
	assertContains(t, listRec.Body.String(), `"shared_by":"account"`)
}

func TestCreateAPIKeyEnforcesPlanKeyLimit(t *testing.T) {
	app := testBillingApp(t, "https://stripe.invalid")
	app.Config.DeveloperMaxActiveKeys = 1
	account, err := app.BillingStore.UpsertAccount(context.Background(), api.Account{
		Email: "buyer@example.com", Plan: api.PlanDeveloper, SubscriptionStatus: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	cookie := signInWithLoginToken(t, app, account.ID)

	createKey := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/account/api-keys", strings.NewReader(`{"name":"Bot"}`))
		req.RemoteAddr = "203.0.113.40:1234"
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		app.Router.ServeHTTP(rec, req)
		return rec
	}

	if rec := createKey(); rec.Code != http.StatusOK {
		t.Fatalf("first key status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec := createKey()
	if rec.Code != http.StatusForbidden {
		t.Fatalf("second key status = %d, body = %s, want 403", rec.Code, rec.Body.String())
	}
	assertContains(t, rec.Body.String(), "API_KEY_LIMIT_REACHED")

	// Revoking frees the slot again.
	listReq := httptest.NewRequest(http.MethodGet, "/account/api-keys", nil)
	listReq.RemoteAddr = "203.0.113.40:1234"
	listReq.AddCookie(cookie)
	listRec := httptest.NewRecorder()
	app.Router.ServeHTTP(listRec, listReq)
	assertContains(t, listRec.Body.String(), `"key_limit":1`)
	keys, err := app.BillingStore.ActiveAPIKeysForAccount(context.Background(), account.ID)
	if err != nil || len(keys) != 1 {
		t.Fatalf("active keys = (%d, %v), want 1", len(keys), err)
	}
	deleteReq := httptest.NewRequest(http.MethodDelete, "/account/api-keys/"+keys[0].ID, nil)
	deleteReq.RemoteAddr = "203.0.113.40:1234"
	deleteReq.AddCookie(cookie)
	deleteRec := httptest.NewRecorder()
	app.Router.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("revoke status = %d, body = %s", deleteRec.Code, deleteRec.Body.String())
	}
	if rec := createKey(); rec.Code != http.StatusOK {
		t.Fatalf("key after revoke status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAPIKeysShareOneAccountRateBucket(t *testing.T) {
	// One request of budget for the whole account: a second key must not
	// bring a fresh rate-limit bucket.
	app := testBillingAppWithConfig(t, "https://stripe.invalid", func(cfg *config.Config) {
		cfg.DeveloperRateLimitPerMinute = 1
		cfg.AnonymousRateBurst = 1
	})
	account, err := app.BillingStore.UpsertAccount(context.Background(), api.Account{
		Email: "buyer@example.com", Plan: api.PlanDeveloper, SubscriptionStatus: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	var raws []string
	for i := 0; i < 2; i++ {
		generated, err := api.GenerateAPIKey("test", app.Config.APIKeyHashSecret)
		if err != nil {
			t.Fatal(err)
		}
		_, err = app.BillingStore.CreateAPIKey(context.Background(), api.APIKeyRecord{AccountID: account.ID, KeyHash: generated.Hash, KeyPrefix: generated.Prefix, Plan: api.PlanDeveloper, LastFour: generated.LastFour})
		if err != nil {
			t.Fatal(err)
		}
		raws = append(raws, generated.Raw)
	}

	healthWith := func(key string) int {
		req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
		req.RemoteAddr = "203.0.113.41:1234"
		req.Header.Set("X-API-Key", key)
		rec := httptest.NewRecorder()
		app.Router.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := healthWith(raws[0]); code != http.StatusOK {
		t.Fatalf("first key status = %d, want 200", code)
	}
	if code := healthWith(raws[1]); code != http.StatusTooManyRequests {
		t.Fatalf("second key status = %d, want 429 from the shared account bucket", code)
	}
}

func TestAccountAPIKeysRejectsMissingSession(t *testing.T) {
	app := testBillingApp(t, "https://stripe.invalid")
	req := httptest.NewRequest(http.MethodGet, "/account/api-keys", nil)
	req.RemoteAddr = "203.0.113.31:1234"
	rec := httptest.NewRecorder()

	app.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s, want 401", rec.Code, rec.Body.String())
	}
	assertContains(t, rec.Body.String(), "NOT_SIGNED_IN")
}

func TestLoginTokenIsSingleUse(t *testing.T) {
	app := testBillingApp(t, "https://stripe.invalid")
	account, err := app.BillingStore.UpsertAccount(context.Background(), api.Account{
		Email: "buyer@example.com", Plan: api.PlanDeveloper, SubscriptionStatus: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, hash, err := api.GenerateLoginToken(app.Config.APIKeyHashSecret)
	if err != nil {
		t.Fatal(err)
	}
	created, err := app.BillingStore.CreateLoginToken(context.Background(), account.ID, hash, time.Hour, 0)
	if err != nil || !created {
		t.Fatalf("CreateLoginToken = (%v, %v), want stored token", created, err)
	}

	first := verifyRequest(t, app, raw)
	if first.Code != http.StatusSeeOther || !strings.Contains(first.Header().Get("Location"), "login=success") {
		t.Fatalf("first verify = %d %s, want redirect to login=success", first.Code, first.Header().Get("Location"))
	}
	if len(first.Result().Cookies()) == 0 {
		t.Fatal("first verify did not set a session cookie")
	}

	second := verifyRequest(t, app, raw)
	if second.Code != http.StatusSeeOther || !strings.Contains(second.Header().Get("Location"), "login=invalid") {
		t.Fatalf("second verify = %d %s, want redirect to login=invalid", second.Code, second.Header().Get("Location"))
	}
}

func TestAccountLoginReturnsGenericResponse(t *testing.T) {
	app := testBillingApp(t, "https://stripe.invalid")
	req := httptest.NewRequest(http.MethodPost, "/account/login", strings.NewReader(`{"email":"nobody@example.com"}`))
	req.RemoteAddr = "203.0.113.32:1234"
	rec := httptest.NewRecorder()

	app.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	assertContains(t, rec.Body.String(), `"sent":true`)
}

func TestLoginTokenCooldownBlocksRepeatIssuance(t *testing.T) {
	app := testBillingApp(t, "https://stripe.invalid")
	account, err := app.BillingStore.UpsertAccount(context.Background(), api.Account{
		Email: "buyer@example.com", Plan: api.PlanDeveloper, SubscriptionStatus: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, firstHash, _ := api.GenerateLoginToken(app.Config.APIKeyHashSecret)
	created, err := app.BillingStore.CreateLoginToken(context.Background(), account.ID, firstHash, time.Hour, time.Minute)
	if err != nil || !created {
		t.Fatalf("first CreateLoginToken = (%v, %v), want stored token", created, err)
	}
	_, secondHash, _ := api.GenerateLoginToken(app.Config.APIKeyHashSecret)
	created, err = app.BillingStore.CreateLoginToken(context.Background(), account.ID, secondHash, time.Hour, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("second token within cooldown should not be stored")
	}
}

func signInWithLoginToken(t *testing.T, app *App, accountID string) *http.Cookie {
	t.Helper()
	raw, hash, err := api.GenerateLoginToken(app.Config.APIKeyHashSecret)
	if err != nil {
		t.Fatal(err)
	}
	created, err := app.BillingStore.CreateLoginToken(context.Background(), accountID, hash, time.Hour, 0)
	if err != nil || !created {
		t.Fatalf("CreateLoginToken = (%v, %v), want stored token", created, err)
	}
	rec := verifyRequest(t, app, raw)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("verify status = %d, want 303", rec.Code)
	}
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == "rfm_account_session" && cookie.Value != "" {
			return cookie
		}
	}
	t.Fatal("verify did not set the account session cookie")
	return nil
}

func verifyRequest(t *testing.T, app *App, rawToken string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/account/verify?token="+rawToken, nil)
	req.RemoteAddr = "203.0.113.33:1234"
	rec := httptest.NewRecorder()
	app.Router.ServeHTTP(rec, req)
	return rec
}
