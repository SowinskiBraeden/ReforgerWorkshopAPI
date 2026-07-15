package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/api"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
	"go.uber.org/zap"
)

const accountSessionCookie = "rfm_account_session"

// loginSentMessage is returned for every login request so responses do not
// reveal whether an email has a subscription.
const loginSentMessage = "If that email has a subscription, a sign-in link is on its way. It expires in 30 minutes."

type loginRequest struct {
	Email string `json:"email"`
}

func (a *App) AccountLoginHandler(w http.ResponseWriter, r *http.Request) {
	if !a.billingReady(w, r) {
		return
	}
	var input loginRequest
	if err := decodeBillingJSON(r, a.Config.MaxBodyBytes, &input); err != nil {
		config.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON.")
		return
	}
	email := strings.TrimSpace(input.Email)
	if email == "" || !strings.Contains(email, "@") || len(email) > 254 {
		config.WriteError(w, r, http.StatusBadRequest, "INVALID_EMAIL", "A valid email address is required.")
		return
	}
	sent := map[string]any{"sent": true, "message": loginSentMessage}
	account, ok, err := a.BillingStore.GetAccountByEmail(r.Context(), email)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "LOGIN_LOOKUP_FAILED", "Failed to process sign-in request.")
		return
	}
	if !ok {
		writeBillingJSON(w, http.StatusOK, sent)
		return
	}
	raw, hash, err := api.GenerateLoginToken(a.Config.APIKeyHashSecret)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "LOGIN_TOKEN_FAILED", "Failed to process sign-in request.")
		return
	}
	created, err := a.BillingStore.CreateLoginToken(r.Context(), account.ID, hash, a.loginTokenTTL(), a.Config.LoginTokenCooldown)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "LOGIN_TOKEN_FAILED", "Failed to process sign-in request.")
		return
	}
	if created {
		a.deliverLoginEmail(account.Email, raw, "Sign in to manage your Reforger Mods API keys.")
	}
	writeBillingJSON(w, http.StatusOK, sent)
}

func (a *App) AccountVerifyHandler(w http.ResponseWriter, r *http.Request) {
	if !a.Config.BillingEnabled || a.BillingStore == nil {
		http.NotFound(w, r)
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	hash, err := api.HashAPIKey(token, a.Config.APIKeyHashSecret)
	if err != nil {
		http.Redirect(w, r, "/account/api-keys/?login=invalid", http.StatusSeeOther)
		return
	}
	accountID, ok, err := a.BillingStore.ConsumeLoginToken(r.Context(), hash)
	if err != nil || !ok {
		http.Redirect(w, r, "/account/api-keys/?login=invalid", http.StatusSeeOther)
		return
	}
	a.setAccountSessionCookie(w, accountID)
	http.Redirect(w, r, "/account/api-keys/?login=success", http.StatusSeeOther)
}

func (a *App) AccountLogoutHandler(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     accountSessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.cookieSecure(),
		SameSite: http.SameSiteLaxMode,
	})
	writeBillingJSON(w, http.StatusOK, map[string]bool{"signed_out": true})
}

// AccountSessionHandler tells the frontend whether the browser holds a valid
// account session, and for whom.
func (a *App) AccountSessionHandler(w http.ResponseWriter, r *http.Request) {
	if !a.billingReady(w, r) {
		return
	}
	account, err := a.accountFromRequest(r)
	if err != nil {
		writeBillingJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	writeBillingJSON(w, http.StatusOK, map[string]any{
		"authenticated":       true,
		"email":               account.Email,
		"plan":                account.Plan,
		"subscription_status": account.SubscriptionStatus,
	})
}

// accountFromRequest resolves the signed-in account from the session cookie.
func (a *App) accountFromRequest(r *http.Request) (api.Account, error) {
	cookie, err := r.Cookie(accountSessionCookie)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return api.Account{}, fmt.Errorf("not signed in")
	}
	accountID, ok := api.VerifyAccountSessionToken(cookie.Value, a.sessionSecret(), time.Now())
	if !ok {
		return api.Account{}, fmt.Errorf("session is invalid or expired")
	}
	account, found, err := a.BillingStore.GetAccount(r.Context(), accountID)
	if err != nil || !found {
		return api.Account{}, fmt.Errorf("account was not found")
	}
	return account, nil
}

func (a *App) setAccountSessionCookie(w http.ResponseWriter, accountID string) {
	ttl := a.Config.AccountSessionTTL
	if ttl <= 0 {
		ttl = 30 * 24 * time.Hour
	}
	expires := time.Now().Add(ttl)
	http.SetCookie(w, &http.Cookie{
		Name:     accountSessionCookie,
		Value:    api.CreateAccountSessionToken(accountID, expires, a.sessionSecret()),
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   a.cookieSecure(),
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *App) loginTokenTTL() time.Duration {
	if a.Config.LoginTokenTTL > 0 {
		return a.Config.LoginTokenTTL
	}
	return 30 * time.Minute
}

func (a *App) sessionSecret() string {
	if secret := strings.TrimSpace(a.Config.AccountSessionSecret); secret != "" {
		return secret
	}
	return a.Config.APIKeyHashSecret
}

func (a *App) cookieSecure() bool {
	return strings.HasPrefix(strings.ToLower(a.Config.PublicBaseURL), "https://")
}

// deliverLoginEmail sends a magic sign-in link asynchronously. Without SMTP
// configured the link is logged instead so local development still works.
func (a *App) deliverLoginEmail(email string, rawToken string, intro string) {
	link := a.Config.PublicBaseURL + "/account/verify?token=" + url.QueryEscape(rawToken)
	if !a.Mailer.Configured() {
		zap.S().Warnw("smtp not configured; logging sign-in link instead", "email", email, "link", link)
		return
	}
	body := intro + "\n\n" +
		"Use this one-time link to sign in:\n" + link + "\n\n" +
		"The link expires in 30 minutes and can only be used once.\n" +
		"If you did not request it, you can ignore this email.\n\n" +
		"- Reforger Mods API\n"
	mailer := a.Mailer
	go func() {
		if err := mailer.Send(email, "Sign in to Reforger Mods API", body); err != nil {
			zap.S().Warnw("sign-in email delivery failed", "email", email, "error", err)
		}
	}()
}

// sendWelcomeSignInEmail issues a fresh sign-in link after checkout so key
// access is never tied to the browser that completed payment.
func (a *App) sendWelcomeSignInEmail(ctx context.Context, account api.Account) {
	if strings.TrimSpace(account.Email) == "" {
		return
	}
	raw, hash, err := api.GenerateLoginToken(a.Config.APIKeyHashSecret)
	if err != nil {
		return
	}
	created, err := a.BillingStore.CreateLoginToken(ctx, account.ID, hash, a.loginTokenTTL(), a.Config.LoginTokenCooldown)
	if err != nil || !created {
		return
	}
	a.deliverLoginEmail(account.Email, raw, "Your Reforger Mods API subscription is active. Sign in any time to view, create, or revoke API keys.")
}
