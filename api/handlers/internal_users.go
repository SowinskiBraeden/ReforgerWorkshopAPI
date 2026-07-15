package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/api"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

// internalAdminBillingGuard applies the shared checks for admin endpoints that
// need the billing store. It writes the error response and returns false when
// the request must not proceed.
func (a *App) internalAdminBillingGuard(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if !a.Config.InternalMetricsEnabled {
		config.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "Not found.")
		return false
	}
	if !a.internalAdminAllowed(r) {
		writeAdminUnauthorized(w, r)
		return false
	}
	if a.BillingStore == nil {
		config.WriteError(w, r, http.StatusServiceUnavailable, "BILLING_DISABLED", "Billing store is not available.")
		return false
	}
	return true
}

type internalRevokeKeyRequest struct {
	Notify bool   `json:"notify"`
	Reason string `json:"reason"`
}

func (a *App) internalRevokeUserKeyHandler(w http.ResponseWriter, r *http.Request) {
	if !a.internalAdminBillingGuard(w, r) {
		return
	}
	accountID := strings.TrimSpace(mux.Vars(r)["id"])
	keyID := strings.TrimSpace(mux.Vars(r)["keyId"])
	if accountID == "" || keyID == "" {
		config.WriteError(w, r, http.StatusBadRequest, "INVALID_KEY", "Account id and key id are required.")
		return
	}
	input := internalRevokeKeyRequest{Notify: true}
	if r.Body != nil {
		_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&input)
	}
	account, found, err := a.BillingStore.GetAccount(r.Context(), accountID)
	if err != nil || !found {
		config.WriteError(w, r, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "Account was not found.")
		return
	}
	key, found, err := a.BillingStore.GetAPIKeyByID(r.Context(), accountID, keyID)
	if err != nil || !found {
		config.WriteError(w, r, http.StatusNotFound, "KEY_NOT_FOUND", "API key was not found.")
		return
	}
	if err := a.BillingStore.RevokeAPIKey(r.Context(), accountID, keyID); err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "KEY_REVOKE_FAILED", "Failed to revoke API key.")
		return
	}
	zap.S().Infow("admin revoked api key", "accountId", accountID, "keyId", keyID, "reason", input.Reason)
	emailed := false
	if input.Notify && strings.TrimSpace(account.Email) != "" && a.Mailer.Configured() {
		emailed = true
		a.sendKeyRevokedEmail(account.Email, key, input.Reason)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"revoked": true, "emailed": emailed})
}

func (a *App) sendKeyRevokedEmail(email string, key api.APIKeyRecord, reason string) {
	name := strings.TrimSpace(key.Name)
	if name == "" {
		name = "API key"
	}
	body := fmt.Sprintf("Your Reforger Mods API key %q (%s...%s) has been revoked by an administrator.\n\n",
		name, key.KeyPrefix, key.LastFour)
	if strings.TrimSpace(reason) != "" {
		body += "Reason: " + strings.TrimSpace(reason) + "\n\n"
	}
	body += "The key no longer works. If you believe this was a mistake, reply to this email or visit " +
		a.Config.PublicBaseURL + "/support/ to get in touch.\n\n- Reforger Mods API\n"
	mailer := a.Mailer
	go func() {
		if err := mailer.Send(email, "Your Reforger Mods API key was revoked", body); err != nil {
			zap.S().Warnw("key revocation email delivery failed", "email", email, "error", err)
		}
	}()
}

func (a *App) internalSendLoginLinkHandler(w http.ResponseWriter, r *http.Request) {
	if !a.internalAdminBillingGuard(w, r) {
		return
	}
	accountID := strings.TrimSpace(mux.Vars(r)["id"])
	account, found, err := a.BillingStore.GetAccount(r.Context(), accountID)
	if err != nil || !found {
		config.WriteError(w, r, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "Account was not found.")
		return
	}
	if strings.TrimSpace(account.Email) == "" {
		config.WriteError(w, r, http.StatusBadRequest, "NO_EMAIL", "Account has no email address.")
		return
	}
	raw, hash, err := api.GenerateLoginToken(a.Config.APIKeyHashSecret)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "LOGIN_TOKEN_FAILED", "Failed to create sign-in link.")
		return
	}
	// Admin-issued links skip the request cooldown so support can always
	// resend one immediately.
	created, err := a.BillingStore.CreateLoginToken(r.Context(), account.ID, hash, a.loginTokenTTL(), 0)
	if err != nil || !created {
		config.WriteError(w, r, http.StatusInternalServerError, "LOGIN_TOKEN_FAILED", "Failed to create sign-in link.")
		return
	}
	a.deliverLoginEmail(account.Email, raw, "An administrator sent you this sign-in link for your Reforger Mods API account.")
	zap.S().Infow("admin sent login link", "accountId", account.ID, "email", account.Email)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"sent": true, "mailer_configured": a.Mailer.Configured()})
}

func (a *App) internalDeleteUserHandler(w http.ResponseWriter, r *http.Request) {
	if !a.internalAdminBillingGuard(w, r) {
		return
	}
	accountID := strings.TrimSpace(mux.Vars(r)["id"])
	account, found, err := a.BillingStore.GetAccount(r.Context(), accountID)
	if err != nil || !found {
		config.WriteError(w, r, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "Account was not found.")
		return
	}
	force := r.URL.Query().Get("force") == "true"
	if !force && account.Plan != api.PlanFree && account.SubscriptionStatus == "active" {
		config.WriteError(w, r, http.StatusConflict, "ACCOUNT_HAS_SUBSCRIPTION",
			"Account has an active paid subscription. Cancel it in Stripe first, or repeat with force=true.")
		return
	}
	if err := a.BillingStore.DeleteAccount(r.Context(), accountID); err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "ACCOUNT_DELETE_FAILED", "Failed to delete account.")
		return
	}
	zap.S().Infow("admin deleted account", "accountId", accountID, "email", account.Email, "plan", account.Plan, "forced", force)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]bool{"deleted": true})
}
