package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

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
		annotations := api.AnnotationsFromContext(r.Context())
		key := apiKeyFromRequest(r)
		if key == "" || a.BillingStore == nil {
			return api.RateIdentity{Bucket: "plan:free:" + clientIP, Limit: a.Config.AnonymousRateLimitPerMinute, Burst: a.Config.AnonymousRateBurst}
		}
		hash, err := api.HashAPIKey(key, a.Config.APIKeyHashSecret)
		if err != nil {
			return invalidKeyIdentity()
		}
		internalKey, ok, err := a.BillingStore.GetInternalAPIKeyByHash(r.Context(), hash)
		if err == nil && ok {
			if !internalKey.IsActive || internalKey.RevokedAt != nil {
				return invalidKeyIdentity()
			}
			auth := api.BillingAuth{Plan: api.PlanInternal, AccountID: "internal", KeyID: internalKey.ID}
			*r = *r.WithContext(context.WithValue(r.Context(), api.ContextBillingAuthKey, auth))
			annotations.SetAuth("internal_key", "internal", internalKey.ID, "", api.PlanInternal, true)
			go a.BillingStore.TouchInternalAPIKeyUsed(context.Background(), internalKey.ID)
			limits := a.rateLimitInfo(api.PlanInternal)
			return api.RateIdentity{Bucket: "plan:" + api.PlanInternal + ":" + internalKey.ID, Limit: limits.Limit, Burst: limits.Burst}
		}
		record, account, clientID, disabledAt, expiresAt, accountStatus, ok, err := a.BillingStore.GetAPIKeyAuth(r.Context(), hash)
		if err != nil || !ok {
			return invalidKeyIdentity()
		}
		if usable, reason := api.KeyUsable(record, disabledAt, expiresAt, accountStatus, timeNow()); !usable {
			switch reason {
			case "account_suspended", "account_deleted":
				return api.RateIdentity{RejectStatus: http.StatusForbidden, RejectCode: "ACCOUNT_SUSPENDED", RejectMessage: "Account is suspended."}
			case "expired":
				return api.RateIdentity{RejectStatus: http.StatusUnauthorized, RejectCode: "API_KEY_EXPIRED", RejectMessage: "API key has expired."}
			case "disabled":
				return api.RateIdentity{RejectStatus: http.StatusUnauthorized, RejectCode: "API_KEY_DISABLED", RejectMessage: "API key is temporarily disabled."}
			default:
				return invalidKeyIdentity()
			}
		}
		if !subscriptionAllowsPaidAccess(account.SubscriptionStatus) || normalizeBillingPlan(account.Plan) == "" {
			return api.RateIdentity{RejectStatus: http.StatusForbidden, RejectCode: "API_KEY_INACTIVE", RejectMessage: "API key subscription is not active."}
		}
		plan := normalizeBillingPlan(account.Plan)
		auth := api.BillingAuth{Plan: plan, AccountID: account.ID, KeyID: record.ID, ClientID: clientID}
		*r = *r.WithContext(context.WithValue(r.Context(), api.ContextBillingAuthKey, auth))
		annotations.SetAuth("api_key", account.ID, record.ID, clientID, plan, clientVerifiedFor(r, clientID))
		go a.BillingStore.TouchAPIKeyUsedAt(context.Background(), record.ID)
		limits := a.rateLimitInfo(plan)
		// The bucket is keyed by account, not key, so creating extra keys
		// never multiplies the paid rate limit.
		return api.RateIdentity{Bucket: "plan:" + plan + ":" + account.ID, Limit: limits.Limit, Burst: limits.Burst}
	})
}

// clientVerifiedFor treats a self-reported client name as verified only when
// the authenticated key belongs to a registered client (headers alone can be
// spoofed).
func clientVerifiedFor(r *http.Request, clientID string) bool {
	return clientID != ""
}

func timeNow() time.Time { return time.Now().UTC() }

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
	case api.PlanInternal:
		info.Limit = a.Config.InternalRateLimitPerMinute
		info.SharedBy = "internal_key"
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
