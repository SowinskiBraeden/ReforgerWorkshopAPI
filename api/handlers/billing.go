package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/api"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

const stripeWebhookTolerance = 5 * time.Minute
const billingAppVersion = "1.3.0"

type checkoutRequest struct {
	Plan  string `json:"plan"`
	Email string `json:"email"`
}

type createAPIKeyRequest struct {
	Name string `json:"name"`
}

func (a *App) BillingCheckoutHandler(w http.ResponseWriter, r *http.Request) {
	if !a.billingReady(w, r) {
		return
	}
	var input checkoutRequest
	if err := decodeBillingJSON(r, a.Config.MaxBodyBytes, &input); err != nil {
		config.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON.")
		return
	}
	plan := normalizeBillingPlan(input.Plan)
	priceID, ok := a.priceIDForPlan(plan)
	if !ok {
		config.WriteError(w, r, http.StatusBadRequest, "INVALID_PLAN", "Plan must be developer or pro.")
		return
	}
	email := strings.TrimSpace(input.Email)
	if email != "" && (!strings.Contains(email, "@") || len(email) > 254) {
		config.WriteError(w, r, http.StatusBadRequest, "INVALID_EMAIL", "Email address is invalid.")
		return
	}
	account, err := a.BillingStore.UpsertAccount(r.Context(), api.Account{Email: email, Plan: api.PlanFree, SubscriptionStatus: api.SubscriptionStatusNone})
	if err != nil {
		zap.S().Warnw("billing account preparation failed", "error", err, "email_present", email != "")
		config.WriteError(w, r, http.StatusInternalServerError, "ACCOUNT_STORE_FAILED", "Failed to prepare billing account.")
		return
	}
	session, err := a.StripeClient.CreateSubscriptionCheckoutSession(r.Context(), api.CreateStripeCheckoutSessionParams{
		PriceID:    priceID,
		SuccessURL: a.Config.BillingSuccessURL,
		CancelURL:  a.Config.BillingCancelURL,
		Email:      email,
		Plan:       plan,
		AccountID:  account.ID,
		AppVersion: billingAppVersion,
	})
	if err != nil {
		zap.S().Warnw("stripe subscription checkout creation failed", "error", err)
		config.WriteError(w, r, http.StatusBadGateway, "CHECKOUT_SESSION_FAILED", "Failed to create Stripe Checkout Session.")
		return
	}
	writeBillingJSON(w, http.StatusOK, map[string]string{"url": session.URL})
}

func (a *App) BillingSessionHandler(w http.ResponseWriter, r *http.Request) {
	if !a.billingReady(w, r) {
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" || !strings.HasPrefix(sessionID, "cs_") {
		config.WriteError(w, r, http.StatusBadRequest, "INVALID_SESSION", "A valid session_id is required.")
		return
	}
	session, err := a.StripeClient.GetCheckoutSession(r.Context(), sessionID)
	if err != nil {
		config.WriteError(w, r, http.StatusBadGateway, "SESSION_LOOKUP_FAILED", "Failed to verify Stripe Checkout Session.")
		return
	}
	account, err := a.accountFromCheckoutSession(r, session)
	if err != nil {
		config.WriteError(w, r, http.StatusBadGateway, "SESSION_NOT_ACTIVE", err.Error())
		return
	}
	rawKey, key, err := a.ensureInitialAPIKey(r, account)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "API_KEY_PROVISION_FAILED", "Failed to provision API key.")
		return
	}
	// A verified completed Checkout session proves ownership, so sign the
	// browser in for follow-up key management.
	a.setAccountSessionCookie(w, account.ID)
	resp := map[string]any{
		"status":         account.SubscriptionStatus,
		"plan":           account.Plan,
		"email":          account.Email,
		"api_key":        nil,
		"api_key_prefix": key.KeyPrefix,
		"message":        "This API key was already revealed. Create a new key from your account page if needed.",
	}
	if rawKey != "" {
		resp["api_key"] = rawKey
		resp["message"] = "Store this API key now. It will only be shown once."
	}
	writeBillingJSON(w, http.StatusOK, resp)
}

func (a *App) BillingPortalHandler(w http.ResponseWriter, r *http.Request) {
	if !a.billingReady(w, r) {
		return
	}
	account, err := a.accountFromRequest(r)
	if err != nil {
		config.WriteError(w, r, http.StatusUnauthorized, "NOT_SIGNED_IN", "Sign in with your email to open the billing portal.")
		return
	}
	if strings.TrimSpace(account.StripeCustomerID) == "" {
		config.WriteError(w, r, http.StatusBadRequest, "NO_STRIPE_CUSTOMER", "This account has no Stripe customer yet.")
		return
	}
	portal, err := a.StripeClient.CreatePortalSession(r.Context(), account.StripeCustomerID, a.Config.BillingPortalReturnURL)
	if err != nil {
		config.WriteError(w, r, http.StatusBadGateway, "PORTAL_SESSION_FAILED", "Failed to create Stripe Customer Portal session.")
		return
	}
	writeBillingJSON(w, http.StatusOK, map[string]string{"url": portal.URL})
}

func (a *App) AccountAPIKeysHandler(w http.ResponseWriter, r *http.Request) {
	if !a.billingReady(w, r) {
		return
	}
	account, err := a.accountFromRequest(r)
	if err != nil {
		config.WriteError(w, r, http.StatusUnauthorized, "NOT_SIGNED_IN", "Sign in with your email to manage API keys.")
		return
	}
	keys, err := a.BillingStore.ActiveAPIKeysForAccount(r.Context(), account.ID)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "API_KEYS_LOOKUP_FAILED", "Failed to load API keys.")
		return
	}
	items := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		item := map[string]any{"id": key.ID, "prefix": key.KeyPrefix, "last_four": key.LastFour, "name": key.Name, "plan": key.Plan, "created_at": key.CreatedAt}
		if !key.LastUsedAt.IsZero() {
			item["last_used_at"] = key.LastUsedAt
		}
		items = append(items, item)
	}
	writeBillingJSON(w, http.StatusOK, map[string]any{
		"email":               account.Email,
		"plan":                account.Plan,
		"subscription_status": account.SubscriptionStatus,
		"api_keys":            items,
		"key_limit":           a.maxActiveKeys(account.Plan),
		"rate_limit":          a.rateLimitInfo(account.Plan),
	})
}

func (a *App) RateLimitsHandler(w http.ResponseWriter, r *http.Request) {
	auth, authenticated := api.BillingAuthFromContext(r.Context())
	plan := api.PlanFree
	if authenticated {
		plan = auth.Plan
	}
	writeBillingJSON(w, http.StatusOK, map[string]any{
		"authenticated": authenticated,
		"rate_limit":    a.rateLimitInfo(plan),
	})
}

func (a *App) CreateAccountAPIKeyHandler(w http.ResponseWriter, r *http.Request) {
	if !a.billingReady(w, r) {
		return
	}
	var input createAPIKeyRequest
	if err := decodeBillingJSON(r, a.Config.MaxBodyBytes, &input); err != nil {
		config.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON.")
		return
	}
	account, err := a.accountFromRequest(r)
	if err != nil {
		config.WriteError(w, r, http.StatusUnauthorized, "NOT_SIGNED_IN", "Sign in with your email to manage API keys.")
		return
	}
	keys, err := a.BillingStore.ActiveAPIKeysForAccount(r.Context(), account.ID)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "API_KEYS_LOOKUP_FAILED", "Failed to load API keys.")
		return
	}
	limit := a.maxActiveKeys(account.Plan)
	if len(keys) >= limit {
		config.WriteError(w, r, http.StatusForbidden, "API_KEY_LIMIT_REACHED", fmt.Sprintf("Your plan allows %d active keys. Revoke a key to create a new one.", limit))
		return
	}
	raw, key, err := a.createAPIKeyForAccount(r, account, input.Name)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "API_KEY_CREATE_FAILED", "Failed to create API key.")
		return
	}
	writeBillingJSON(w, http.StatusOK, map[string]any{"api_key": raw, "api_key_prefix": key.KeyPrefix, "message": "Store this API key now. It will only be shown once."})
}

func (a *App) DeleteAccountAPIKeyHandler(w http.ResponseWriter, r *http.Request) {
	if !a.billingReady(w, r) {
		return
	}
	account, err := a.accountFromRequest(r)
	if err != nil {
		config.WriteError(w, r, http.StatusUnauthorized, "NOT_SIGNED_IN", "Sign in with your email to manage API keys.")
		return
	}
	id := strings.TrimSpace(muxVar(r, "id"))
	if id == "" {
		config.WriteError(w, r, http.StatusBadRequest, "INVALID_API_KEY", "API key id is required.")
		return
	}
	if err := a.BillingStore.RevokeAPIKey(r.Context(), account.ID, id); err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "API_KEY_REVOKE_FAILED", "Failed to revoke API key.")
		return
	}
	writeBillingJSON(w, http.StatusOK, map[string]bool{"revoked": true})
}

type stripeWebhookEvent struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Data struct {
		Object json.RawMessage `json:"object"`
	} `json:"data"`
}

type stripeSubscriptionObject struct {
	ID       string            `json:"id"`
	Customer string            `json:"customer"`
	Status   string            `json:"status"`
	Metadata map[string]string `json:"metadata"`
	Items    struct {
		Data []struct {
			Price struct {
				ID string `json:"id"`
			} `json:"price"`
		} `json:"data"`
	} `json:"items"`
}

type stripeInvoiceObject struct {
	Customer     string `json:"customer"`
	Subscription string `json:"subscription"`
}

func (a *App) StripeWebhookHandler(w http.ResponseWriter, r *http.Request) {
	if !a.billingReady(w, r) {
		return
	}
	if strings.TrimSpace(a.Config.StripeWebhookSecret) == "" {
		config.WriteError(w, r, http.StatusServiceUnavailable, "STRIPE_WEBHOOK_NOT_CONFIGURED", "Stripe webhook signing secret is not configured.")
		return
	}
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, billingBodyLimit(a.Config.MaxBodyBytes)))
	if err != nil {
		config.WriteError(w, r, http.StatusBadRequest, "INVALID_WEBHOOK_BODY", "Webhook body could not be read.")
		return
	}
	if err := verifyStripeSignature(body, r.Header.Get("Stripe-Signature"), a.Config.StripeWebhookSecret, time.Now); err != nil {
		config.WriteError(w, r, http.StatusBadRequest, "INVALID_STRIPE_SIGNATURE", "Stripe webhook signature verification failed.")
		return
	}
	var event stripeWebhookEvent
	if err := json.Unmarshal(body, &event); err != nil {
		config.WriteError(w, r, http.StatusBadRequest, "INVALID_WEBHOOK_JSON", "Webhook body must be valid JSON.")
		return
	}
	shouldProcess, err := a.BillingStore.BeginStripeEvent(r.Context(), event.ID, event.Type)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "WEBHOOK_IDEMPOTENCY_FAILED", "Webhook event could not be checked.")
		return
	}
	if !shouldProcess {
		writeBillingJSON(w, http.StatusOK, map[string]bool{"received": true})
		return
	}
	if err := a.processStripeEvent(r, event); err != nil {
		_ = a.BillingStore.FinishStripeEvent(r.Context(), event.ID, "failed", err.Error())
		zap.S().Warnw("stripe webhook processing failed", "event", event.ID, "type", event.Type, "error", err)
		config.WriteError(w, r, http.StatusInternalServerError, "WEBHOOK_PROCESSING_FAILED", "Webhook event could not be processed.")
		return
	}
	_ = a.BillingStore.FinishStripeEvent(r.Context(), event.ID, "processed", "")
	writeBillingJSON(w, http.StatusOK, map[string]bool{"received": true})
}

func (a *App) processStripeEvent(r *http.Request, event stripeWebhookEvent) error {
	switch event.Type {
	case "checkout.session.completed":
		var session api.StripeCheckoutSession
		if err := json.Unmarshal(event.Data.Object, &session); err != nil {
			return err
		}
		account, err := a.accountFromCheckoutSession(r, session)
		if err != nil {
			return err
		}
		a.sendWelcomeSignInEmail(r.Context(), account)
		return nil
	case "customer.subscription.created", "customer.subscription.updated":
		var sub stripeSubscriptionObject
		if err := json.Unmarshal(event.Data.Object, &sub); err != nil {
			return err
		}
		_, err := a.upsertAccountFromSubscription(r, sub, "")
		return err
	case "customer.subscription.deleted":
		var sub stripeSubscriptionObject
		if err := json.Unmarshal(event.Data.Object, &sub); err != nil {
			return err
		}
		account, err := a.upsertAccountFromSubscription(r, sub, "")
		if err != nil {
			return err
		}
		return a.BillingStore.RevokePaidKeysForAccount(r.Context(), account.ID)
	case "invoice.paid":
		return a.updateInvoiceSubscriptionStatus(r, event.Data.Object, "active")
	case "invoice.payment_failed":
		return a.updateInvoiceSubscriptionStatus(r, event.Data.Object, "past_due")
	default:
		return nil
	}
}

func (a *App) updateInvoiceSubscriptionStatus(r *http.Request, raw json.RawMessage, status string) error {
	var invoice stripeInvoiceObject
	if err := json.Unmarshal(raw, &invoice); err != nil {
		return err
	}
	account, ok, err := a.BillingStore.GetAccountBySubscription(r.Context(), invoice.Subscription)
	if err != nil || !ok {
		return err
	}
	account.SubscriptionStatus = status
	_, err = a.BillingStore.UpsertAccount(r.Context(), account)
	return err
}

func (a *App) accountFromCheckoutSession(r *http.Request, session api.StripeCheckoutSession) (api.Account, error) {
	plan := normalizeBillingPlan(session.Metadata["plan"])
	if plan == "" {
		return api.Account{}, fmt.Errorf("checkout session missing plan metadata")
	}
	if session.Customer == "" || session.Subscription == "" {
		return api.Account{}, fmt.Errorf("checkout session is missing customer or subscription")
	}
	status := "active"
	if session.Status != "" && session.Status != "complete" {
		return api.Account{}, fmt.Errorf("checkout session is not complete")
	}
	account := api.Account{
		ID:                   session.ClientReferenceID,
		Email:                firstNonEmpty(session.CustomerEmail, session.CustomerDetails.Email),
		StripeCustomerID:     session.Customer,
		StripeSubscriptionID: session.Subscription,
		Plan:                 plan,
		SubscriptionStatus:   status,
	}
	return a.BillingStore.UpsertAccountByStripeCustomer(r.Context(), account)
}

func (a *App) upsertAccountFromSubscription(r *http.Request, sub stripeSubscriptionObject, email string) (api.Account, error) {
	plan := planFromSubscription(sub, a.Config.StripeDeveloperPriceID, a.Config.StripeProPriceID)
	status := strings.TrimSpace(sub.Status)
	if !subscriptionAllowsPaidAccess(status) {
		plan = api.PlanFree
	}
	account, ok, err := a.BillingStore.GetAccountByStripeCustomer(r.Context(), sub.Customer)
	if err != nil {
		return account, err
	}
	if !ok {
		account = api.Account{StripeCustomerID: sub.Customer}
	}
	account.Email = firstNonEmpty(account.Email, email)
	account.StripeSubscriptionID = sub.ID
	account.Plan = plan
	account.SubscriptionStatus = firstNonEmpty(status, api.SubscriptionStatusNone)
	account, err = a.BillingStore.UpsertAccountByStripeCustomer(r.Context(), account)
	if err != nil {
		return account, err
	}
	if account.Plan == api.PlanFree {
		err = a.BillingStore.RevokePaidKeysForAccount(r.Context(), account.ID)
	}
	return account, err
}

func (a *App) ensureInitialAPIKey(r *http.Request, account api.Account) (string, api.APIKeyRecord, error) {
	keys, err := a.BillingStore.ActiveAPIKeysForAccount(r.Context(), account.ID)
	if err != nil {
		return "", api.APIKeyRecord{}, err
	}
	if len(keys) > 0 {
		return "", keys[0], nil
	}
	return a.createAPIKeyForAccount(r, account, "Default")
}

func (a *App) createAPIKeyForAccount(r *http.Request, account api.Account, name string) (string, api.APIKeyRecord, error) {
	if !subscriptionAllowsPaidAccess(account.SubscriptionStatus) || normalizeBillingPlan(account.Plan) == "" {
		return "", api.APIKeyRecord{}, fmt.Errorf("subscription is not active")
	}
	mode := "test"
	if a.Config.AppEnv == "production" || strings.HasPrefix(a.Config.StripeSecretKey, "sk_live_") {
		mode = "live"
	}
	generated, err := api.GenerateAPIKey(mode, a.Config.APIKeyHashSecret)
	if err != nil {
		return "", api.APIKeyRecord{}, err
	}
	key, err := a.BillingStore.CreateAPIKey(r.Context(), api.APIKeyRecord{
		AccountID: account.ID,
		KeyHash:   generated.Hash,
		KeyPrefix: generated.Prefix,
		Name:      strings.TrimSpace(name),
		Plan:      account.Plan,
		LastFour:  generated.LastFour,
	})
	if err != nil {
		return "", key, err
	}
	return generated.Raw, key, nil
}

func (a *App) billingReady(w http.ResponseWriter, r *http.Request) bool {
	if !a.Config.BillingEnabled || a.BillingStore == nil {
		config.WriteError(w, r, http.StatusNotFound, "BILLING_DISABLED", "Billing is not enabled.")
		return false
	}
	if strings.TrimSpace(a.Config.StripeSecretKey) == "" {
		config.WriteError(w, r, http.StatusServiceUnavailable, "STRIPE_NOT_CONFIGURED", "Stripe is not configured.")
		return false
	}
	return true
}

func (a *App) maxActiveKeys(plan string) int {
	limit := a.Config.DeveloperMaxActiveKeys
	if normalizeBillingPlan(plan) == api.PlanPro {
		limit = a.Config.ProMaxActiveKeys
	}
	if limit <= 0 {
		limit = 2
	}
	return limit
}

func (a *App) priceIDForPlan(plan string) (string, bool) {
	switch normalizeBillingPlan(plan) {
	case api.PlanDeveloper:
		return strings.TrimSpace(a.Config.StripeDeveloperPriceID), strings.TrimSpace(a.Config.StripeDeveloperPriceID) != ""
	case api.PlanPro:
		return strings.TrimSpace(a.Config.StripeProPriceID), strings.TrimSpace(a.Config.StripeProPriceID) != ""
	default:
		return "", false
	}
}

func normalizeBillingPlan(plan string) string {
	switch strings.ToLower(strings.TrimSpace(plan)) {
	case api.PlanInternal:
		return api.PlanInternal
	case api.PlanDeveloper:
		return api.PlanDeveloper
	case api.PlanPro:
		return api.PlanPro
	default:
		return ""
	}
}

func planFromSubscription(sub stripeSubscriptionObject, developerPriceID string, proPriceID string) string {
	if plan := normalizeBillingPlan(sub.Metadata["plan"]); plan != "" {
		return plan
	}
	for _, item := range sub.Items.Data {
		switch item.Price.ID {
		case developerPriceID:
			return api.PlanDeveloper
		case proPriceID:
			return api.PlanPro
		}
	}
	return api.PlanFree
}

func subscriptionAllowsPaidAccess(status string) bool {
	return strings.TrimSpace(status) == "active"
}

func decodeBillingJSON(r *http.Request, maxBody int64, out any) error {
	if r.Body == nil {
		return nil
	}
	defer r.Body.Close()
	err := json.NewDecoder(io.LimitReader(r.Body, billingBodyLimit(maxBody))).Decode(out)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func writeBillingJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func muxVar(r *http.Request, key string) string {
	return mux.Vars(r)[key]
}

func billingBodyLimit(configured int64) int64 {
	if configured > 0 {
		return configured
	}
	return 1048576
}

func verifyStripeSignature(payload []byte, header string, secret string, now func() time.Time) error {
	timestamp, signatures := parseStripeSignatureHeader(header)
	if timestamp == 0 || len(signatures) == 0 {
		return fmt.Errorf("missing timestamp or v1 signature")
	}
	eventTime := time.Unix(timestamp, 0)
	if now == nil {
		now = time.Now
	}
	if delta := now().Sub(eventTime); delta > stripeWebhookTolerance || delta < -stripeWebhookTolerance {
		return fmt.Errorf("signature timestamp outside tolerance")
	}
	signedPayload := strconv.FormatInt(timestamp, 10) + "." + string(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signedPayload))
	expected := mac.Sum(nil)
	for _, sig := range signatures {
		actual, err := hex.DecodeString(sig)
		if err == nil && hmac.Equal(actual, expected) {
			return nil
		}
	}
	return fmt.Errorf("no matching v1 signature")
}

func parseStripeSignatureHeader(header string) (int64, []string) {
	var timestamp int64
	var signatures []string
	for _, part := range strings.Split(header, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch key {
		case "t":
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err == nil {
				timestamp = parsed
			}
		case "v1":
			if value != "" {
				signatures = append(signatures, value)
			}
		}
	}
	return timestamp, signatures
}
