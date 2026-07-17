package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/api"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/telemetry"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

func (a *App) billingStoreReady(w http.ResponseWriter, r *http.Request) bool {
	if a.BillingStore == nil {
		config.WriteError(w, r, http.StatusServiceUnavailable, "BILLING_DISABLED", "Billing store is not available.")
		return false
	}
	return true
}

func decodeAdminJSON(w http.ResponseWriter, r *http.Request, into any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(into); err != nil {
		config.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON.")
		return false
	}
	return true
}

// --- Users ---

func (a *App) adminUsersHandler(w http.ResponseWriter, r *http.Request) {
	if !a.billingStoreReady(w, r) {
		return
	}
	limit, offset := parsePagination(r.URL.Query())
	accounts, total, err := a.BillingStore.SearchAccounts(r.Context(),
		r.URL.Query().Get("q"), r.URL.Query().Get("status"), limit, offset)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "ADMIN_USERS_FAILED", "Failed to load users.")
		return
	}
	type userRow struct {
		api.AccountAdminView
		FirstSeenAt *time.Time `json:"firstSeenAt,omitempty"`
		LastSeenAt  *time.Time `json:"lastSeenAt,omitempty"`
		DaysActive  int64      `json:"daysActive"`
	}
	rows := make([]userRow, 0, len(accounts))
	for _, account := range accounts {
		row := userRow{AccountAdminView: account}
		if a.Telemetry != nil {
			if profile, ok := a.Telemetry.GetEntityProfile(r.Context(), "user", account.ID); ok {
				first, last := profile.FirstSeenAt, profile.LastSeenAt
				row.FirstSeenAt, row.LastSeenAt, row.DaysActive = &first, &last, profile.DaysActive
			}
		}
		rows = append(rows, row)
	}
	writeAdminJSON(w, map[string]any{"users": rows, "total": total})
}

func (a *App) adminUserDetailHandler(w http.ResponseWriter, r *http.Request) {
	if !a.billingStoreReady(w, r) {
		return
	}
	ctx := r.Context()
	accountID := strings.TrimSpace(mux.Vars(r)["id"])
	account, found, err := a.BillingStore.GetAccountAdmin(ctx, accountID)
	if err != nil || !found {
		config.WriteError(w, r, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "Account was not found.")
		return
	}
	keys, _ := a.BillingStore.ListKeysAdmin(ctx, accountID, 100)
	clients, _ := a.BillingStore.ListAPIClients(ctx, accountID, 100)
	out := map[string]any{
		"user":    account,
		"keys":    keys,
		"clients": clients,
		"limits":  a.rateLimitInfo(account.Plan),
	}
	if a.Telemetry != nil {
		from, to, _, _ := parseRange(r.URL.Query(), time.Now())
		filter := telemetry.RequestFilter{From: from, To: to, AccountID: accountID}
		usage, _ := a.Telemetry.Totals(ctx, filter)
		series, _ := a.Telemetry.Timeseries(ctx, filter, seriesInterval(from, to), "")
		recentErrors, _, _ := a.Telemetry.ListErrors(ctx, telemetry.ErrorFilter{From: from, To: to, AccountID: accountID}, 10, 0)
		countries, _ := a.Telemetry.TopBy(ctx, filter, "country", 10)
		networks, _ := a.Telemetry.TopBy(ctx, filter, "network", 10)
		routes, _ := a.Telemetry.TopBy(ctx, filter, "route", 10)
		out["usage"] = usage
		out["series"] = series
		out["recentErrors"] = recentErrors
		out["countries"] = countries
		out["networks"] = networks
		out["routes"] = routes
		if profile, ok := a.Telemetry.GetEntityProfile(ctx, "user", accountID); ok {
			out["profile"] = profile
		}
	}
	writeAdminJSON(w, out)
}

func (a *App) adminCreateUserHandler(w http.ResponseWriter, r *http.Request) {
	if !a.billingStoreReady(w, r) {
		return
	}
	var input struct {
		Email string `json:"email"`
		Plan  string `json:"plan"`
		Notes string `json:"notes"`
	}
	if !decodeAdminJSON(w, r, &input) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(input.Email))
	if email == "" || !strings.Contains(email, "@") {
		config.WriteError(w, r, http.StatusBadRequest, "INVALID_EMAIL", "A valid email is required.")
		return
	}
	if existing, ok, _ := a.BillingStore.GetAccountByEmail(r.Context(), email); ok {
		config.WriteError(w, r, http.StatusConflict, "ACCOUNT_EXISTS", "An account with this email already exists: "+existing.ID)
		return
	}
	account, err := a.BillingStore.UpsertAccount(r.Context(), api.Account{Email: email, Plan: strings.TrimSpace(input.Plan)})
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "ACCOUNT_CREATE_FAILED", "Failed to create account.")
		return
	}
	if strings.TrimSpace(input.Notes) != "" {
		notes := input.Notes
		_ = a.BillingStore.UpdateAccountAdmin(r.Context(), account.ID, api.AdminAccountUpdate{Notes: &notes})
	}
	a.audit(r, "user.create", "account", account.ID, map[string]string{"email": email, "plan": account.Plan})
	writeAdminJSON(w, map[string]any{"created": true, "id": account.ID})
}

func (a *App) adminUpdateUserHandler(w http.ResponseWriter, r *http.Request) {
	if !a.billingStoreReady(w, r) {
		return
	}
	accountID := strings.TrimSpace(mux.Vars(r)["id"])
	var input struct {
		Status     *string `json:"status"`
		Notes      *string `json:"notes"`
		Tags       *string `json:"tags"`
		IsInternal *bool   `json:"isInternal"`
		IsTest     *bool   `json:"isTest"`
		Plan       *string `json:"plan"`
	}
	if !decodeAdminJSON(w, r, &input) {
		return
	}
	// Plan and status changes are administrator-only; support may edit notes
	// and tags.
	identity := adminFromContext(r.Context())
	if (input.Status != nil || input.Plan != nil || input.IsInternal != nil || input.IsTest != nil) &&
		!telemetry.RoleAtLeast(identity.Role, telemetry.RoleAdministrator) {
		config.WriteError(w, r, http.StatusForbidden, "ADMIN_FORBIDDEN", "Only administrators may change status or plan.")
		return
	}
	if _, found, err := a.BillingStore.GetAccountAdmin(r.Context(), accountID); err != nil || !found {
		config.WriteError(w, r, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "Account was not found.")
		return
	}
	update := api.AdminAccountUpdate{
		Status: input.Status, Notes: input.Notes, Tags: input.Tags,
		IsInternal: input.IsInternal, IsTest: input.IsTest, Plan: input.Plan,
	}
	if err := a.BillingStore.UpdateAccountAdmin(r.Context(), accountID, update); err != nil {
		config.WriteError(w, r, http.StatusBadRequest, "ACCOUNT_UPDATE_FAILED", err.Error())
		return
	}
	a.audit(r, "user.update", "account", accountID, input)
	writeAdminJSON(w, map[string]bool{"updated": true})
}

func (a *App) adminDeleteUserHandler(w http.ResponseWriter, r *http.Request) {
	if !a.billingStoreReady(w, r) {
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
	a.audit(r, "user.delete", "account", accountID, map[string]any{"email": account.Email, "plan": account.Plan, "forced": force})
	zap.S().Infow("admin deleted account", "accountId", accountID, "plan", account.Plan, "forced", force)
	writeAdminJSON(w, map[string]bool{"deleted": true})
}

func (a *App) adminLoginLinkHandler(w http.ResponseWriter, r *http.Request) {
	if !a.billingStoreReady(w, r) {
		return
	}
	a.audit(r, "user.login_link", "account", mux.Vars(r)["id"], nil)
	a.internalSendLoginLinkHandler(w, r)
}

// --- API keys ---

func (a *App) adminKeysHandler(w http.ResponseWriter, r *http.Request) {
	if !a.billingStoreReady(w, r) {
		return
	}
	limit, _ := parsePagination(r.URL.Query())
	keys, err := a.BillingStore.ListKeysAdmin(r.Context(), r.URL.Query().Get("user"), limit)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "ADMIN_KEYS_FAILED", "Failed to load API keys.")
		return
	}
	writeAdminJSON(w, map[string]any{"keys": keys})
}

func (a *App) adminCreateKeyHandler(w http.ResponseWriter, r *http.Request) {
	if !a.billingStoreReady(w, r) {
		return
	}
	accountID := strings.TrimSpace(mux.Vars(r)["id"])
	account, found, err := a.BillingStore.GetAccountAdmin(r.Context(), accountID)
	if err != nil || !found {
		config.WriteError(w, r, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "Account was not found.")
		return
	}
	if account.Status != api.AccountStatusActive {
		config.WriteError(w, r, http.StatusConflict, "ACCOUNT_NOT_ACTIVE", "Keys can only be created for active accounts.")
		return
	}
	var input struct {
		Name        string `json:"name"`
		Environment string `json:"environment"`
		ClientID    string `json:"clientId"`
		Scopes      string `json:"scopes"`
		Quota       int64  `json:"monthlyQuota"`
		ExpiresAt   string `json:"expiresAt"` // YYYY-MM-DD, optional
	}
	if !decodeAdminJSON(w, r, &input) {
		return
	}
	mode := "test"
	if a.Config.AppEnv == "production" {
		mode = "live"
	}
	generated, err := api.GenerateAPIKey(mode, a.Config.APIKeyHashSecret)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "KEY_CREATE_FAILED", "Failed to generate API key.")
		return
	}
	record, err := a.BillingStore.CreateAPIKey(r.Context(), api.APIKeyRecord{
		AccountID: accountID,
		KeyHash:   generated.Hash,
		KeyPrefix: generated.Prefix,
		Name:      strings.TrimSpace(input.Name),
		Plan:      account.Plan,
		LastFour:  generated.LastFour,
	})
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "KEY_CREATE_FAILED", "Failed to store API key.")
		return
	}
	var expires *time.Time
	if strings.TrimSpace(input.ExpiresAt) != "" {
		parsed := parseDay(input.ExpiresAt, time.Time{})
		if !parsed.IsZero() {
			expires = &parsed
		}
	}
	environment := input.Environment
	if environment == "" {
		environment = api.EnvProduction
	}
	if err := a.BillingStore.SetKeyMetadata(r.Context(), record.ID, nil, &environment, &input.Scopes, &input.Quota, expires, nil, &input.ClientID); err != nil {
		config.WriteError(w, r, http.StatusBadRequest, "KEY_METADATA_FAILED", err.Error())
		return
	}
	a.audit(r, "key.create", "api_key", record.ID, map[string]any{
		"accountId": accountID, "name": input.Name, "environment": environment, "clientId": input.ClientID,
	})
	// The raw secret is returned exactly once and never stored.
	writeAdminJSON(w, map[string]any{
		"apiKey":  generated.Raw,
		"keyId":   record.ID,
		"prefix":  generated.Prefix,
		"message": "Store this API key now. It will only be shown once.",
	})
}

func (a *App) adminUpdateKeyHandler(w http.ResponseWriter, r *http.Request) {
	if !a.billingStoreReady(w, r) {
		return
	}
	keyID := strings.TrimSpace(mux.Vars(r)["keyId"])
	var input struct {
		Disabled    *bool   `json:"disabled"`
		Name        *string `json:"name"`
		Environment *string `json:"environment"`
		Scopes      *string `json:"scopes"`
		Quota       *int64  `json:"monthlyQuota"`
		ExpiresAt   *string `json:"expiresAt"` // "" clears
		Notes       *string `json:"notes"`
		ClientID    *string `json:"clientId"`
	}
	if !decodeAdminJSON(w, r, &input) {
		return
	}
	if input.Disabled != nil {
		if err := a.BillingStore.SetKeyDisabled(r.Context(), keyID, *input.Disabled); err != nil {
			config.WriteError(w, r, http.StatusInternalServerError, "KEY_UPDATE_FAILED", "Failed to update key.")
			return
		}
	}
	var expires *time.Time
	if input.ExpiresAt != nil {
		parsed := parseDay(*input.ExpiresAt, time.Time{})
		expires = &parsed // zero clears the expiry
	}
	if err := a.BillingStore.SetKeyMetadata(r.Context(), keyID, input.Name, input.Environment, input.Scopes, input.Quota, expires, input.Notes, input.ClientID); err != nil {
		config.WriteError(w, r, http.StatusBadRequest, "KEY_UPDATE_FAILED", err.Error())
		return
	}
	a.audit(r, "key.update", "api_key", keyID, input)
	writeAdminJSON(w, map[string]bool{"updated": true})
}

func (a *App) adminRevokeKeyHandler(w http.ResponseWriter, r *http.Request) {
	if !a.billingStoreReady(w, r) {
		return
	}
	keyID := strings.TrimSpace(mux.Vars(r)["keyId"])
	var input struct {
		AccountID string `json:"accountId"`
		Reason    string `json:"reason"`
		Notify    bool   `json:"notify"`
	}
	if !decodeAdminJSON(w, r, &input) {
		return
	}
	accountID := strings.TrimSpace(input.AccountID)
	if accountID == "" {
		config.WriteError(w, r, http.StatusBadRequest, "INVALID_KEY", "accountId is required.")
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
	a.audit(r, "key.revoke", "api_key", keyID, map[string]string{"accountId": accountID, "reason": input.Reason})
	emailed := false
	if input.Notify && a.Mailer.Configured() {
		if account, ok, _ := a.BillingStore.GetAccount(r.Context(), accountID); ok && strings.TrimSpace(account.Email) != "" {
			emailed = true
			a.sendKeyRevokedEmail(account.Email, key, input.Reason)
		}
	}
	writeAdminJSON(w, map[string]any{"revoked": true, "emailed": emailed})
}

// --- Registered API clients ---

func (a *App) adminRegisteredClientsHandler(w http.ResponseWriter, r *http.Request) {
	if !a.billingStoreReady(w, r) {
		return
	}
	clients, err := a.BillingStore.ListAPIClients(r.Context(), r.URL.Query().Get("user"), 500)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "CLIENTS_FAILED", "Failed to load clients.")
		return
	}
	writeAdminJSON(w, map[string]any{"clients": clients})
}

func (a *App) adminCreateClientHandler(w http.ResponseWriter, r *http.Request) {
	if !a.billingStoreReady(w, r) {
		return
	}
	var input api.APIClient
	if !decodeAdminJSON(w, r, &input) {
		return
	}
	client, err := a.BillingStore.CreateAPIClient(r.Context(), input)
	if err != nil {
		config.WriteError(w, r, http.StatusBadRequest, "CLIENT_CREATE_FAILED", err.Error())
		return
	}
	a.audit(r, "client.create", "api_client", client.ID, map[string]string{"name": client.Name, "accountId": client.AccountID})
	writeAdminJSON(w, map[string]any{"created": true, "client": client})
}

func (a *App) adminUpdateClientHandler(w http.ResponseWriter, r *http.Request) {
	if !a.billingStoreReady(w, r) {
		return
	}
	clientID := strings.TrimSpace(mux.Vars(r)["id"])
	var fields map[string]any
	if !decodeAdminJSON(w, r, &fields) {
		return
	}
	if err := a.BillingStore.UpdateAPIClient(r.Context(), clientID, fields); err != nil {
		config.WriteError(w, r, http.StatusBadRequest, "CLIENT_UPDATE_FAILED", err.Error())
		return
	}
	a.audit(r, "client.update", "api_client", clientID, fields)
	writeAdminJSON(w, map[string]bool{"updated": true})
}

func (a *App) adminDeleteClientHandler(w http.ResponseWriter, r *http.Request) {
	if !a.billingStoreReady(w, r) {
		return
	}
	clientID := strings.TrimSpace(mux.Vars(r)["id"])
	if err := a.BillingStore.DeleteAPIClient(r.Context(), clientID); err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "CLIENT_DELETE_FAILED", "Failed to delete client.")
		return
	}
	a.audit(r, "client.delete", "api_client", clientID, nil)
	writeAdminJSON(w, map[string]bool{"deleted": true})
}

// --- Internal (service) API keys ---

func (a *App) internalAPIKeysListHandler(w http.ResponseWriter, r *http.Request) {
	a.internalAPIKeysHandler(w, r)
}

func (a *App) createInternalAPIKeyJSONHandler(w http.ResponseWriter, r *http.Request) {
	a.audit(r, "internal_key.create", "internal_api_key", "", nil)
	a.createInternalAPIKeyHandler(w, r)
}

func (a *App) deleteInternalAPIKeyJSONHandler(w http.ResponseWriter, r *http.Request) {
	a.audit(r, "internal_key.revoke", "internal_api_key", mux.Vars(r)["id"], nil)
	a.deleteInternalAPIKeyHandler(w, r)
}

// --- Admin users (panel accounts) ---

func (a *App) adminUsersListHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	users, err := a.Telemetry.ListAdminUsers(r.Context())
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "ADMIN_ACCOUNTS_FAILED", "Failed to load admin users.")
		return
	}
	writeAdminJSON(w, map[string]any{"adminUsers": users, "envAdminConfigured": a.internalAdminConfigured()})
}

func (a *App) adminUserCreateHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if !decodeAdminJSON(w, r, &input) {
		return
	}
	identity := adminFromContext(r.Context())
	user, err := a.Telemetry.CreateAdminUser(r.Context(), input.Username, input.Password, input.Role, identity.Username)
	if err != nil {
		config.WriteError(w, r, http.StatusBadRequest, "ADMIN_ACCOUNT_CREATE_FAILED", err.Error())
		return
	}
	a.audit(r, "admin_user.create", "admin_user", user.ID, map[string]string{"username": user.Username, "role": user.Role})
	writeAdminJSON(w, map[string]any{"created": true, "user": user})
}

func (a *App) adminUserUpdateHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	id := mux.Vars(r)["id"]
	var input struct {
		Role     string `json:"role"`
		Disabled *bool  `json:"disabled"`
		Password string `json:"password"`
	}
	if !decodeAdminJSON(w, r, &input) {
		return
	}
	if err := a.Telemetry.UpdateAdminUser(r.Context(), id, input.Role, input.Disabled, input.Password); err != nil {
		config.WriteError(w, r, http.StatusBadRequest, "ADMIN_ACCOUNT_UPDATE_FAILED", err.Error())
		return
	}
	a.audit(r, "admin_user.update", "admin_user", id, map[string]any{
		"role": input.Role, "disabled": input.Disabled, "passwordChanged": input.Password != "",
	})
	writeAdminJSON(w, map[string]bool{"updated": true})
}

func (a *App) adminUserDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	id := mux.Vars(r)["id"]
	if err := a.Telemetry.DeleteAdminUser(r.Context(), id); err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "ADMIN_ACCOUNT_DELETE_FAILED", "Failed to delete admin user.")
		return
	}
	a.audit(r, "admin_user.delete", "admin_user", id, nil)
	writeAdminJSON(w, map[string]bool{"deleted": true})
}

// --- Settings & operations ---

// editableSettings lists the runtime-tunable settings; everything else is
// environment configuration.
var editableSettings = map[string]struct{}{
	"slow_request_ms": {},
}

func (a *App) adminSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	writeAdminJSON(w, map[string]any{
		"settings": a.Telemetry.Settings(r.Context()),
		"config": map[string]any{
			"telemetryDbPath":      a.Config.TelemetryDBPath,
			"rawRetentionDays":     a.Config.TelemetryRawRetentionDays,
			"hourlyRetentionDays":  a.Config.TelemetryHourlyRetention,
			"logRetentionDays":     a.Config.TelemetryLogRetentionDays,
			"errorRetentionDays":   a.Config.TelemetryErrRetentionDays,
			"anonIdRotation":       a.Config.AnonIDRotation,
			"slowRequestMsDefault": a.Config.TelemetrySlowRequestMs,
			"timezone":             a.Config.MetricsTimezone,
			"instance":             a.Config.InstanceID,
		},
		"editable":    []string{"slow_request_ms"},
		"aggregation": a.Aggregator.State(r.Context()),
	})
}

func (a *App) adminSettingsUpdateHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	var input map[string]string
	if !decodeAdminJSON(w, r, &input) {
		return
	}
	identity := adminFromContext(r.Context())
	for key, value := range input {
		if _, ok := editableSettings[key]; !ok {
			config.WriteError(w, r, http.StatusBadRequest, "UNKNOWN_SETTING", "Unknown setting: "+key)
			return
		}
		if err := a.Telemetry.PutSetting(r.Context(), key, strings.TrimSpace(value), identity.Username); err != nil {
			config.WriteError(w, r, http.StatusInternalServerError, "SETTING_UPDATE_FAILED", "Failed to update setting.")
			return
		}
	}
	a.audit(r, "settings.update", "settings", "", input)
	writeAdminJSON(w, map[string]bool{"updated": true})
}

func (a *App) adminImportLogsHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	var input struct {
		DryRun bool   `json:"dryRun"`
		From   string `json:"from"`
		To     string `json:"to"`
		Fresh  bool   `json:"fresh"`
	}
	if r.ContentLength > 0 && !decodeAdminJSON(w, r, &input) {
		return
	}
	importer := telemetry.NewImporter(a.Telemetry, telemetry.ImporterConfig{
		HashSecret: firstNonEmptyString(a.Config.TelemetryHashSecret, a.Config.APIKeyHashSecret),
		Rotation:   a.Config.AnonIDRotation,
	})
	summary, err := importer.ImportDir(r.Context(), a.Config.LogDir, telemetry.ImportOptions{
		DryRun:  input.DryRun,
		FromDay: input.From,
		ToDay:   input.To,
		Fresh:   input.Fresh,
	})
	if err != nil {
		zap.S().Warnw("historical log import failed", "error", err)
		config.WriteError(w, r, http.StatusInternalServerError, "IMPORT_FAILED", "Historical log import failed: "+err.Error())
		return
	}
	aggregated := false
	if !input.DryRun && !summary.FirstEventAt.IsZero() {
		a.aggMu.Lock()
		err := a.Aggregator.RebuildRange(r.Context(), summary.FirstEventAt, summary.LastEventAt)
		a.aggMu.Unlock()
		if err != nil {
			zap.S().Warnw("post-import aggregation rebuild failed", "error", err)
			config.WriteError(w, r, http.StatusInternalServerError, "IMPORT_AGGREGATION_FAILED", "Logs imported, but aggregate rebuild failed: "+err.Error())
			return
		}
		aggregated = true
	}
	a.audit(r, "telemetry.import_logs", "import", "", input)
	writeAdminJSON(w, map[string]any{
		"import":      summary,
		"aggregated":  aggregated,
		"aggregation": a.Aggregator.State(r.Context()),
	})
}

func (a *App) adminRebuildHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	a.aggMu.Lock()
	defer a.aggMu.Unlock()
	if err := a.Aggregator.RebuildAll(r.Context()); err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "REBUILD_FAILED", "Aggregate rebuild failed: "+err.Error())
		return
	}
	a.audit(r, "telemetry.rebuild_aggregates", "aggregates", "", nil)
	writeAdminJSON(w, map[string]any{"rebuilt": true, "aggregation": a.Aggregator.State(r.Context())})
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
