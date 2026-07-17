package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/api"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/models"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/telemetry"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
)

// Version is the running application version, stamped onto telemetry events.
var Version = "dev"

// App stores the router, stores, and telemetry plumbing so it can be reused.
type App struct {
	Router         *mux.Router
	Handler        http.Handler // telemetry wrapper around Router; serve this
	Config         config.Config
	Cache          *api.ResponseCache
	IndexStore     *api.IndexStore
	IndexScheduler *IndexScheduler
	Middleware     *api.MiddlewareChain
	Telemetry      *telemetry.Store
	Recorder       *telemetry.Recorder
	Hooks          *api.TelemetryHooks
	Aggregator     *telemetry.Aggregator
	BillingStore   *api.BillingStore
	StripeClient   api.StripeClient
	Mailer         api.Mailer

	aggregatorStop chan struct{}
	aggregatorWG   sync.WaitGroup
	aggMu          sync.Mutex
}

// New creates the mux router with all routes and wires the telemetry stack.
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

	a.initTelemetry()

	a.Cache = api.NewResponseCache(a.Config, a.Hooks)
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
	a.Middleware = api.NewMiddleware(a.Config)
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

	a.registerAdminRoutes(router)
	router.NotFoundHandler = http.HandlerFunc(a.serveNotFound)

	return router
}

// initTelemetry opens the telemetry store, starts the async recorder, mirrors
// zap logs into the store, and starts the aggregation loop.
func (a *App) initTelemetry() {
	a.Hooks = api.NewTelemetryHooks(nil)
	if !a.Config.TelemetryEnabled {
		zap.S().Warn("telemetry is disabled; the admin panel will have no data")
		return
	}
	store, err := telemetry.Open(a.Config.TelemetryDBPath)
	if err != nil {
		zap.S().Errorw("telemetry storage unavailable; continuing without telemetry", "path", a.Config.TelemetryDBPath, "error", err)
		return
	}
	a.Telemetry = store
	a.Recorder = telemetry.NewRecorder(store)
	a.Hooks = api.NewTelemetryHooks(a.Recorder)
	a.Aggregator = telemetry.NewAggregator(store)

	// Mirror structured logs into the telemetry DB for the log explorer.
	sink := telemetry.NewLogSink(a.Recorder, zapcore.InfoLevel, a.Config.InstanceID, Version)
	existing := zap.L()
	zap.ReplaceGlobals(existing.WithOptions(zap.WrapCore(func(core zapcore.Core) zapcore.Core {
		return zapcore.NewTee(core, sink)
	})))

	a.bootstrapAdminUsers()

	a.aggregatorStop = make(chan struct{})
	a.aggregatorWG.Add(1)
	go a.aggregationLoop()
}

// aggregationLoop periodically aggregates raw events and enforces retention.
func (a *App) aggregationLoop() {
	defer a.aggregatorWG.Done()
	a.runAggregation()
	ticker := time.NewTicker(5 * time.Minute)
	prune := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	defer prune.Stop()
	for {
		select {
		case <-a.aggregatorStop:
			return
		case <-ticker.C:
			a.runAggregation()
		case <-prune.C:
			if err := a.Aggregator.Prune(context.Background(),
				a.Config.TelemetryRawRetentionDays,
				a.Config.TelemetryHourlyRetention,
				a.Config.TelemetryLogRetentionDays,
				a.Config.TelemetryErrRetentionDays,
			); err != nil {
				zap.S().Warnw("telemetry prune failed", "error", err)
			}
		}
	}
}

func (a *App) runAggregation() {
	a.aggMu.Lock()
	defer a.aggMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if err := a.Aggregator.Run(ctx); err != nil {
		zap.S().Warnw("telemetry aggregation failed", "error", err)
	}
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
	a.Router = a.New()
	// The telemetry middleware is the outermost wrapper so every inbound
	// request — including 404s — produces exactly one request event.
	if a.Recorder != nil {
		a.Handler = api.NewTelemetryMiddleware(a.Config, a.Recorder, a.Router, Version).Handler()
	} else {
		a.Handler = a.Router
	}
}

func (a *App) Shutdown(ctx context.Context) error {
	var firstErr error

	if a.IndexScheduler != nil {
		a.IndexScheduler.Stop()
	}
	if a.aggregatorStop != nil {
		close(a.aggregatorStop)
		a.aggregatorWG.Wait()
	}

	if a.Cache != nil {
		if err := a.Cache.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if a.Recorder != nil {
		if err := a.Recorder.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if a.Telemetry != nil {
		if err := a.Telemetry.Close(); err != nil && firstErr == nil {
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

func deprecatedRoute(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Deprecation", "true")
		w.Header().Set("Link", `</v1`+r.URL.Path+`>; rel="successor-version"`)
		next.ServeHTTP(w, r)
	})
}
