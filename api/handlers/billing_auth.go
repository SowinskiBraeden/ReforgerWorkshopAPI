package handlers

import (
	"context"
	"net/http"
	"strings"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/api"
)

type rateLimitInfo struct {
	Plan     string `json:"plan"`
	Limit    int    `json:"limit_per_minute"`
	Burst    int    `json:"burst"`
	Window   string `json:"window"`
	SharedBy string `json:"shared_by"`
	KeyLimit int    `json:"active_key_limit,omitempty"`
}

func (a *App) configureBillingIdentityResolver() {
	if a.Middleware == nil {
		return
	}
	a.Middleware.SetIdentityResolver(func(r *http.Request, clientIP string) api.RateIdentity {
		key := apiKeyFromRequest(r)
		if key == "" || a.BillingStore == nil {
			return api.RateIdentity{Bucket: "plan:free:" + clientIP, Limit: a.Config.AnonymousRateLimitPerMinute, Burst: a.Config.AnonymousRateBurst}
		}
		hash, err := api.HashAPIKey(key, a.Config.APIKeyHashSecret)
		if err != nil {
			return invalidKeyIdentity()
		}
		record, account, ok, err := a.BillingStore.GetAPIKeyByHash(r.Context(), hash)
		if err != nil || !ok || !record.IsActive || !record.RevokedAt.IsZero() {
			return invalidKeyIdentity()
		}
		if !subscriptionAllowsPaidAccess(account.SubscriptionStatus) || normalizeBillingPlan(account.Plan) == "" {
			return api.RateIdentity{RejectStatus: http.StatusForbidden, RejectCode: "API_KEY_INACTIVE", RejectMessage: "API key subscription is not active."}
		}
		plan := normalizeBillingPlan(account.Plan)
		auth := api.BillingAuth{Plan: plan, AccountID: account.ID, KeyID: record.ID}
		*r = *r.WithContext(context.WithValue(r.Context(), api.ContextBillingAuthKey, auth))
		go a.BillingStore.TouchAPIKeyUsed(context.Background(), record.ID)
		limits := a.rateLimitInfo(plan)
		// The bucket is keyed by account, not key, so creating extra keys
		// never multiplies the paid rate limit.
		return api.RateIdentity{Bucket: "plan:" + plan + ":" + account.ID, Limit: limits.Limit, Burst: limits.Burst}
	})
}

func (a *App) rateLimitInfo(plan string) rateLimitInfo {
	plan = normalizeBillingPlan(plan)
	if plan == "" {
		plan = api.PlanFree
	}
	info := rateLimitInfo{
		Plan:     plan,
		Window:   "1 minute",
		SharedBy: "client_ip",
	}
	switch plan {
	case api.PlanDeveloper:
		info.Limit = a.Config.DeveloperRateLimitPerMinute
		info.SharedBy = "account"
		info.KeyLimit = a.maxActiveKeys(plan)
	case api.PlanPro:
		info.Limit = a.Config.ProRateLimitPerMinute
		info.SharedBy = "account"
		info.KeyLimit = a.maxActiveKeys(plan)
	default:
		info.Limit = a.Config.AnonymousRateLimitPerMinute
	}
	if info.Limit <= 0 {
		info.Limit = a.Config.AnonymousRateLimitPerMinute
	}
	info.Burst = maxBillingInt(info.Limit/10, a.Config.AnonymousRateBurst)
	if plan == api.PlanFree {
		info.Burst = a.Config.AnonymousRateBurst
	}
	if info.Burst <= 0 {
		info.Burst = 1
	}
	return info
}

func apiKeyFromRequest(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("X-API-Key")); value != "" {
		return value
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}

func invalidKeyIdentity() api.RateIdentity {
	return api.RateIdentity{RejectStatus: http.StatusUnauthorized, RejectCode: "INVALID_API_KEY", RejectMessage: "API key is invalid or revoked."}
}

func maxBillingInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
