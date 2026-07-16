package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/api"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/models"
	"github.com/gorilla/mux"
	"go.uber.org/zap"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
)

// App stores the router and db connection so it can be reused
type App struct {
	Router         *mux.Router
	Config         config.Config
	Cache          *api.ResponseCache
	IndexStore     *api.IndexStore
	IndexScheduler *IndexScheduler
	Middleware     *api.MiddlewareChain
	Metrics        *api.Metrics
	MetricsStore   *api.MetricsStore
	BillingStore   *api.BillingStore
	StripeClient   api.StripeClient
	Mailer         api.Mailer
}

// New creates a new mux router and all the routes
func (a *App) New() *mux.Router {

	router := mux.NewRouter()

	// Serve static files
	router.PathPrefix("/static/").Handler(staticFileHandler())

	router.HandleFunc("/ads.txt", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./static/ads.txt")
	})
	router.HandleFunc("/robots.txt", a.serveRobots).Methods("GET", "HEAD")
	router.HandleFunc("/sitemap.xml", a.serveSitemap).Methods("GET", "HEAD")
	router.HandleFunc("/", a.servePublicPage("home")).Methods("GET", "HEAD")
	router.HandleFunc("/mods/", a.servePublicPage("mods")).Methods("GET", "HEAD")
	router.HandleFunc("/mods", a.servePublicPage("mods")).Methods("GET", "HEAD")
	router.HandleFunc("/mods/{id}/", a.serveModDetailPage).Methods("GET", "HEAD")
	router.HandleFunc("/mods/{id}", a.serveModDetailPage).Methods("GET", "HEAD")
	modsRedirect := func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/mods/", http.StatusMovedPermanently)
	}
	modDetailRedirect := func(w http.ResponseWriter, r *http.Request) {
		id := strings.ToUpper(mux.Vars(r)["id"])
		http.Redirect(w, r, "/mods/"+id+"/", http.StatusMovedPermanently)
	}
	router.HandleFunc("/arma-reforger-mods/", modsRedirect).Methods("GET", "HEAD")
	router.HandleFunc("/arma-reforger-mods", modsRedirect).Methods("GET", "HEAD")
	router.HandleFunc("/mods-browser/", modsRedirect).Methods("GET", "HEAD")
	router.HandleFunc("/mods-browser", modsRedirect).Methods("GET", "HEAD")
	router.HandleFunc("/arma-reforger-mods/{id}/", modDetailRedirect).Methods("GET", "HEAD")
	router.HandleFunc("/arma-reforger-mods/{id}", modDetailRedirect).Methods("GET", "HEAD")

	// Tool and guide pages. Each is registered with and without the trailing
	// slash; servePublicPage 301s to the canonical trailing-slash path.
	registerPage := func(slug string, path string) {
		router.HandleFunc(path, a.servePublicPage(slug)).Methods("GET", "HEAD")
		router.HandleFunc(strings.TrimSuffix(path, "/"), a.servePublicPage(slug)).Methods("GET", "HEAD")
	}
	for _, page := range toolPages {
		if page.Path == "/mods/" {
			continue // registered above, ahead of the {id} detail routes
		}
		registerPage(page.Slug, page.Path)
	}
	for _, page := range guidePages {
		registerPage(page.Slug, page.Path)
	}
	// Alternate names for canonical tool routes.
	configCreatorRedirect := func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/config-generator/", http.StatusMovedPermanently)
	}
	router.HandleFunc("/config-creator/", configCreatorRedirect).Methods("GET", "HEAD")
	router.HandleFunc("/config-creator", configCreatorRedirect).Methods("GET", "HEAD")
	router.HandleFunc("/config-builder/", configCreatorRedirect).Methods("GET", "HEAD")
	router.HandleFunc("/config-builder", configCreatorRedirect).Methods("GET", "HEAD")
	validatorRedirect := func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/config-validator/", http.StatusMovedPermanently)
	}
	router.HandleFunc("/validator/", validatorRedirect).Methods("GET", "HEAD")
	router.HandleFunc("/validator", validatorRedirect).Methods("GET", "HEAD")
	router.HandleFunc("/arma-reforger-mods-api/", a.servePublicPage("api")).Methods("GET", "HEAD")
	router.HandleFunc("/arma-reforger-mods-api", a.servePublicPage("api")).Methods("GET", "HEAD")
	// The former /docs pages are folded into the API reference.
	docsRedirect := func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/arma-reforger-mods-api/", http.StatusMovedPermanently)
	}
	modStructuresRedirect := func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/arma-reforger-mods-api/#mod-object", http.StatusMovedPermanently)
	}
	methodologyRedirect := func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/arma-reforger-mods-api/#caching", http.StatusMovedPermanently)
	}
	router.HandleFunc("/coming-soon/", a.serveComingSoon).Methods("GET", "HEAD")
	router.HandleFunc("/coming-soon", a.serveComingSoon).Methods("GET", "HEAD")
	router.HandleFunc("/docs/", docsRedirect).Methods("GET", "HEAD")
	router.HandleFunc("/docs", docsRedirect).Methods("GET", "HEAD")
	router.HandleFunc("/docs/mod-structures/", modStructuresRedirect).Methods("GET", "HEAD")
	router.HandleFunc("/docs/mod-structures", modStructuresRedirect).Methods("GET", "HEAD")
	router.HandleFunc("/docs/mods/", modStructuresRedirect).Methods("GET", "HEAD")
	router.HandleFunc("/docs/mods", modStructuresRedirect).Methods("GET", "HEAD")
	router.HandleFunc("/docs/methodology/", methodologyRedirect).Methods("GET", "HEAD")
	router.HandleFunc("/docs/methodology", methodologyRedirect).Methods("GET", "HEAD")
	router.HandleFunc("/docs/changelog/", a.servePublicPage("changelog")).Methods("GET", "HEAD")
	router.HandleFunc("/docs/changelog", a.servePublicPage("changelog")).Methods("GET", "HEAD")
	router.HandleFunc("/changelog/", a.servePublicPage("changelog")).Methods("GET", "HEAD")
	router.HandleFunc("/changelog", a.servePublicPage("changelog")).Methods("GET", "HEAD")
	router.HandleFunc("/privacy/", a.servePublicPage("privacy")).Methods("GET", "HEAD")
	router.HandleFunc("/privacy", a.servePublicPage("privacy")).Methods("GET", "HEAD")
	router.HandleFunc("/terms/", a.servePublicPage("terms")).Methods("GET", "HEAD")
	router.HandleFunc("/terms", a.servePublicPage("terms")).Methods("GET", "HEAD")
	router.HandleFunc("/support/", a.servePublicPage("support")).Methods("GET", "HEAD")
	router.HandleFunc("/support", a.servePublicPage("support")).Methods("GET", "HEAD")
	router.HandleFunc("/pricing/", a.servePublicPage("pricing")).Methods("GET", "HEAD")
	router.HandleFunc("/pricing", a.servePublicPage("pricing")).Methods("GET", "HEAD")
	router.HandleFunc("/billing/success/", a.servePublicPage("billing-success")).Methods("GET", "HEAD")
	router.HandleFunc("/billing/success", a.servePublicPage("billing-success")).Methods("GET", "HEAD")
	router.HandleFunc("/account/billing/", a.servePublicPage("account-billing")).Methods("GET", "HEAD")
	router.HandleFunc("/account/billing", a.servePublicPage("account-billing")).Methods("GET", "HEAD")
	router.HandleFunc("/account/api-keys/", a.servePublicPage("account-api-keys")).Methods("GET", "HEAD")

	a.Metrics = api.NewMetrics()
	a.configureMetricsWindowLocation(a.Metrics)

	if a.Config.MetricsPersistenceEnabled {
		metricsStatePath := a.Config.MetricsStatePath
		absoluteMetricsStatePath, absErr := filepath.Abs(metricsStatePath)
		if absErr != nil {
			absoluteMetricsStatePath = metricsStatePath
		}
		metricsStateExists := false
		if _, err := os.Stat(metricsStatePath); err == nil {
			metricsStateExists = true
		} else if !os.IsNotExist(err) {
			zap.S().Warnw(
				"metrics state path could not be checked",
				"path", metricsStatePath,
				"absolutePath", absoluteMetricsStatePath,
				"error", err,
			)
		}
		store, err := api.NewMetricsStore(
			a.Config.MetricsStatePath,
			a.Config.MetricsFlushInterval,
		)
		if err != nil {
			zap.S().Warnw("metrics persistence disabled", "error", err)
		} else {
			zap.S().Infow(
				"metrics persistence enabled",
				"path", metricsStatePath,
				"absolutePath", absoluteMetricsStatePath,
				"stateFileExists", metricsStateExists,
			)
			if err := store.Load(a.Metrics); err != nil {
				zap.S().Warnw(
					"metrics state was not loaded; starting with fresh metrics",
					"path", metricsStatePath,
					"absolutePath", absoluteMetricsStatePath,
					"error",
					err,
				)
			} else if metricsStateExists {
				zap.S().Infow(
					"metrics state loaded",
					"path", metricsStatePath,
					"absolutePath", absoluteMetricsStatePath,
					"totalRequests", a.Metrics.TotalRequests(),
				)
			}
			if a.Metrics.TotalRequests() == 0 {
				imported, err := api.ImportRequestMetricsFromLogs(a.Metrics, a.Config.LogDir)
				if err != nil {
					zap.S().Warnw("historical request log import failed", "error", err)
				} else if imported > 0 {
					zap.S().Infow(
						"historical request logs imported into metrics",
						"requests", imported,
						"path", metricsStatePath,
						"absolutePath", absoluteMetricsStatePath,
						"totalRequests", a.Metrics.TotalRequests(),
					)
				}
			}

			store.Start(a.Metrics)
			a.MetricsStore = store
		}
	}

	a.Cache = api.NewResponseCache(a.Config, a.Metrics)
	if a.Config.IndexEnabled {
		store, err := api.OpenIndexStore(a.Config.IndexDBPath)
		if err != nil {
			zap.S().Fatalw("index storage unavailable", "path", a.Config.IndexDBPath, "error", err)
		}
		a.IndexStore = store
		a.Cache.SetIndexStore(store)
		if err := a.Cache.PreloadHotEntries(context.Background(), a.Config.IndexHotLoadLimit); err != nil {
			zap.S().Warnw("index hot cache preload failed", "error", err)
		}
		if a.Config.IndexRefreshEnabled {
			a.IndexScheduler = NewIndexScheduler(a)
			a.IndexScheduler.Start()
		}
	}
	if err := a.validateProductionInternalAdminConfig(); err != nil {
		zap.S().Fatalw("production internal admin configuration invalid", "error", err)
	}
	if a.Config.BillingEnabled {
		if err := a.validateProductionBillingConfig(); err != nil {
			zap.S().Fatalw("production billing configuration invalid", "error", err)
		}
		store, err := api.OpenBillingStore(a.Config.BillingDBPath)
		if err != nil {
			zap.S().Fatalw("billing storage unavailable", "path", a.Config.BillingDBPath, "error", err)
		}
		a.BillingStore = store
		a.StripeClient = api.StripeClient{
			SecretKey: a.Config.StripeSecretKey,
			BaseURL:   a.Config.StripeAPIBaseURL,
		}
		a.Mailer = api.Mailer{
			Host:     a.Config.SMTPHost,
			Port:     a.Config.SMTPPort,
			Username: a.Config.SMTPUsername,
			Password: a.Config.SMTPPassword,
			From:     a.Config.SMTPFrom,
		}
	}
	a.Middleware = api.NewMiddleware(a.Config, a.Metrics)
	a.configureBillingIdentityResolver()

	// API Routes. Unversioned routes are retained as deprecated aliases.
	v1 := router.PathPrefix("/v1").Subrouter()
	a.registerAPIRoutes(v1, false)
	a.registerAPIRoutes(router, true)
	router.Handle("/billing/checkout", a.Middleware.Wrap(http.HandlerFunc(a.BillingCheckoutHandler))).Methods("POST", "OPTIONS")
	router.Handle("/billing/session", a.Middleware.Wrap(http.HandlerFunc(a.BillingSessionHandler))).Methods("GET", "OPTIONS")
	router.Handle("/billing/portal", a.Middleware.Wrap(http.HandlerFunc(a.BillingPortalHandler))).Methods("POST", "OPTIONS")
	router.HandleFunc("/stripe/webhook", a.StripeWebhookHandler).Methods("POST")
	router.Handle("/account/login", a.Middleware.Wrap(http.HandlerFunc(a.AccountLoginHandler))).Methods("POST", "OPTIONS")
	router.HandleFunc("/account/verify", a.AccountVerifyHandler).Methods("GET")
	router.Handle("/account/logout", a.Middleware.Wrap(http.HandlerFunc(a.AccountLogoutHandler))).Methods("POST", "OPTIONS")
	router.Handle("/account/session", a.Middleware.Wrap(http.HandlerFunc(a.AccountSessionHandler))).Methods("GET", "OPTIONS")
	router.Handle("/account/api-keys", a.Middleware.Wrap(http.HandlerFunc(a.AccountAPIKeysHandler))).Methods("GET", "OPTIONS")
	router.Handle("/account/api-keys", a.Middleware.Wrap(http.HandlerFunc(a.CreateAccountAPIKeyHandler))).Methods("POST", "OPTIONS")
	router.Handle("/account/api-keys/{id}", a.Middleware.Wrap(http.HandlerFunc(a.DeleteAccountAPIKeyHandler))).Methods("DELETE", "OPTIONS")
	router.Handle("/rate-limits", a.Middleware.Wrap(http.HandlerFunc(a.RateLimitsHandler))).Methods("GET", "OPTIONS")
	v1.Handle("/rate-limits", a.Middleware.Wrap(http.HandlerFunc(a.RateLimitsHandler))).Methods("GET", "OPTIONS")
	router.HandleFunc("/internal/metrics", a.internalMetricsHandler).Methods("GET")
	router.HandleFunc("/internal/metrics/import-logs", a.internalMetricsImportLogsHandler).Methods("POST")
	router.HandleFunc("/internal/metrics/panel", a.internalMetricsPanelHandler).Methods("GET")
	router.HandleFunc("/internal/login", a.internalLoginHandler).Methods("POST")
	router.HandleFunc("/internal/logout", a.internalLogoutHandler).Methods("POST")
	router.HandleFunc("/internal/admin/users", a.internalUsersHandler).Methods("GET")
	router.HandleFunc("/internal/admin/users/{id}", a.internalDeleteUserHandler).Methods("DELETE")
	router.HandleFunc("/internal/admin/users/{id}/login-link", a.internalSendLoginLinkHandler).Methods("POST")
	router.HandleFunc("/internal/admin/users/{id}/keys/{keyId}/revoke", a.internalRevokeUserKeyHandler).Methods("POST")
	router.HandleFunc("/internal/admin/api-keys", a.internalAPIKeysHandler).Methods("GET")
	router.HandleFunc("/internal/admin/api-keys", a.createInternalAPIKeyHandler).Methods("POST")
	router.HandleFunc("/internal/admin/api-keys/{id}", a.deleteInternalAPIKeyHandler).Methods("DELETE")
	v1.HandleFunc("/internal/metrics", a.internalMetricsHandler).Methods("GET")
	v1.HandleFunc("/internal/metrics/import-logs", a.internalMetricsImportLogsHandler).Methods("POST")
	v1.HandleFunc("/internal/metrics/panel", a.internalMetricsPanelHandler).Methods("GET")
	router.NotFoundHandler = http.HandlerFunc(a.serveNotFound)

	return router
}

func (a *App) configureMetricsWindowLocation(metrics *api.Metrics) {
	if metrics == nil {
		return
	}
	timezone := strings.TrimSpace(a.Config.MetricsTimezone)
	if timezone == "" {
		timezone = "UTC"
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		zap.S().Warnw(
			"metrics timezone invalid; using UTC",
			"timezone", timezone,
			"error", err,
		)
		location = time.UTC
		timezone = "UTC"
	}
	metrics.SetWindowLocation(location)
	zap.S().Infow("metrics window timezone configured", "timezone", timezone)
}

func (a *App) registerAPIRoutes(router *mux.Router, deprecated bool) {
	wrap := func(handler http.HandlerFunc) http.Handler {
		var h http.Handler = handler
		if deprecated {
			h = deprecatedRoute(h)
		}
		return a.Middleware.Wrap(h)
	}
	router.Handle("/health", wrap(a.healthCheckHandler)).Methods("GET", "HEAD", "OPTIONS")
	router.Handle("/mod/{id}", wrap(a.ModByIDHandler)).Methods("GET", "OPTIONS")
	router.Handle("/mods", wrap(a.ModsHandler)).Methods("GET", "OPTIONS")
	router.Handle("/mods/{page}", wrap(a.ModsByPageHandler)).Methods("GET", "OPTIONS")
	router.Handle("/search", wrap(a.SearchHandler)).Methods("GET", "OPTIONS")
	router.Handle("/refresh/jobs/{id}", wrap(a.RefreshJobHandler)).Methods("GET", "OPTIONS")
}

func (a *App) Initialize() {
	// initialize api router
	a.Router = a.New()
}

func (a *App) Shutdown(ctx context.Context) error {
	var firstErr error

	if a.IndexScheduler != nil {
		a.IndexScheduler.Stop()
	}

	if a.Cache != nil {
		if err := a.Cache.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	if a.MetricsStore != nil {
		if err := a.MetricsStore.Close(a.Metrics); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if a.IndexStore != nil {
		if err := a.IndexStore.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if a.BillingStore != nil {
		if err := a.BillingStore.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

func (a *App) healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	data := models.HealthCheckData{
		Code:  http.StatusOK,
		Alive: true,
	}
	b, _ := json.Marshal(models.HealthCheckResponse{
		Status: "success",
		Data:   data,
	})
	_, _ = io.Writer.Write(w, b)
}

func (a *App) RefreshJobHandler(w http.ResponseWriter, r *http.Request) {
	if a.Cache == nil {
		config.WriteError(w, r, http.StatusNotFound, "REFRESH_JOB_NOT_FOUND", "Refresh job was not found.")
		return
	}
	id := strings.TrimSpace(mux.Vars(r)["id"])
	job, ok := a.Cache.RefreshJob(id)
	if !ok {
		config.WriteError(w, r, http.StatusNotFound, "REFRESH_JOB_NOT_FOUND", "Refresh job was not found.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if job.Status == api.RefreshJobQueued || job.Status == api.RefreshJobRunning {
		w.Header().Set("Retry-After", strconv.Itoa(job.RetryAfterSeconds))
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(job)
}

func (a *App) internalMetricsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if !a.Config.InternalMetricsEnabled {
		config.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "Not found.")
		return
	}
	if !a.internalAdminAllowed(r) {
		writeAdminUnauthorized(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if a.Metrics == nil {
		a.Metrics = api.NewMetrics()
		a.configureMetricsWindowLocation(a.Metrics)
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(a.Metrics.Snapshot(a.Cache))
}

func (a *App) internalMetricsImportLogsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if !a.Config.InternalMetricsEnabled {
		config.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "Not found.")
		return
	}
	if !a.internalAdminAllowed(r) {
		writeAdminUnauthorized(w, r)
		return
	}
	if a.Metrics == nil {
		a.Metrics = api.NewMetrics()
		a.configureMetricsWindowLocation(a.Metrics)
	}
	imported, err := api.ImportRequestMetricsFromLogs(a.Metrics, a.Config.LogDir)
	if err != nil {
		zap.S().Warnw("manual request log import failed", "error", err)
		config.WriteError(w, r, http.StatusInternalServerError, "METRICS_LOG_IMPORT_FAILED", "Failed to import request logs.")
		return
	}
	if a.MetricsStore != nil {
		if err := a.MetricsStore.Save(a.Metrics); err != nil {
			zap.S().Warnw("metrics state save after manual log import failed", "error", err)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"imported":      imported,
		"totalRequests": a.Metrics.TotalRequests(),
	})
}

func (a *App) internalMetricsPanelHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if !a.Config.InternalMetricsEnabled {
		config.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "Not found.")
		return
	}
	if !a.internalAdminAllowed(r) {
		writeInternalLoginPage(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, internalMetricsPanelPath())
}

func (a *App) internalUsersHandler(w http.ResponseWriter, r *http.Request) {
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
	users, err := a.BillingStore.AdminAccountSummaries(r.Context(), 300)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "ADMIN_USERS_FAILED", "Failed to load users.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"users": users})
}

func (a *App) internalLoginHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if !a.Config.InternalMetricsEnabled {
		config.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "Not found.")
		return
	}
	if !a.internalAdminConfigured() {
		config.WriteError(w, r, http.StatusServiceUnavailable, "ADMIN_NOT_CONFIGURED", "Admin login is not configured.")
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
	if !constantTimeEqual(strings.TrimSpace(input.Username), strings.TrimSpace(a.Config.InternalAdminUsername)) ||
		!constantTimeEqual(input.Password, a.Config.InternalAdminPassword) {
		config.WriteError(w, r, http.StatusUnauthorized, "INVALID_ADMIN_LOGIN", "Invalid username or password.")
		return
	}
	expires := time.Now().Add(12 * time.Hour)
	http.SetCookie(w, &http.Cookie{
		Name:     internalAdminCookie,
		Value:    api.CreateAccountSessionToken("admin:"+a.Config.InternalAdminUsername, expires, a.internalAdminSessionSecret()),
		Path:     "/internal",
		Expires:  expires,
		HttpOnly: true,
		Secure:   a.adminCookieSecure(),
		SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]bool{"authenticated": true})
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

const internalAdminCookie = "rfm_internal_admin"

func (a *App) internalAdminAllowed(r *http.Request) bool {
	if !a.internalAdminConfigured() {
		return false
	}
	cookie, err := r.Cookie(internalAdminCookie)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return false
	}
	subject, ok := api.VerifyAccountSessionToken(cookie.Value, a.internalAdminSessionSecret(), time.Now())
	return ok && constantTimeEqual(subject, "admin:"+a.Config.InternalAdminUsername)
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

func writeAdminUnauthorized(w http.ResponseWriter, r *http.Request) {
	config.WriteError(w, r, http.StatusUnauthorized, "ADMIN_LOGIN_REQUIRED", "Admin login is required.")
}

func writeInternalLoginPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Admin Login | Reforger Mods API</title><style>:root{color-scheme:dark;--bg:#0b1118;--panel:#111a24;--text:#e8eef5;--muted:#8fa2b7;--line:#253446;--accent:#26c29a;--bad:#ff6b6b}*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;background:var(--bg);color:var(--text);font-family:Inter,ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}.login{width:min(420px,calc(100vw - 32px));background:var(--panel);border:1px solid var(--line);border-radius:8px;padding:24px}h1{margin:0 0 6px;font-size:22px}.sub{margin:0 0 20px;color:var(--muted);font-size:13px}label{display:block;margin:12px 0 6px;color:var(--muted);font-size:12px;text-transform:uppercase;letter-spacing:.04em}input,button{width:100%;height:42px;border-radius:6px;border:1px solid var(--line);background:#0f1722;color:var(--text);padding:0 12px}button{margin-top:18px;background:var(--accent);border-color:var(--accent);color:#06120f;font-weight:800;cursor:pointer}.error{margin-top:14px;color:var(--bad);font-size:13px}</style></head><body><form class="login" id="login"><h1>Admin Login</h1><p class="sub">Sign in to view internal metrics, request logs, and subscriber diagnostics.</p><label for="username">Username</label><input id="username" autocomplete="username" required autofocus><label for="password">Password</label><input id="password" type="password" autocomplete="current-password" required><button type="submit">Sign in</button><div class="error" id="error"></div></form><script>document.getElementById("login").addEventListener("submit",async function(e){e.preventDefault();const error=document.getElementById("error");error.textContent="";const res=await fetch("/internal/login",{method:"POST",credentials:"same-origin",headers:{"Content-Type":"application/json"},body:JSON.stringify({username:document.getElementById("username").value,password:document.getElementById("password").value})});if(res.ok){location.reload();return}let text="Login failed";try{const body=await res.json();text=body.error&&body.error.message?body.error.message:text}catch{}error.textContent=text});</script></body></html>`))
}

func constantTimeEqual(a string, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func internalMetricsPanelPath() string {
	for _, path := range []string{
		"./static/internal_metrics.html",
		"../../static/internal_metrics.html",
	} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return "./static/internal_metrics.html"
}

func deprecatedRoute(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Deprecation", "true")
		w.Header().Set("Link", `</v1`+r.URL.Path+`>; rel="successor-version"`)
		next.ServeHTTP(w, r)
	})
}
