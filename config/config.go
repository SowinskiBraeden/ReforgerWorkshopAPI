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
	RateLimitClientTTL          time.Duration

	CacheMaxEntries          int
	ModCacheTTL              time.Duration
	ModCacheStale            time.Duration
	ListCacheTTL             time.Duration
	ListCacheStale           time.Duration
	NotFoundCacheTTL         time.Duration
	CacheRefreshTimeout      time.Duration
	CacheRefreshParallel     int
	CacheRefreshQueueSize    int
	CacheRefreshJobRetention time.Duration
	CacheRefreshRetryAfter   time.Duration

	UpstreamTimeout     time.Duration
	UpstreamRetries     int
	UpstreamConcurrency int
	UpstreamUserAgent   string

	InternalMetricsEnabled bool
	InternalMetricsToken   string

	MetricsPersistenceEnabled bool
	MetricsStatePath          string
	MetricsFlushInterval      time.Duration

	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}

var current *Config

// New sets up all config related services
func New() *Config {

	_ = godotenv.Load()

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

		AnonymousRateLimitPerMinute: envInt("ANON_RATE_LIMIT_PER_MINUTE", 60),
		AnonymousRateBurst:          envInt("ANON_RATE_BURST", 20),
		RateLimitClientTTL:          envDuration("RATE_LIMIT_CLIENT_TTL", 10*time.Minute),

		CacheMaxEntries:          envInt("CACHE_MAX_ENTRIES", 1000),
		ModCacheTTL:              envDuration("CACHE_MOD_TTL", time.Hour),
		ModCacheStale:            envDuration("CACHE_MOD_STALE", 24*time.Hour),
		ListCacheTTL:             envDuration("CACHE_LIST_TTL", 10*time.Minute),
		ListCacheStale:           envDuration("CACHE_LIST_STALE", time.Hour),
		NotFoundCacheTTL:         envDuration("CACHE_NOT_FOUND_TTL", 10*time.Minute),
		CacheRefreshTimeout:      envDuration("CACHE_REFRESH_TIMEOUT", 20*time.Second),
		CacheRefreshParallel:     envInt("CACHE_REFRESH_CONCURRENCY", 8),
		CacheRefreshQueueSize:    envInt("CACHE_REFRESH_QUEUE_SIZE", 64),
		CacheRefreshJobRetention: envDuration("CACHE_REFRESH_JOB_RETENTION", 15*time.Minute),
		CacheRefreshRetryAfter:   envDuration("CACHE_REFRESH_RETRY_AFTER", 2*time.Second),

		UpstreamTimeout:     envDuration("UPSTREAM_TIMEOUT", 15*time.Second),
		UpstreamRetries:     envInt("UPSTREAM_RETRIES", 2),
		UpstreamConcurrency: envInt("UPSTREAM_CONCURRENCY", 4),
		UpstreamUserAgent:   envString("UPSTREAM_USER_AGENT", "Cedarline Reforger Mods API/1.0 (+https://cedarline.digital)"),

		InternalMetricsEnabled: envBool("INTERNAL_METRICS_ENABLED", true),
		InternalMetricsToken:   strings.TrimSpace(os.Getenv("INTERNAL_METRICS_TOKEN")),

		MetricsPersistenceEnabled: envBool("METRICS_PERSISTENCE_ENABLED", false),
		MetricsStatePath:          envString("METRICS_STATE_PATH", ""),
		MetricsFlushInterval:      envDuration("METRICS_FLUSH_INTERVAL", 15*time.Second),

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
