package handlers

import (
	"fmt"
	"strings"
)

func (a *App) validateProductionBillingConfig() error {
	if !a.productionBillingEnabled() {
		return nil
	}
	var missing []string
	require := func(name string, value string) {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	require("STRIPE_SECRET_KEY", a.Config.StripeSecretKey)
	require("STRIPE_WEBHOOK_SECRET", a.Config.StripeWebhookSecret)
	require("STRIPE_DEVELOPER_PRICE_ID", a.Config.StripeDeveloperPriceID)
	require("STRIPE_PRO_PRICE_ID", a.Config.StripeProPriceID)
	require("API_KEY_HASH_SECRET", a.Config.APIKeyHashSecret)
	require("ACCOUNT_SESSION_SECRET", a.Config.AccountSessionSecret)
	require("SMTP_HOST", a.Config.SMTPHost)
	require("SMTP_USERNAME", a.Config.SMTPUsername)
	require("SMTP_PASSWORD", a.Config.SMTPPassword)
	require("SMTP_FROM", a.Config.SMTPFrom)
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(a.Config.PublicBaseURL)), "https://") {
		missing = append(missing, "PUBLIC_BASE_URL=https://...")
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(a.Config.APIBaseURL)), "https://") {
		missing = append(missing, "API_BASE_URL=https://...")
	}
	if len(missing) > 0 {
		return fmt.Errorf("production billing requires: %s", strings.Join(missing, ", "))
	}
	if !strings.HasPrefix(strings.TrimSpace(a.Config.StripeSecretKey), "sk_live_") {
		return fmt.Errorf("production billing requires a live Stripe secret key")
	}
	return nil
}

func (a *App) productionBillingEnabled() bool {
	return a.Config.BillingEnabled && strings.EqualFold(strings.TrimSpace(a.Config.AppEnv), "production")
}
