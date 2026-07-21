package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/api"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/telemetry"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

const internalAdminCookie = "rfm_internal_admin"
const adminSessionTTL = 12 * time.Hour

// adminIdentity is the authenticated admin for a request.
type adminIdentity struct {
	Username string
	Role     string
}

// registerAdminRoutes mounts the internal admin panel and its JSON API.
// Every handler goes through requireAdmin (session + role check, server-side)
// and mutating handlers additionally require the X-Admin-CSRF header.
func (a *App) registerAdminRoutes(router *mux.Router) {
	router.HandleFunc("/internal/login", a.internalLoginHandler).Methods("POST")
	router.HandleFunc("/internal/logout", a.internalLogoutHandler).Methods("POST")
	router.HandleFunc("/internal/admin/", a.adminPanelHandler).Methods("GET")
	router.HandleFunc("/internal/admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/internal/admin/", http.StatusMovedPermanently)
	}).Methods("GET")
	// Old panel path kept as a redirect.
	router.HandleFunc("/internal/metrics/panel", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/internal/admin/", http.StatusMovedPermanently)
	}).Methods("GET")

	get := func(path string, minRole string, handler http.HandlerFunc) {
		router.HandleFunc(path, a.requireAdmin(minRole, false, handler)).Methods("GET")
	}
	mutate := func(path string, method string, minRole string, handler http.HandlerFunc) {
		router.HandleFunc(path, a.requireAdmin(minRole, true, handler)).Methods(method)
	}

	// Session info for the SPA.
	get("/internal/api/session", telemetry.RoleViewer, a.adminSessionHandler)

	// Analytics (read-only).
	get("/internal/api/overview", telemetry.RoleViewer, a.adminOverviewHandler)
	get("/internal/api/realtime", telemetry.RoleViewer, a.adminRealtimeHandler)
	get("/internal/api/timeseries", telemetry.RoleViewer, a.adminTimeseriesHandler)
	get("/internal/api/requests", telemetry.RoleViewer, a.adminRequestsHandler)
	get("/internal/api/requests/{id}", telemetry.RoleViewer, a.adminRequestDetailHandler)
	get("/internal/api/endpoints", telemetry.RoleViewer, a.adminEndpointsHandler)
	get("/internal/api/performance", telemetry.RoleViewer, a.adminPerformanceHandler)
	get("/internal/api/cache", telemetry.RoleViewer, a.adminCacheHandler)
	get("/internal/api/errors", telemetry.RoleViewer, a.adminErrorsHandler)
	get("/internal/api/errors/{id}", telemetry.RoleViewer, a.adminErrorDetailHandler)
	mutate("/internal/api/errors/{id}", "PATCH", telemetry.RoleOperator, a.adminErrorUpdateHandler)
	get("/internal/api/rate-limits", telemetry.RoleViewer, a.adminRateLimitsHandler)
	get("/internal/api/geography", telemetry.RoleViewer, a.adminGeographyHandler)
	get("/internal/api/networks", telemetry.RoleViewer, a.adminNetworksHandler)
	get("/internal/api/search-analytics", telemetry.RoleViewer, a.adminSearchAnalyticsHandler)
	get("/internal/api/logs", telemetry.RoleViewer, a.adminLogsHandler)
	get("/internal/api/logs/{id}", telemetry.RoleViewer, a.adminLogDetailHandler)
	get("/internal/api/jobs", telemetry.RoleViewer, a.adminJobsHandler)
	get("/internal/api/jobs/{id}", telemetry.RoleViewer, a.adminJobDetailHandler)
	get("/internal/api/retention", telemetry.RoleViewer, a.adminRetentionHandler)
	get("/internal/api/clients", telemetry.RoleViewer, a.adminClientsHandler)
	get("/internal/api/marketing", telemetry.RoleViewer, a.adminMarketingHandler)
	get("/internal/api/export", telemetry.RoleViewer, a.adminExportHandler)
	get("/internal/api/health", telemetry.RoleViewer, a.adminHealthHandler)
	get("/internal/api/audit", telemetry.RoleSupport, a.adminAuditHandler)

	// User / key / client management.
	get("/internal/api/users", telemetry.RoleSupport, a.adminUsersHandler)
	get("/internal/api/users/{id}", telemetry.RoleSupport, a.adminUserDetailHandler)
	mutate("/internal/api/users", "POST", telemetry.RoleAdministrator, a.adminCreateUserHandler)
	mutate("/internal/api/users/{id}", "PATCH", telemetry.RoleSupport, a.adminUpdateUserHandler)
	mutate("/internal/api/users/{id}", "DELETE", telemetry.RoleAdministrator, a.adminDeleteUserHandler)
	mutate("/internal/api/users/{id}/login-link", "POST", telemetry.RoleSupport, a.adminLoginLinkHandler)
	mutate("/internal/api/users/{id}/keys", "POST", telemetry.RoleAdministrator, a.adminCreateKeyHandler)
	get("/internal/api/keys", telemetry.RoleSupport, a.adminKeysHandler)
	mutate("/internal/api/keys/{keyId}", "PATCH", telemetry.RoleAdministrator, a.adminUpdateKeyHandler)
	mutate("/internal/api/keys/{keyId}/revoke", "POST", telemetry.RoleAdministrator, a.adminRevokeKeyHandler)
	mutate("/internal/api/clients", "POST", telemetry.RoleAdministrator, a.adminCreateClientHandler)
	get("/internal/api/registered-clients", telemetry.RoleSupport, a.adminRegisteredClientsHandler)
	mutate("/internal/api/clients/{id}", "PATCH", telemetry.RoleAdministrator, a.adminUpdateClientHandler)
	mutate("/internal/api/clients/{id}", "DELETE", telemetry.RoleAdministrator, a.adminDeleteClientHandler)

	// Internal API keys (service-to-service).
	get("/internal/api/internal-keys", telemetry.RoleOperator, a.internalAPIKeysListHandler)
	mutate("/internal/api/internal-keys", "POST", telemetry.RoleAdministrator, a.createInternalAPIKeyJSONHandler)
	mutate("/internal/api/internal-keys/{id}", "DELETE", telemetry.RoleAdministrator, a.deleteInternalAPIKeyJSONHandler)

	// Admin accounts + settings + operations.
	get("/internal/api/admin-users", telemetry.RoleAdministrator, a.adminUsersListHandler)
	mutate("/internal/api/admin-users", "POST", telemetry.RoleAdministrator, a.adminUserCreateHandler)
	mutate("/internal/api/admin-users/{id}", "PATCH", telemetry.RoleAdministrator, a.adminUserUpdateHandler)
	mutate("/internal/api/admin-users/{id}", "DELETE", telemetry.RoleAdministrator, a.adminUserDeleteHandler)
	get("/internal/api/settings", telemetry.RoleViewer, a.adminSettingsHandler)
	mutate("/internal/api/settings", "PATCH", telemetry.RoleAdministrator, a.adminSettingsUpdateHandler)
	mutate("/internal/api/import-logs", "POST", telemetry.RoleOperator, a.adminImportLogsHandler)
	mutate("/internal/api/rebuild-aggregates", "POST", telemetry.RoleOperator, a.adminRebuildHandler)

	// Legacy JSON endpoints kept for compatibility with existing tooling.
	// The SameSite=Strict session cookie protects these; the X-Admin-CSRF
	// header is only demanded on the new /internal/api routes the SPA uses.
	legacy := func(minRole string, handler http.HandlerFunc) http.HandlerFunc {
		return a.requireAdmin(minRole, false, handler)
	}
	router.HandleFunc("/internal/metrics", legacy(telemetry.RoleViewer, a.adminOverviewHandler)).Methods("GET")
	router.HandleFunc("/internal/metrics/import-logs", legacy(telemetry.RoleOperator, a.adminImportLogsHandler)).Methods("POST")
	router.HandleFunc("/internal/admin/users", legacy(telemetry.RoleSupport, a.adminUsersHandler)).Methods("GET")
	router.HandleFunc("/internal/admin/users/{id}", legacy(telemetry.RoleAdministrator, a.adminDeleteUserHandler)).Methods("DELETE")
	router.HandleFunc("/internal/admin/users/{id}/login-link", legacy(telemetry.RoleSupport, a.adminLoginLinkHandler)).Methods("POST")
	router.HandleFunc("/internal/admin/users/{id}/keys/{keyId}/revoke", legacy(telemetry.RoleAdministrator, a.internalRevokeUserKeyHandler)).Methods("POST")
	router.HandleFunc("/internal/admin/api-keys", legacy(telemetry.RoleOperator, a.internalAPIKeysHandler)).Methods("GET")
	router.HandleFunc("/internal/admin/api-keys", legacy(telemetry.RoleAdministrator, a.createInternalAPIKeyJSONHandler)).Methods("POST")
	router.HandleFunc("/internal/admin/api-keys/{id}", legacy(telemetry.RoleAdministrator, a.deleteInternalAPIKeyJSONHandler)).Methods("DELETE")
}

// requireAdmin enforces panel enablement, a valid session, minimum role, and
// (for mutations) the CSRF header. Checks are entirely server-side.
func (a *App) requireAdmin(minRole string, mutating bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
		w.Header().Set("Cache-Control", "no-store")
		if !a.Config.InternalMetricsEnabled {
			config.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "Not found.")
			return
		}
		identity, ok := a.adminIdentityFromRequest(r)
		if !ok {
			writeAdminUnauthorized(w, r)
			return
		}
		if !telemetry.RoleAtLeast(identity.Role, minRole) {
			config.WriteError(w, r, http.StatusForbidden, "ADMIN_FORBIDDEN", "Your role does not allow this action.")
			return
		}
		if mutating && strings.TrimSpace(r.Header.Get("X-Admin-CSRF")) == "" {
			config.WriteError(w, r, http.StatusForbidden, "CSRF_REQUIRED", "Missing CSRF header.")
			return
		}
		ctx := context.WithValue(r.Context(), adminIdentityKey{}, identity)
		next(w, r.WithContext(ctx))
	}
}

type adminIdentityKey struct{}

func adminFromContext(ctx context.Context) adminIdentity {
	identity, _ := ctx.Value(adminIdentityKey{}).(adminIdentity)
	return identity
}

// audit records an admin action; failures are logged but never block.
func (a *App) audit(r *http.Request, action string, targetType string, targetID string, details any) {
	if a.Telemetry == nil {
		return
	}
	identity := adminFromContext(r.Context())
	event := telemetry.AuditEvent{
		Actor:      identity.Username,
		ActorRole:  identity.Role,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		RequestID:  r.Header.Get("X-Request-Id"),
	}
	if details != nil {
		event.Details = telemetry.AuditDetails(details)
	}
	if err := a.Telemetry.RecordAudit(r.Context(), event); err != nil {
		zap.S().Warnw("audit event write failed", "action", action, "error", err)
	}
}

func (a *App) adminIdentityFromRequest(r *http.Request) (adminIdentity, bool) {
	cookie, err := r.Cookie(internalAdminCookie)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return adminIdentity{}, false
	}
	subject, ok := api.VerifyAccountSessionToken(cookie.Value, a.internalAdminSessionSecret(), time.Now())
	if !ok {
		return adminIdentity{}, false
	}
	parts := strings.Split(subject, "|")
	if len(parts) != 3 || parts[0] != "admin" || !telemetry.ValidRole(parts[2]) {
		return adminIdentity{}, false
	}
	return adminIdentity{Username: parts[1], Role: parts[2]}, true
}

// loginThrottle limits admin sign-in attempts per client address. The
// address is used transiently in memory only (never stored or logged),
// consistent with the privacy model.
type loginThrottle struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

var adminLoginThrottle = &loginThrottle{attempts: map[string][]time.Time{}}

const (
	loginAttemptsPerWindow = 5
	loginWindow            = time.Minute
)

// allow records one attempt for the key and reports whether it is within the
// sliding window. Stale entries are pruned lazily so the map stays bounded.
func (l *loginThrottle) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.attempts) > 10000 {
		for existing, times := range l.attempts {
			if len(times) == 0 || now.Sub(times[len(times)-1]) > loginWindow {
				delete(l.attempts, existing)
			}
		}
	}
	recent := l.attempts[key][:0]
	for _, at := range l.attempts[key] {
		if now.Sub(at) < loginWindow {
			recent = append(recent, at)
		}
	}
	if len(recent) >= loginAttemptsPerWindow {
		l.attempts[key] = recent
		return false
	}
	l.attempts[key] = append(recent, now)
	return true
}

func (a *App) internalLoginHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if !a.Config.InternalMetricsEnabled {
		config.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "Not found.")
		return
	}
	clientIP := ""
	if a.Middleware != nil {
		clientIP = a.Middleware.ClientIP(r)
	}
	if !adminLoginThrottle.allow(clientIP, time.Now()) {
		w.Header().Set("Retry-After", "60")
		zap.S().Warnw("admin login throttled")
		config.WriteError(w, r, http.StatusTooManyRequests, "LOGIN_RATE_LIMITED", "Too many sign-in attempts. Try again in a minute.")
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&input); err != nil {
		config.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON.")
		return
	}
	identity, ok := a.authenticateAdmin(r.Context(), input.Username, input.Password)
	if !ok {
		zap.S().Infow("admin login failed", "username", telemetry.SanitizeText(input.Username, 40))
		config.WriteError(w, r, http.StatusUnauthorized, "INVALID_ADMIN_LOGIN", "Invalid username or password.")
		return
	}
	expires := time.Now().Add(adminSessionTTL)
	subject := strings.Join([]string{"admin", identity.Username, identity.Role}, "|")
	http.SetCookie(w, &http.Cookie{
		Name:     internalAdminCookie,
		Value:    api.CreateAccountSessionToken(subject, expires, a.internalAdminSessionSecret()),
		Path:     "/internal",
		Expires:  expires,
		HttpOnly: true,
		Secure:   a.adminCookieSecure(),
		SameSite: http.SameSiteStrictMode,
	})
	zap.S().Infow("admin login", "username", identity.Username, "role", identity.Role)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"authenticated": true, "username": identity.Username, "role": identity.Role})
}

// authenticateAdmin accepts the env bootstrap account (always administrator)
// or a DB-backed admin user.
func (a *App) authenticateAdmin(ctx context.Context, username string, password string) (adminIdentity, bool) {
	if a.internalAdminConfigured() &&
		constantTimeEqual(strings.TrimSpace(username), strings.TrimSpace(a.Config.InternalAdminUsername)) &&
		constantTimeEqual(password, a.Config.InternalAdminPassword) {
		return adminIdentity{Username: strings.TrimSpace(username), Role: telemetry.RoleAdministrator}, true
	}
	if a.Telemetry != nil {
		if user, ok := a.Telemetry.AuthenticateAdminUser(ctx, username, password); ok {
			return adminIdentity{Username: user.Username, Role: user.Role}, true
		}
	}
	return adminIdentity{}, false
}

func (a *App) internalLogoutHandler(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     internalAdminCookie,
		Value:    "",
		Path:     "/internal",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.adminCookieSecure(),
		SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"signed_out": true})
}

func (a *App) adminSessionHandler(w http.ResponseWriter, r *http.Request) {
	identity := adminFromContext(r.Context())
	writeAdminJSON(w, map[string]any{
		"username": identity.Username,
		"role":     identity.Role,
		"version":  Version,
		"timezone": a.Config.MetricsTimezone,
	})
}

// bootstrapAdminUsers logs guidance when no DB-backed admin users exist yet.
func (a *App) bootstrapAdminUsers() {
	if a.Telemetry == nil {
		return
	}
	users, err := a.Telemetry.ListAdminUsers(context.Background())
	if err == nil && len(users) == 0 && a.internalAdminConfigured() {
		zap.S().Infow("no database admin users exist yet; the INTERNAL_ADMIN_USERNAME env account acts as administrator and can create users in Settings")
	}
}

func (a *App) adminPanelHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	w.Header().Set("Cache-Control", "no-store")
	if !a.Config.InternalMetricsEnabled {
		config.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "Not found.")
		return
	}
	if _, ok := a.adminIdentityFromRequest(r); !ok {
		writeInternalLoginPage(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	body, err := os.ReadFile(adminPanelPath())
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "ADMIN_PANEL_UNAVAILABLE", "Admin panel is unavailable.")
		return
	}
	html := strings.ReplaceAll(string(body), "{{ADMIN_STATIC_VERSION}}", Version)
	html = strings.ReplaceAll(html, "{{ADMIN_API_BASE}}", "/internal/api")
	_, _ = w.Write([]byte(html))
}

func adminPanelPath() string {
	for _, path := range []string{
		"./static/internal/admin.html",
		"../../static/internal/admin.html",
	} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return "./static/internal/admin.html"
}

func (a *App) internalAdminConfigured() bool {
	return strings.TrimSpace(a.Config.InternalAdminUsername) != "" && a.Config.InternalAdminPassword != ""
}

func (a *App) internalAdminSessionSecret() string {
	for _, value := range []string{a.Config.InternalAdminSessionSecret, a.Config.AccountSessionSecret, a.Config.APIKeyHashSecret, a.Config.InternalMetricsToken} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return a.Config.InternalAdminPassword
}

func (a *App) adminCookieSecure() bool {
	return strings.HasPrefix(strings.ToLower(a.Config.PublicBaseURL), "https://")
}

// internalAdminAllowed is the legacy check used by pre-RBAC handlers; any
// authenticated admin role passes (those handlers do their own gating).
func (a *App) internalAdminAllowed(r *http.Request) bool {
	_, ok := a.adminIdentityFromRequest(r)
	return ok
}

func writeAdminUnauthorized(w http.ResponseWriter, r *http.Request) {
	config.WriteError(w, r, http.StatusUnauthorized, "ADMIN_LOGIN_REQUIRED", "Admin login is required.")
}

func writeAdminJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(payload)
}

func writeInternalLoginPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Admin Login | Reforger Mods API</title><style>:root{color-scheme:dark;--bg:#0b1118;--panel:#111a24;--text:#e8eef5;--muted:#8fa2b7;--line:#253446;--accent:#26c29a;--bad:#ff6b6b}*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;background:var(--bg);color:var(--text);font-family:Inter,ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}.login{width:min(420px,calc(100vw - 32px));background:var(--panel);border:1px solid var(--line);border-radius:8px;padding:24px}h1{margin:0 0 6px;font-size:22px}.sub{margin:0 0 20px;color:var(--muted);font-size:13px}label{display:block;margin:12px 0 6px;color:var(--muted);font-size:12px;text-transform:uppercase;letter-spacing:.04em}input,button{width:100%;height:42px;border-radius:6px;border:1px solid var(--line);background:#0f1722;color:var(--text);padding:0 12px}button{margin-top:18px;background:var(--accent);border-color:var(--accent);color:#06120f;font-weight:800;cursor:pointer}.error{margin-top:14px;color:var(--bad);font-size:13px}</style></head><body><form class="login" id="login"><h1>Admin Login</h1><p class="sub">Sign in to the internal administration panel.</p><label for="username">Username</label><input id="username" autocomplete="username" required autofocus><label for="password">Password</label><input id="password" type="password" autocomplete="current-password" required><button type="submit">Sign in</button><div class="error" id="error"></div></form><script>document.getElementById("login").addEventListener("submit",async function(e){e.preventDefault();const error=document.getElementById("error");error.textContent="";const res=await fetch("/internal/login",{method:"POST",credentials:"same-origin",headers:{"Content-Type":"application/json"},body:JSON.stringify({username:document.getElementById("username").value,password:document.getElementById("password").value})});if(res.ok){location.reload();return}let text="Login failed";try{const body=await res.json();text=body.error&&body.error.message?body.error.message:text}catch{}error.textContent=text});</script></body></html>`))
}

func constantTimeEqual(a string, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
