package config

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/models"
	"github.com/joho/godotenv"
)

// Config holds the project config values
type Config struct {
	BaseURL string
	FullURL string
	Port    string

	TrustedProxyCIDRs string
	AllowedOrigins    []string
	MaxBodyBytes      int64
	MaxQueryLength    int

	AnonymousRateLimitPerMinute int
	AnonymousRateBurst          int
	RateLimitClientTTL          time.Duration

	CacheMaxEntries      int
	ModCacheTTL          time.Duration
	ModCacheStale        time.Duration
	ListCacheTTL         time.Duration
	ListCacheStale       time.Duration
	NotFoundCacheTTL     time.Duration
	CacheRefreshTimeout  time.Duration
	CacheRefreshParallel int

	UpstreamTimeout     time.Duration
	UpstreamRetries     int
	UpstreamConcurrency int
	UpstreamUserAgent   string

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

	cfg := &Config{
		BaseURL: envString("BASE_URL", "localhost"),
		FullURL: strings.TrimRight(envString("FULL_URL", "http://localhost:8000"), "/"),
		Port:    envString("PORT", "8000"),

		TrustedProxyCIDRs: envString("TRUSTED_PROXY_CIDRS", ""),
		AllowedOrigins:    envCSV("CORS_ALLOWED_ORIGINS"),
		MaxBodyBytes:      int64(envInt("MAX_BODY_BYTES", 1048576)),
		MaxQueryLength:    envInt("MAX_QUERY_LENGTH", 2048),

		AnonymousRateLimitPerMinute: envInt("ANON_RATE_LIMIT_PER_MINUTE", 60),
		AnonymousRateBurst:          envInt("ANON_RATE_BURST", 20),
		RateLimitClientTTL:          envDuration("RATE_LIMIT_CLIENT_TTL", 10*time.Minute),

		CacheMaxEntries:      envInt("CACHE_MAX_ENTRIES", 1000),
		ModCacheTTL:          envDuration("CACHE_MOD_TTL", time.Hour),
		ModCacheStale:        envDuration("CACHE_MOD_STALE", 24*time.Hour),
		ListCacheTTL:         envDuration("CACHE_LIST_TTL", 10*time.Minute),
		ListCacheStale:       envDuration("CACHE_LIST_STALE", time.Hour),
		NotFoundCacheTTL:     envDuration("CACHE_NOT_FOUND_TTL", 10*time.Minute),
		CacheRefreshTimeout:  envDuration("CACHE_REFRESH_TIMEOUT", 20*time.Second),
		CacheRefreshParallel: envInt("CACHE_REFRESH_CONCURRENCY", 8),

		UpstreamTimeout:     envDuration("UPSTREAM_TIMEOUT", 15*time.Second),
		UpstreamRetries:     envInt("UPSTREAM_RETRIES", 2),
		UpstreamConcurrency: envInt("UPSTREAM_CONCURRENCY", 4),
		UpstreamUserAgent:   envString("UPSTREAM_USER_AGENT", "Cedarline Reforger Mods API/1.0 (+https://cedarline.digital)"),

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
	return strings.TrimRight(envString("FULL_URL", "http://localhost:8000"), "/")
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

// setLogger is a helper function to set the Logger based on the environment
func setLogger(env string) (*zap.Logger, error) {
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
