package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

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
	router.HandleFunc("/arma-reforger-mods/", a.servePublicPage("mods")).Methods("GET", "HEAD")
	router.HandleFunc("/arma-reforger-mods", a.servePublicPage("mods")).Methods("GET", "HEAD")
	router.HandleFunc("/arma-reforger-mods/{id}/", a.serveModDetailPage).Methods("GET", "HEAD")
	router.HandleFunc("/arma-reforger-mods/{id}", a.serveModDetailPage).Methods("GET", "HEAD")

	// Tool and guide pages. Each is registered with and without the trailing
	// slash; servePublicPage 301s to the canonical trailing-slash path.
	registerPage := func(slug string, path string) {
		router.HandleFunc(path, a.servePublicPage(slug)).Methods("GET", "HEAD")
		router.HandleFunc(strings.TrimSuffix(path, "/"), a.servePublicPage(slug)).Methods("GET", "HEAD")
	}
	for _, page := range toolPages {
		if page.Path == "/arma-reforger-mods/" {
			continue // registered above, ahead of the {id} detail routes
		}
		registerPage(page.Slug, page.Path)
	}
	for _, page := range guidePages {
		registerPage(page.Slug, page.Path)
	}
	// /config-creator is an alternate name for the same tool.
	configCreatorRedirect := func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/config-generator/", http.StatusMovedPermanently)
	}
	router.HandleFunc("/config-creator/", configCreatorRedirect).Methods("GET", "HEAD")
	router.HandleFunc("/config-creator", configCreatorRedirect).Methods("GET", "HEAD")
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

	if a.Config.MetricsPersistenceEnabled {
		store, err := api.NewMetricsStore(
			a.Config.MetricsStatePath,
			a.Config.MetricsFlushInterval,
		)
		if err != nil {
			zap.S().Warnw("metrics persistence disabled", "error", err)
		} else {
			if err := store.Load(a.Metrics); err != nil {
				zap.S().Warnw(
					"metrics state was not loaded; starting with fresh metrics",
					"error",
					err,
				)
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
	router.HandleFunc("/internal/metrics/panel", a.internalMetricsPanelHandler).Methods("GET")
	v1.HandleFunc("/internal/metrics", a.internalMetricsHandler).Methods("GET")
	v1.HandleFunc("/internal/metrics/panel", a.internalMetricsPanelHandler).Methods("GET")
	router.NotFoundHandler = http.HandlerFunc(a.serveNotFound)

	return router
}

func (a *App) registerAPIRoutes(router *mux.Router, deprecated bool) {
	wrap := func(handler http.HandlerFunc) http.Handler {
		var h http.Handler = handler
		if deprecated {
			h = deprecatedRoute(h)
		}
		return a.Middleware.Wrap(h)
	}
	router.Handle("/health", wrap(a.healthCheckHandler)).Methods("GET", "OPTIONS")
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
	if !internalMetricsAllowed(r, a.Config.InternalMetricsToken) {
		writeMetricsUnauthorized(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if a.Metrics == nil {
		a.Metrics = api.NewMetrics()
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(a.Metrics.Snapshot(a.Cache))
}

func (a *App) internalMetricsPanelHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if !a.Config.InternalMetricsEnabled {
		config.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "Not found.")
		return
	}
	if !internalMetricsAllowed(r, a.Config.InternalMetricsToken) {
		writeMetricsUnauthorized(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, internalMetricsPanelPath())
}

func internalMetricsAllowed(r *http.Request, token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	return constantTimeEqual(metricsTokenFromRequest(r), token)
}

func metricsTokenFromRequest(r *http.Request) string {
	if token := strings.TrimSpace(r.Header.Get("X-Internal-Metrics-Token")); token != "" {
		return token
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	if strings.HasPrefix(strings.ToLower(auth), "basic ") {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(auth[6:]))
		if err == nil {
			_, password, ok := strings.Cut(string(decoded), ":")
			if ok {
				return password
			}
		}
	}
	return ""
}

func writeMetricsUnauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Internal Metrics", charset="UTF-8"`)
	config.WriteError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Metrics token is required.")
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
