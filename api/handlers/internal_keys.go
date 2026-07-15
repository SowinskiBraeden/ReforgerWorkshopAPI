package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/api"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
	"github.com/gorilla/mux"
)

type internalAPIKeyCreateRequest struct {
	Name string `json:"name"`
}

func (a *App) internalAPIKeysHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if !a.Config.InternalMetricsEnabled {
		config.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "Not found.")
		return
	}
	if !a.internalAdminAllowed(r) {
		writeAdminUnauthorized(w, r)
		return
	}
	if a.BillingStore == nil {
		config.WriteError(w, r, http.StatusServiceUnavailable, "BILLING_DISABLED", "Billing store is not available.")
		return
	}
	keys, err := a.BillingStore.InternalAPIKeys(r.Context())
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_KEYS_FAILED", "Failed to load internal API keys.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"api_keys":   keys,
		"rate_limit": a.rateLimitInfo(api.PlanInternal),
	})
}

func (a *App) createInternalAPIKeyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if !a.Config.InternalMetricsEnabled {
		config.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "Not found.")
		return
	}
	if !a.internalAdminAllowed(r) {
		writeAdminUnauthorized(w, r)
		return
	}
	if a.BillingStore == nil {
		config.WriteError(w, r, http.StatusServiceUnavailable, "BILLING_DISABLED", "Billing store is not available.")
		return
	}
	var input internalAPIKeyCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		config.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON.")
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = "Internal"
	}
	if len(name) > 80 {
		name = name[:80]
	}
	raw, key, err := a.createInternalAPIKey(r.Context(), name)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_KEY_CREATE_FAILED", "Failed to create internal API key.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"api_key":        raw,
		"api_key_prefix": key.KeyPrefix,
		"message":        "Store this internal API key now. It will only be shown once.",
	})
}

func (a *App) deleteInternalAPIKeyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if !a.Config.InternalMetricsEnabled {
		config.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "Not found.")
		return
	}
	if !a.internalAdminAllowed(r) {
		writeAdminUnauthorized(w, r)
		return
	}
	if a.BillingStore == nil {
		config.WriteError(w, r, http.StatusServiceUnavailable, "BILLING_DISABLED", "Billing store is not available.")
		return
	}
	keyID := strings.TrimSpace(mux.Vars(r)["id"])
	if keyID == "" {
		config.WriteError(w, r, http.StatusBadRequest, "INVALID_INTERNAL_KEY", "Internal API key id is required.")
		return
	}
	if err := a.BillingStore.RevokeInternalAPIKey(r.Context(), keyID); err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_KEY_REVOKE_FAILED", "Failed to revoke internal API key.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]bool{"revoked": true})
}

func (a *App) createInternalAPIKey(ctx context.Context, name string) (string, api.InternalAPIKeyRecord, error) {
	mode := "test"
	if a.Config.AppEnv == "production" || strings.HasPrefix(a.Config.StripeSecretKey, "sk_live_") {
		mode = "live"
	}
	generated, err := api.GenerateAPIKey(mode, a.Config.APIKeyHashSecret)
	if err != nil {
		return "", api.InternalAPIKeyRecord{}, err
	}
	key, err := a.BillingStore.CreateInternalAPIKey(ctx, api.InternalAPIKeyRecord{
		KeyHash:   generated.Hash,
		KeyPrefix: generated.Prefix,
		Name:      name,
		LastFour:  generated.LastFour,
	})
	if err != nil {
		return "", key, fmt.Errorf("create internal key: %w", err)
	}
	return generated.Raw, key, nil
}
