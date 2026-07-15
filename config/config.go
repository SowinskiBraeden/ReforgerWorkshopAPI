package config

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/models"
	"github.com/joho/godotenv"
)

// Config holds the project config values
type Config struct {
	BaseURL                  string
	FullURL                  string
	PublicBaseURL            string
	APIBaseURL               string
	PublicCanonicalRedirects bool
	BindAddress              string

	LogDir      string
	LogToStdout bool

	TrustedProxyCIDRs string
	AllowedOrigins    []string
	MaxBodyBytes      int64
	MaxQueryLength    int

	AnonymousRateLimitPerMinute int
	AnonymousRateBurst          int
	DeveloperRateLimitPerMinute int
	ProRateLimitPerMinute       int
	InternalRateLimitPerMinute  int
	DeveloperMaxActiveKeys      int
	ProMaxActiveKeys            int
	RateLimitClientTTL          time.Duration

	CacheMaxEntries          int
	ModCacheTTL              time.Duration
	ModCacheStale            time.Duration
	ListCacheTTL             time.Duration
	ListCacheStale           time.Duration
	SearchCacheTTL           time.Duration
	SearchCacheStale         time.Duration
	ModDetailCacheTTL        time.Duration
	ModDetailCacheStale      time.Duration
	NotFoundCacheTTL         time.Duration
	CacheRefreshTimeout      time.Duration
	CacheRefreshParallel     int
	CacheRefreshQueueSize    int
	CacheRefreshJobRetention time.Duration
	CacheRefreshRetryAfter   time.Duration

	IndexEnabled                  bool
	IndexDBPath                   string
	IndexRefreshEnabled           bool
	IndexPopularPages             int
	IndexRecentPages              int
	IndexRefreshInterval          time.Duration
	IndexDetailRefreshConcurrency int
	IndexListRefreshConcurrency   int
	IndexHotLoadLimit             int

	UpstreamTimeout     time.Duration
	UpstreamRetries     int
	UpstreamConcurrency int
	UpstreamUserAgent   string

	InternalMetricsEnabled     bool
	InternalMetricsToken       string
	InternalAdminUsername      string
	InternalAdminPassword      string
	InternalAdminSessionSecret string
	MetricsOwnClientPatterns   []string
	MetricsInternalCIDRs       string

	MetricsPersistenceEnabled bool
	MetricsStatePath          string
	MetricsFlushInterval      time.Duration

	BillingEnabled         bool
	BillingDBPath          string
	StripeSecretKey        string
	StripeWebhookSecret    string
	StripeDeveloperPriceID string
	StripeProPriceID       string
	BillingSuccessURL      string
	BillingCancelURL       string
	BillingPortalReturnURL string
	APIKeyHashSecret       string
	AppEnv                 string
	StripeAPIBaseURL       string

	AccountSessionSecret string
	AccountSessionTTL    time.Duration
	LoginTokenTTL        time.Duration
	LoginTokenCooldown   time.Duration

	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string

	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}

var current *Config

// New sets up all config related services
func New() *Config {

	if err := godotenv.Load(); err != nil {
		fmt.Fprintln(os.Stderr, "warning: .env was not loaded; check .env syntax")
	}

	//setup zap logger and replace default Logger
	logger, err := setLogger(os.Getenv("ENV"))
	if err != nil {
		// if we get an error, we will just set the default to debug and move on
		zap.S().With(err).Warn("issue setting logger")
	}
	_ = zap.ReplaceGlobals(logger)

	fullURL := strings.TrimRight(envString("FULL_URL", "http://localhost:8000"), "/")
	apiBaseURL := strings.TrimRight(envString("API_BASE_URL", fullURL), "/")
	publicBaseURL := strings.TrimRight(envString("PUBLIC_BASE_URL", fullURL), "/")

	cfg := &Config{
		BaseURL:                  envString("BASE_URL", "localhost"),
		FullURL:                  apiBaseURL,
		PublicBaseURL:            publicBaseURL,
		APIBaseURL:               apiBaseURL,
		PublicCanonicalRedirects: envBool("PUBLIC_CANONICAL_REDIRECTS", false),
		BindAddress:              envString("BIND_ADDRESS", "0.0.0.0:8000"),

		LogDir:      envString("LOG_DIR", "logs"),
		LogToStdout: envBool("LOG_TO_STDOUT", true),

		TrustedProxyCIDRs: envString("TRUSTED_PROXY_CIDRS", ""),
		AllowedOrigins:    envCSV("CORS_ALLOWED_ORIGINS"),
		MaxBodyBytes:      int64(envInt("MAX_BODY_BYTES", 1048576)),
		MaxQueryLength:    envInt("MAX_QUERY_LENGTH", 2048),

		AnonymousRateLimitPerMinute: envInt("RATE_LIMIT_FREE_PER_MINUTE", envInt("ANON_RATE_LIMIT_PER_MINUTE", 60)),
		AnonymousRateBurst:          envInt("ANON_RATE_BURST", 20),
		DeveloperRateLimitPerMinute: envInt("RATE_LIMIT_DEVELOPER_PER_MINUTE", 300),
		ProRateLimitPerMinute:       envInt("RATE_LIMIT_PRO_PER_MINUTE", 1200),
		InternalRateLimitPerMinute:  envInt("RATE_LIMIT_INTERNAL_PER_MINUTE", 5000),
		DeveloperMaxActiveKeys:      envInt("DEVELOPER_MAX_ACTIVE_KEYS", 2),
		ProMaxActiveKeys:            envInt("PRO_MAX_ACTIVE_KEYS", 10),
		RateLimitClientTTL:          envDuration("RATE_LIMIT_CLIENT_TTL", 10*time.Minute),

		CacheMaxEntries:          envInt("CACHE_MAX_ENTRIES", 1000),
		ModCacheTTL:              envDuration("CACHE_MOD_TTL", time.Hour),
		ModCacheStale:            envDuration("CACHE_MOD_STALE", 24*time.Hour),
		ListCacheTTL:             envDuration("CACHE_LIST_FRESH_TTL", envDuration("CACHE_LIST_TTL", 10*time.Minute)),
		ListCacheStale:           envDuration("CACHE_LIST_STALE_TTL", envDuration("CACHE_LIST_STALE", time.Hour)),
		SearchCacheTTL:           envDuration("CACHE_SEARCH_FRESH_TTL", 10*time.Minute),
		SearchCacheStale:         envDuration("CACHE_SEARCH_STALE_TTL", 2*time.Hour),
		ModDetailCacheTTL:        envDuration("CACHE_MOD_DETAIL_FRESH_TTL", envDuration("CACHE_MOD_TTL", time.Hour)),
		ModDetailCacheStale:      envDuration("CACHE_MOD_DETAIL_STALE_TTL", envDuration("CACHE_MOD_STALE", 24*time.Hour)),
		NotFoundCacheTTL:         envDuration("CACHE_NOT_FOUND_TTL", 10*time.Minute),
		CacheRefreshTimeout:      envDuration("CACHE_REFRESH_TIMEOUT", 20*time.Second),
		CacheRefreshParallel:     envInt("CACHE_REFRESH_CONCURRENCY", 8),
		CacheRefreshQueueSize:    envInt("CACHE_REFRESH_QUEUE_SIZE", 64),
		CacheRefreshJobRetention: envDuration("CACHE_REFRESH_JOB_RETENTION", 15*time.Minute),
		CacheRefreshRetryAfter:   envDuration("CACHE_REFRESH_RETRY_AFTER", 2*time.Second),

		IndexEnabled:                  envBool("INDEX_ENABLED", false),
		IndexDBPath:                   envString("INDEX_DB_PATH", "/var/lib/reforgermods-api/reforgermods-index.db"),
		IndexRefreshEnabled:           envBool("INDEX_REFRESH_ENABLED", true),
		IndexPopularPages:             envInt("INDEX_POPULAR_PAGES", 10),
		IndexRecentPages:              envInt("INDEX_RECENT_PAGES", 5),
		IndexRefreshInterval:          envDuration("INDEX_REFRESH_INTERVAL", 30*time.Minute),
		IndexDetailRefreshConcurrency: envInt("INDEX_DETAIL_REFRESH_CONCURRENCY", 1),
		IndexListRefreshConcurrency:   envInt("INDEX_LIST_REFRESH_CONCURRENCY", 1),
		IndexHotLoadLimit:             envInt("INDEX_HOT_LOAD_LIMIT", 500),

		UpstreamTimeout:     envDuration("UPSTREAM_TIMEOUT", 15*time.Second),
		UpstreamRetries:     envInt("UPSTREAM_RETRIES", 2),
		UpstreamConcurrency: envInt("UPSTREAM_CONCURRENCY", 4),
		UpstreamUserAgent:   envString("UPSTREAM_USER_AGENT", "Cedarline Reforger Mods API/1.0 (+https://cedarline.digital)"),

		InternalMetricsEnabled:     envBool("INTERNAL_METRICS_ENABLED", true),
		InternalMetricsToken:       strings.TrimSpace(os.Getenv("INTERNAL_METRICS_TOKEN")),
		InternalAdminUsername:      strings.TrimSpace(os.Getenv("INTERNAL_ADMIN_USERNAME")),
		InternalAdminPassword:      os.Getenv("INTERNAL_ADMIN_PASSWORD"),
		InternalAdminSessionSecret: strings.TrimSpace(os.Getenv("INTERNAL_ADMIN_SESSION_SECRET")),
		MetricsOwnClientPatterns: envCSVWithDefault(
			"METRICS_OWN_CLIENT_PATTERNS",
			[]string{"node", "ReforgerPanel", "DZRPanel"},
		),
		MetricsInternalCIDRs: envString("METRICS_INTERNAL_CIDRS", "127.0.0.1/32,::1/128"),

		MetricsPersistenceEnabled: envBool("METRICS_PERSISTENCE_ENABLED", false),
		MetricsStatePath:          envString("METRICS_STATE_PATH", ""),
		MetricsFlushInterval:      envDuration("METRICS_FLUSH_INTERVAL", 15*time.Second),

		BillingEnabled:         envBool("BILLING_ENABLED", false),
		BillingDBPath:          envString("BILLING_DB_PATH", "/var/lib/reforgermods-api/reforgermods-billing.db"),
		StripeSecretKey:        strings.TrimSpace(os.Getenv("STRIPE_SECRET_KEY")),
		StripeWebhookSecret:    strings.TrimSpace(os.Getenv("STRIPE_WEBHOOK_SECRET")),
		StripeDeveloperPriceID: strings.TrimSpace(os.Getenv("STRIPE_DEVELOPER_PRICE_ID")),
		StripeProPriceID:       strings.TrimSpace(os.Getenv("STRIPE_PRO_PRICE_ID")),
		BillingSuccessURL:      envString("BILLING_SUCCESS_URL", publicBaseURL+"/account/api-keys/?checkout=success"),
		BillingCancelURL:       envString("BILLING_CANCEL_URL", publicBaseURL+"/pricing"),
		BillingPortalReturnURL: envString("BILLING_PORTAL_RETURN_URL", publicBaseURL+"/account/billing"),
		APIKeyHashSecret:       strings.TrimSpace(os.Getenv("API_KEY_HASH_SECRET")),
		AppEnv:                 strings.ToLower(envString("APP_ENV", "sandbox")),
		StripeAPIBaseURL:       strings.TrimRight(envString("STRIPE_API_BASE_URL", ""), "/"),

		AccountSessionSecret: strings.TrimSpace(os.Getenv("ACCOUNT_SESSION_SECRET")),
		AccountSessionTTL:    envDuration("ACCOUNT_SESSION_TTL", 30*24*time.Hour),
		LoginTokenTTL:        envDuration("LOGIN_TOKEN_TTL", 30*time.Minute),
		LoginTokenCooldown:   envDuration("LOGIN_TOKEN_COOLDOWN", time.Minute),

		SMTPHost:     envString("SMTP_HOST", ""),
		SMTPPort:     envInt("SMTP_PORT", 587),
		SMTPUsername: strings.TrimSpace(os.Getenv("SMTP_USERNAME")),
		SMTPPassword: os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:     envString("SMTP_FROM", ""),

		ReadHeaderTimeout: envDuration("SERVER_READ_HEADER_TIMEOUT", 5*time.Second),
		ReadTimeout:       envDuration("SERVER_READ_TIMEOUT", 10*time.Second),
		WriteTimeout:      envDuration("SERVER_WRITE_TIMEOUT", 30*time.Second),
		IdleTimeout:       envDuration("SERVER_IDLE_TIMEOUT", 60*time.Second),
	}
	current = cfg
	return cfg
}

func GetFullURL() string {
	if current != nil {
		return current.FullURL
	}
	_ = godotenv.Load()
	fullURL := strings.TrimRight(envString("FULL_URL", "http://localhost:8000"), "/")
	return strings.TrimRight(envString("API_BASE_URL", fullURL), "/")
}

// ErrorStatus is a useful function that will log, write http headers and body for a
// given message, status code and error
func ErrorStatus(message string, httpStatusCode int, w http.ResponseWriter, err error) {
	zap.S().With(err).Error(message)
	WriteError(w, nil, httpStatusCode, "INTERNAL_ERROR", message)
}

func WriteError(w http.ResponseWriter, r *http.Request, httpStatusCode int, code string, message string) {
	w.Header().Set("Content-Type", "application/json")
	requestID := ""
	if r != nil {
		requestID = r.Header.Get("X-Request-Id")
	}
	w.WriteHeader(httpStatusCode)
	b, _ := json.Marshal(models.ErrorResponse{Error: models.Error{Code: code, Message: message, RequestID: requestID}})
	_, _ = w.Write(b)
}

// setLogger is a helper function to set the Logger based on the environment.
func setLogger(env string) (*zap.Logger, error) {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.TimeKey = "ts"

	level := zap.InfoLevel
	if env == "development" || env == "local" {
		level = zap.DebugLevel
	}

	var cores []zapcore.Core
	if envBool("LOG_TO_STDOUT", true) {
		cores = append(cores, zapcore.NewCore(zapcore.NewJSONEncoder(encoderConfig), zapcore.AddSync(os.Stdout), level))
	}

	logDir := envString("LOG_DIR", "logs")
	var logErr error
	if logDir != "" {
		writer, err := newDailyLogWriter(logDir, time.Now)
		if err != nil {
			logErr = err
		} else {
			cores = append(cores, zapcore.NewCore(zapcore.NewJSONEncoder(encoderConfig), zapcore.AddSync(writer), level))
		}
	}

	if len(cores) > 0 {
		return zap.New(zapcore.NewTee(cores...), zap.AddCaller()), logErr
	}

	switch env {
	case "production":
		return zap.NewProduction()
	case "development":
		return zap.NewDevelopment()
	case "local":
		return zap.NewExample(), nil
	default:
		return zap.NewExample(), fmt.Errorf("cannon find ENV car so defaulting to debug logging")
	}
}

type dailyLogWriter struct {
	dir  string
	now  func() time.Time
	mu   sync.Mutex
	date string
	file *os.File
}

func newDailyLogWriter(dir string, now func() time.Time) (*dailyLogWriter, error) {
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, err
	}
	return &dailyLogWriter{dir: dir, now: now}, nil
}

func (w *dailyLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	date := w.now().Format("2006-01-02")
	if w.file == nil || w.date != date {
		if w.file != nil {
			_ = w.file.Close()
		}
		path := filepath.Join(w.dir, date+".log")
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
		if err != nil {
			return 0, err
		}
		w.file = file
		w.date = date
	}
	return w.file.Write(p)
}

func (w *dailyLogWriter) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	return w.file.Sync()
}

func envString(key string, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(v)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func envBool(key string, fallback bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	if seconds, err := strconv.Atoi(v); err == nil {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

func envCSV(key string) []string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func envCSVWithDefault(key string, fallback []string) []string {
	values := envCSV(key)
	if len(values) == 0 {
		return append([]string(nil), fallback...)
	}
	return values
}
