package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/api"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
)

func TestCreateCheckoutSessionCreatesProductAndPersistsSession(t *testing.T) {
	var checkoutCalled bool
	stripe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Basic ") {
			t.Fatalf("Authorization header = %q, want Basic auth", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		switch r.URL.Path {
		case "/v1/checkout/sessions":
			checkoutCalled = true
			assertFormValue(t, r.Form, "line_items[0][price]", "price_dev")
			assertFormValue(t, r.Form, "line_items[0][quantity]", "1")
			assertFormValue(t, r.Form, "mode", "subscription")
			assertFormValue(t, r.Form, "success_url", "https://reforgermods.test/account/api-keys/?checkout=success&session_id={CHECKOUT_SESSION_ID}")
			assertFormValue(t, r.Form, "cancel_url", "https://reforgermods.test/pricing")
			assertFormValue(t, r.Form, "customer_email", "buyer@example.com")
			assertFormValue(t, r.Form, "metadata[plan]", "developer")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"cs_test","url":"https://checkout.stripe.test/session"}`))
		default:
			t.Fatalf("unexpected Stripe path %s", r.URL.Path)
		}
	}))
	defer stripe.Close()

	app := testBillingApp(t, stripe.URL)
	req := httptest.NewRequest(http.MethodPost, "/billing/checkout", strings.NewReader(`{"plan":"developer","email":"buyer@example.com"}`))
	req.RemoteAddr = "203.0.113.10:1234"
	rec := httptest.NewRecorder()

	app.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	assertContains(t, rec.Body.String(), `"url":"https://checkout.stripe.test/session"`)
	if !checkoutCalled {
		t.Fatal("checkout endpoint was not called")
	}
}

func TestBillingCheckoutRejectsInvalidPlan(t *testing.T) {
	stripe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("Stripe should not be called for invalid plans")
	}))
	defer stripe.Close()

	app := testBillingApp(t, stripe.URL)
	req := httptest.NewRequest(http.MethodPost, "/billing/checkout", strings.NewReader(`{"plan":"enterprise","email":"buyer@example.com"}`))
	req.RemoteAddr = "203.0.113.10:1234"
	rec := httptest.NewRecorder()

	app.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s, want 400", rec.Code, rec.Body.String())
	}
}

func TestBillingCheckoutRejectsInvalidEmail(t *testing.T) {
	stripe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("Stripe should not be called with invalid email")
	}))
	defer stripe.Close()

	app := testBillingApp(t, stripe.URL)
	req := httptest.NewRequest(http.MethodPost, "/billing/checkout", strings.NewReader(`{"plan":"developer","email":"invalid"}`))
	req.RemoteAddr = "203.0.113.10:1234"
	rec := httptest.NewRecorder()

	app.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s, want 400", rec.Code, rec.Body.String())
	}
	assertContains(t, rec.Body.String(), "INVALID_EMAIL")
}

func TestBillingCheckoutRejectsMissingEmail(t *testing.T) {
	stripe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("Stripe should not be called without an email")
	}))
	defer stripe.Close()

	app := testBillingApp(t, stripe.URL)
	req := httptest.NewRequest(http.MethodPost, "/billing/checkout", strings.NewReader(`{"plan":"developer"}`))
	req.RemoteAddr = "203.0.113.10:1234"
	rec := httptest.NewRecorder()

	app.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s, want 400", rec.Code, rec.Body.String())
	}
	assertContains(t, rec.Body.String(), "INVALID_EMAIL")
}

func TestCheckoutWebhookRecoversEmailFromStripeCustomer(t *testing.T) {
	var app *App
	stripe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/customers/cus_paid":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"cus_paid","email":"paid@example.com"}`))
		default:
			t.Fatalf("unexpected Stripe path %s", r.URL.Path)
		}
	}))
	defer stripe.Close()
	app = testBillingApp(t, stripe.URL)

	payload := `{"id":"evt_checkout_email","type":"checkout.session.completed","data":{"object":{"id":"cs_paid","customer":"cus_paid","subscription":"sub_paid","status":"complete","client_reference_id":"acct_pending","metadata":{"plan":"developer"}}}}`
	if _, err := app.BillingStore.UpsertAccount(context.Background(), api.Account{ID: "acct_pending", Plan: api.PlanFree, SubscriptionStatus: api.SubscriptionStatusNone}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/stripe/webhook", strings.NewReader(payload))
	req.Header.Set("Stripe-Signature", stripeSignatureHeader(t, payload, app.Config.StripeWebhookSecret, time.Now()))
	rec := httptest.NewRecorder()

	app.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	account, ok, err := app.BillingStore.GetAccountByStripeCustomer(context.Background(), "cus_paid")
	if err != nil || !ok {
		t.Fatalf("account lookup = (%+v, %v, %v), want account", account, ok, err)
	}
	if account.Email != "paid@example.com" {
		t.Fatalf("account email = %q, want paid@example.com", account.Email)
	}
}

func TestStripeWebhookRecordsCompletedCheckoutSession(t *testing.T) {
	app := testBillingApp(t, "https://stripe.invalid")
	payload := `{"id":"evt_test","type":"customer.subscription.deleted","data":{"object":{"id":"sub_test","customer":"cus_test","status":"canceled","metadata":{"plan":"developer"},"items":{"data":[{"price":{"id":"price_dev"}}]}}}}`
	account, err := app.BillingStore.UpsertAccount(context.Background(), api.Account{Email: "buyer@example.com", StripeCustomerID: "cus_test", StripeSubscriptionID: "sub_test", Plan: api.PlanDeveloper, SubscriptionStatus: "active"})
	if err != nil {
		t.Fatal(err)
	}
	generated, _ := api.GenerateAPIKey("test", app.Config.APIKeyHashSecret)
	_, err = app.BillingStore.CreateAPIKey(context.Background(), api.APIKeyRecord{AccountID: account.ID, KeyHash: generated.Hash, KeyPrefix: generated.Prefix, Plan: api.PlanDeveloper, LastFour: generated.LastFour})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/stripe/webhook", strings.NewReader(payload))
	req.Header.Set("Stripe-Signature", stripeSignatureHeader(t, payload, app.Config.StripeWebhookSecret, time.Now()))
	rec := httptest.NewRecorder()

	app.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	updated, ok, err := app.BillingStore.GetAccountByStripeCustomer(req.Context(), "cus_test")
	if err != nil || !ok {
		t.Fatalf("account lookup = (%+v, %v, %v), want account", updated, ok, err)
	}
	if updated.Plan != api.PlanFree || updated.SubscriptionStatus != "canceled" {
		t.Fatalf("account = %+v, want free/canceled", updated)
	}
}

func TestInvoicePaymentFailedKeepsPlanButBlocksAccessUntilPaid(t *testing.T) {
	app := testBillingApp(t, "https://stripe.invalid")
	account, err := app.BillingStore.UpsertAccount(context.Background(), api.Account{
		Email:                "buyer@example.com",
		StripeCustomerID:     "cus_test",
		StripeSubscriptionID: "sub_test",
		Plan:                 api.PlanDeveloper,
		SubscriptionStatus:   "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	generated, err := api.GenerateAPIKey("test", app.Config.APIKeyHashSecret)
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.BillingStore.CreateAPIKey(context.Background(), api.APIKeyRecord{
		AccountID: account.ID,
		KeyHash:   generated.Hash,
		KeyPrefix: generated.Prefix,
		Plan:      api.PlanDeveloper,
		LastFour:  generated.LastFour,
	})
	if err != nil {
		t.Fatal(err)
	}

	failedPayload := `{"id":"evt_invoice_failed","type":"invoice.payment_failed","data":{"object":{"customer":"cus_test","subscription":"sub_test"}}}`
	failedReq := httptest.NewRequest(http.MethodPost, "/stripe/webhook", strings.NewReader(failedPayload))
	failedReq.Header.Set("Stripe-Signature", stripeSignatureHeader(t, failedPayload, app.Config.StripeWebhookSecret, time.Now()))
	failedRec := httptest.NewRecorder()
	app.Router.ServeHTTP(failedRec, failedReq)
	if failedRec.Code != http.StatusOK {
		t.Fatalf("failed invoice webhook status = %d, body = %s", failedRec.Code, failedRec.Body.String())
	}
	updated, ok, err := app.BillingStore.GetAccountBySubscription(context.Background(), "sub_test")
	if err != nil || !ok {
		t.Fatalf("account lookup = (%+v, %v, %v), want account", updated, ok, err)
	}
	if updated.Plan != api.PlanDeveloper || updated.SubscriptionStatus != "past_due" {
		t.Fatalf("account after payment failure = %+v, want developer/past_due", updated)
	}

	blockedReq := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	blockedReq.RemoteAddr = "203.0.113.25:1234"
	blockedReq.Header.Set("X-API-Key", generated.Raw)
	blockedRec := httptest.NewRecorder()
	app.Router.ServeHTTP(blockedRec, blockedReq)
	if blockedRec.Code != http.StatusForbidden {
		t.Fatalf("blocked status = %d, body = %s, want 403", blockedRec.Code, blockedRec.Body.String())
	}

	paidPayload := `{"id":"evt_invoice_paid","type":"invoice.paid","data":{"object":{"customer":"cus_test","subscription":"sub_test"}}}`
	paidReq := httptest.NewRequest(http.MethodPost, "/stripe/webhook", strings.NewReader(paidPayload))
	paidReq.Header.Set("Stripe-Signature", stripeSignatureHeader(t, paidPayload, app.Config.StripeWebhookSecret, time.Now()))
	paidRec := httptest.NewRecorder()
	app.Router.ServeHTTP(paidRec, paidReq)
	if paidRec.Code != http.StatusOK {
		t.Fatalf("paid invoice webhook status = %d, body = %s", paidRec.Code, paidRec.Body.String())
	}

	allowedReq := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	allowedReq.RemoteAddr = "203.0.113.26:1234"
	allowedReq.Header.Set("X-API-Key", generated.Raw)
	allowedRec := httptest.NewRecorder()
	app.Router.ServeHTTP(allowedRec, allowedReq)
	if allowedRec.Code != http.StatusOK {
		t.Fatalf("recovered status = %d, body = %s, want 200", allowedRec.Code, allowedRec.Body.String())
	}
}

func TestStripeWebhookIdempotencySkipsProcessedEvent(t *testing.T) {
	app := testBillingApp(t, "https://stripe.invalid")
	payload := `{"id":"evt_idempotent","type":"customer.subscription.deleted","data":{"object":{"id":"sub_test","customer":"cus_test","status":"canceled","metadata":{"plan":"developer"},"items":{"data":[{"price":{"id":"price_dev"}}]}}}}`
	account, err := app.BillingStore.UpsertAccount(context.Background(), api.Account{Email: "buyer@example.com", StripeCustomerID: "cus_test", StripeSubscriptionID: "sub_test", Plan: api.PlanDeveloper, SubscriptionStatus: "active"})
	if err != nil {
		t.Fatal(err)
	}

	first := httptest.NewRequest(http.MethodPost, "/stripe/webhook", strings.NewReader(payload))
	first.Header.Set("Stripe-Signature", stripeSignatureHeader(t, payload, app.Config.StripeWebhookSecret, time.Now()))
	firstRec := httptest.NewRecorder()
	app.Router.ServeHTTP(firstRec, first)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("first status = %d, body = %s", firstRec.Code, firstRec.Body.String())
	}

	_, err = app.BillingStore.UpsertAccount(context.Background(), api.Account{
		ID:                   account.ID,
		Email:                "buyer@example.com",
		StripeCustomerID:     "cus_test",
		StripeSubscriptionID: "sub_test",
		Plan:                 api.PlanPro,
		SubscriptionStatus:   "active",
	})
	if err != nil {
		t.Fatal(err)
	}

	second := httptest.NewRequest(http.MethodPost, "/stripe/webhook", strings.NewReader(payload))
	second.Header.Set("Stripe-Signature", stripeSignatureHeader(t, payload, app.Config.StripeWebhookSecret, time.Now()))
	secondRec := httptest.NewRecorder()
	app.Router.ServeHTTP(secondRec, second)
	if secondRec.Code != http.StatusOK {
		t.Fatalf("second status = %d, body = %s", secondRec.Code, secondRec.Body.String())
	}

	updated, ok, err := app.BillingStore.GetAccountByStripeCustomer(context.Background(), "cus_test")
	if err != nil || !ok {
		t.Fatalf("account lookup = (%+v, %v, %v), want account", updated, ok, err)
	}
	if updated.Plan != api.PlanPro || updated.SubscriptionStatus != "active" {
		t.Fatalf("duplicate event was processed again; account = %+v", updated)
	}
}

func TestStripeWebhookRejectsBadSignature(t *testing.T) {
	app := testBillingApp(t, "https://stripe.invalid")
	req := httptest.NewRequest(http.MethodPost, "/stripe/webhook", strings.NewReader(`{"type":"checkout.session.completed"}`))
	req.Header.Set("Stripe-Signature", "t=1,v1=bad")
	rec := httptest.NewRecorder()

	app.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAPIKeySelectsDeveloperRateLimitTier(t *testing.T) {
	app := testBillingApp(t, "https://stripe.invalid")
	account, err := app.BillingStore.UpsertAccount(context.Background(), api.Account{Email: "buyer@example.com", StripeCustomerID: "cus_test", StripeSubscriptionID: "sub_test", Plan: api.PlanDeveloper, SubscriptionStatus: "active"})
	if err != nil {
		t.Fatal(err)
	}
	generated, err := api.GenerateAPIKey("test", app.Config.APIKeyHashSecret)
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.BillingStore.CreateAPIKey(context.Background(), api.APIKeyRecord{AccountID: account.ID, KeyHash: generated.Hash, KeyPrefix: generated.Prefix, Plan: api.PlanDeveloper, LastFour: generated.LastFour})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.RemoteAddr = "203.0.113.20:1234"
	req.Header.Set("X-API-Key", generated.Raw)
	rec := httptest.NewRecorder()

	app.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-API-Plan"); got != api.PlanDeveloper {
		t.Fatalf("X-API-Plan = %q, want developer", got)
	}
	if got := rec.Header().Get("RateLimit-Limit"); got != "300" {
		t.Fatalf("RateLimit-Limit = %q, want 300", got)
	}
}

func TestAPIKeySelectsProRateLimitTier(t *testing.T) {
	app := testBillingApp(t, "https://stripe.invalid")
	account, err := app.BillingStore.UpsertAccount(context.Background(), api.Account{Email: "buyer@example.com", StripeCustomerID: "cus_test", StripeSubscriptionID: "sub_test", Plan: api.PlanPro, SubscriptionStatus: "active"})
	if err != nil {
		t.Fatal(err)
	}
	generated, err := api.GenerateAPIKey("test", app.Config.APIKeyHashSecret)
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.BillingStore.CreateAPIKey(context.Background(), api.APIKeyRecord{AccountID: account.ID, KeyHash: generated.Hash, KeyPrefix: generated.Prefix, Plan: api.PlanPro, LastFour: generated.LastFour})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.RemoteAddr = "203.0.113.23:1234"
	req.Header.Set("Authorization", "Bearer "+generated.Raw)
	rec := httptest.NewRecorder()

	app.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-API-Plan"); got != api.PlanPro {
		t.Fatalf("X-API-Plan = %q, want pro", got)
	}
	if got := rec.Header().Get("RateLimit-Limit"); got != "1200" {
		t.Fatalf("RateLimit-Limit = %q, want 1200", got)
	}
}

func TestRateLimitsEndpointReportsAPIKeyPlan(t *testing.T) {
	app := testBillingApp(t, "https://stripe.invalid")
	account, err := app.BillingStore.UpsertAccount(context.Background(), api.Account{Email: "buyer@example.com", StripeCustomerID: "cus_test", StripeSubscriptionID: "sub_test", Plan: api.PlanPro, SubscriptionStatus: "active"})
	if err != nil {
		t.Fatal(err)
	}
	generated, err := api.GenerateAPIKey("test", app.Config.APIKeyHashSecret)
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.BillingStore.CreateAPIKey(context.Background(), api.APIKeyRecord{AccountID: account.ID, KeyHash: generated.Hash, KeyPrefix: generated.Prefix, Plan: api.PlanPro, LastFour: generated.LastFour})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/rate-limits", nil)
	req.RemoteAddr = "203.0.113.22:1234"
	req.Header.Set("Authorization", "Bearer "+generated.Raw)
	rec := httptest.NewRecorder()

	app.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	assertContains(t, rec.Body.String(), `"authenticated":true`)
	assertContains(t, rec.Body.String(), `"rate_limit":{"plan":"pro","limit_per_minute":1200`)
	assertContains(t, rec.Body.String(), `"shared_by":"account"`)
}

func TestAdminCanCreateInternalAPIKeyWithInternalRateLimit(t *testing.T) {
	app := testBillingAppWithConfig(t, "https://stripe.invalid", func(cfg *config.Config) {
		cfg.InternalMetricsEnabled = true
		cfg.InternalAdminUsername = "admin"
		cfg.InternalAdminPassword = "secret-password"
		cfg.InternalAdminSessionSecret = "admin-session"
		cfg.InternalRateLimitPerMinute = 5000
	})
	cookie := adminLoginCookie(t, app)

	createReq := httptest.NewRequest(http.MethodPost, "/internal/admin/api-keys", strings.NewReader(`{"name":"Config Builder"}`))
	createReq.AddCookie(cookie)
	createRec := httptest.NewRecorder()
	app.Router.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", createRec.Code, createRec.Body.String())
	}
	var created struct {
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.APIKey, "rfm_test_") {
		t.Fatalf("api key = %q, want rfm_test_ prefix", created.APIKey)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.RemoteAddr = "203.0.113.50:1234"
	req.Header.Set("X-API-Key", created.APIKey)
	rec := httptest.NewRecorder()
	app.Router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("internal key status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-API-Plan"); got != api.PlanInternal {
		t.Fatalf("X-API-Plan = %q, want internal", got)
	}
	if got := rec.Header().Get("RateLimit-Limit"); got != "5000" {
		t.Fatalf("RateLimit-Limit = %q, want 5000", got)
	}
}

func TestHealthDoesNotExposeBillingReadiness(t *testing.T) {
	app := testBillingApp(t, "https://stripe.invalid")
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.RemoteAddr = "203.0.113.24:1234"
	rec := httptest.NewRecorder()

	app.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data struct {
			Code  int  `json:"code"`
			Alive bool `json:"alive"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Code != http.StatusOK || !body.Data.Alive {
		t.Fatalf("health data = %+v, want alive 200", body.Data)
	}
	if strings.Contains(rec.Body.String(), "billing") || strings.Contains(rec.Body.String(), "stripe") || strings.Contains(rec.Body.String(), "smtp") {
		t.Fatalf("health response exposes billing readiness: %s", rec.Body.String())
	}
}

func TestHealthAllowsHead(t *testing.T) {
	app := testBillingApp(t, "https://stripe.invalid")
	req := httptest.NewRequest(http.MethodHead, "/v1/health", nil)
	req.RemoteAddr = "203.0.113.24:1234"
	rec := httptest.NewRecorder()

	app.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestProductionBillingConfigValidation(t *testing.T) {
	cfg := testSiteConfig()
	cfg.BillingEnabled = true
	cfg.AppEnv = "production"
	cfg.StripeSecretKey = "sk_live_placeholder"
	cfg.StripeWebhookSecret = "whsec_live"
	cfg.StripeDeveloperPriceID = "price_dev"
	cfg.StripeProPriceID = "price_pro"
	cfg.APIKeyHashSecret = "hash-secret"
	cfg.AccountSessionSecret = "session-secret"
	cfg.SMTPHost = "smtp.example.com"
	cfg.SMTPUsername = "apikey"
	cfg.SMTPPassword = "smtp-secret"
	cfg.SMTPFrom = "Reforger Mods API <no-reply@reforgermods.test>"
	app := &App{Config: cfg}
	if err := app.validateProductionBillingConfig(); err != nil {
		t.Fatalf("validateProductionBillingConfig returned error: %v", err)
	}

	cfg.APIKeyHashSecret = ""
	app = &App{Config: cfg}
	if err := app.validateProductionBillingConfig(); err == nil || !strings.Contains(err.Error(), "API_KEY_HASH_SECRET") {
		t.Fatalf("validateProductionBillingConfig error = %v, want missing API_KEY_HASH_SECRET", err)
	}
}

func TestInvalidAPIKeyRejected(t *testing.T) {
	app := testBillingApp(t, "https://stripe.invalid")
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.RemoteAddr = "203.0.113.21:1234"
	req.Header.Set("Authorization", "Bearer rfm_test_invalid")
	rec := httptest.NewRecorder()

	app.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s, want 401", rec.Code, rec.Body.String())
	}
}

func testBillingApp(t *testing.T, stripeBaseURL string) *App {
	t.Helper()
	return testBillingAppWithConfig(t, stripeBaseURL, nil)
}

func testBillingAppWithConfig(t *testing.T, stripeBaseURL string, adjust func(*config.Config)) *App {
	t.Helper()
	cfg := testSiteConfig()
	cfg.BillingEnabled = true
	cfg.BillingDBPath = filepath.Join(t.TempDir(), "billing.db")
	cfg.StripeSecretKey = "sk_test_placeholder"
	cfg.StripeWebhookSecret = "whsec_test"
	cfg.StripeDeveloperPriceID = "price_dev"
	cfg.StripeProPriceID = "price_pro"
	cfg.BillingSuccessURL = "https://reforgermods.test/account/api-keys/?checkout=success&session_id={CHECKOUT_SESSION_ID}"
	cfg.BillingCancelURL = "https://reforgermods.test/pricing"
	cfg.BillingPortalReturnURL = "https://reforgermods.test/account/billing"
	cfg.APIKeyHashSecret = "test-secret"
	cfg.StripeAPIBaseURL = stripeBaseURL
	cfg.AnonymousRateLimitPerMinute = 60
	cfg.DeveloperRateLimitPerMinute = 300
	cfg.ProRateLimitPerMinute = 1200
	cfg.MaxBodyBytes = 1048576
	if adjust != nil {
		adjust(&cfg)
	}
	app := &App{Config: cfg}
	app.Initialize()
	t.Cleanup(func() {
		_ = app.Shutdown(context.Background())
	})
	return app
}

func assertFormValue(t *testing.T, form url.Values, key string, want string) {
	t.Helper()
	if got := form.Get(key); got != want {
		t.Fatalf("form %s = %q, want %q", key, got, want)
	}
}

func stripeSignatureHeader(t *testing.T, payload string, secret string, ts time.Time) string {
	t.Helper()
	timestamp := ts.Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(strconv.FormatInt(timestamp, 10) + "." + payload))
	return "t=" + strconv.FormatInt(timestamp, 10) + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}
