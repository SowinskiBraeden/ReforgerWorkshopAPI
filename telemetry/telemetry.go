// Package telemetry is the observability backbone of the service. It owns a
// dedicated SQLite database holding one authoritative record per inbound HTTP
// request, structured errors, background jobs, structured application logs,
// and pre-aggregated usage tables, plus the internal admin users and audit
// trail. See docs/observability.md for the architecture and privacy model.
package telemetry

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
)

// Request source categories. Every request event carries exactly one.
const (
	SourceWebsite         = "website"          // HTML pages, static assets, robots/sitemap
	SourceInternalWeb     = "internal-web"     // the site's own JS calling the API same-origin
	SourceExternalAPI     = "api-key"          // authenticated external API integration
	SourceAnonymousAPI    = "api-anon"         // anonymous programmatic API usage
	SourceInternalService = "internal-service" // trusted service-to-service traffic
	SourceHealth          = "health"
	SourceMonitoring      = "monitoring"
	SourceCrawler         = "crawler"
	SourceAICrawler       = "ai-crawler"
	SourceBot             = "bot"
	SourceBrowser         = "browser" // browser hitting API routes directly
	SourceAdmin           = "admin"
	SourceUnknown         = "unknown"
)

// Client kinds derived from the user agent, independent of source.
const (
	ClientKindBrowser = "browser"
	ClientKindCLI     = "cli"
	ClientKindServer  = "server"
	ClientKindCrawler = "crawler"
	ClientKindMonitor = "monitor"
	ClientKindUnknown = "unknown"
)

// Authentication types.
const (
	AuthNone        = "none"
	AuthAPIKey      = "api_key"
	AuthInternalKey = "internal_key"
	AuthSession     = "session"
	AuthAdmin       = "admin"
	AuthRejected    = "rejected"
)

// Cache statuses stored on request events.
const (
	CacheHit    = "HIT"
	CacheStale  = "STALE"
	CacheMiss   = "MISS"
	CacheBypass = "BYPASS"
)

// Refresh results stored alongside the cache status of a request.
const (
	RefreshQueued     = "REFRESH_QUEUED"
	RefreshInProgress = "REFRESH_IN_PROGRESS"
	RefreshFailed     = "REFRESH_FAILED"
)

// Background job kinds.
const (
	JobKindCacheRefresh = "cache_refresh"
	JobKindIndexPage    = "index_page"
	JobKindIndexDetail  = "index_detail"
)

// Background job statuses.
const (
	JobQueued    = "queued"
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"
	JobExpired   = "expired"
	JobRejected  = "rejected"
)

// Error resolution states.
const (
	ErrorOpen          = "open"
	ErrorAcknowledged  = "acknowledged"
	ErrorInvestigating = "investigating"
	ErrorResolved      = "resolved"
	ErrorIgnored       = "ignored"
)

// RequestEvent is the authoritative record of one inbound HTTP request.
// Exactly one event exists per request; cache lookups, refresh jobs and other
// internal activity never create request events.
type RequestEvent struct {
	ID             int64     `json:"id"`
	RequestID      string    `json:"requestId"`
	TraceID        string    `json:"traceId,omitempty"`
	At             time.Time `json:"at"`
	Method         string    `json:"method"`
	RouteTemplate  string    `json:"routeTemplate"`
	RequestPath    string    `json:"requestPath"`
	Query          string    `json:"query,omitempty"` // sanitized, secrets redacted
	EndpointGroup  string    `json:"endpointGroup"`
	Status         int       `json:"status"`
	DurationMs     float64   `json:"durationMs"`
	ResponseBytes  int64     `json:"responseBytes"`
	RequestBytes   int64     `json:"requestBytes,omitempty"`
	APIVersion     string    `json:"apiVersion,omitempty"` // "v1" or "legacy"
	Source         string    `json:"source"`
	ClientKind     string    `json:"clientKind"`
	AuthType       string    `json:"authType"`
	AccountID      string    `json:"accountID,omitempty"`
	APIKeyID       string    `json:"apiKeyID,omitempty"`
	APIClientID    string    `json:"apiClientID,omitempty"`
	ClientName     string    `json:"clientName,omitempty"` // self-reported or derived
	ClientVersion  string    `json:"clientVersion,omitempty"`
	ClientVerified bool      `json:"clientVerified"`
	UserAgent      string    `json:"userAgent,omitempty"`
	CountryCode    string    `json:"countryCode"`
	ASN            string    `json:"asn,omitempty"`
	NetworkName    string    `json:"networkName,omitempty"`
	NetworkID      string    `json:"networkID,omitempty"` // rotating HMAC id; never an IP
	IsHosting      string    `json:"isHosting"`           // "hosting" | "residential" | "unknown"
	ViaProxy       bool      `json:"viaProxy"`
	CacheStatus    string    `json:"cacheStatus,omitempty"`
	RefreshResult  string    `json:"refreshResult,omitempty"`
	RateLimited    bool      `json:"rateLimited"`
	RateLimitLimit int       `json:"rateLimitLimit,omitempty"`
	RateBucket     string    `json:"rateBucket,omitempty"`
	ErrorCategory  string    `json:"errorCategory,omitempty"`
	ErrorCode      string    `json:"errorCode,omitempty"`
	SearchTerm     string    `json:"searchTerm,omitempty"` // normalized, length-limited
	ResultCount    int       `json:"resultCount"`          // -1 when not applicable
	ModID          string    `json:"modID,omitempty"`
	AppVersion     string    `json:"appVersion,omitempty"`
	InstanceID     string    `json:"instanceID,omitempty"`
	DedupeKey      string    `json:"-"` // set by the historical importer only
}

// ErrorEvent is one structured application/request error.
type ErrorEvent struct {
	ID            int64     `json:"id"`
	ErrorID       string    `json:"errorId"`
	RequestID     string    `json:"requestId,omitempty"`
	TraceID       string    `json:"traceId,omitempty"`
	JobID         string    `json:"jobId,omitempty"`
	At            time.Time `json:"at"`
	Severity      string    `json:"severity"`
	Category      string    `json:"category"`
	Code          string    `json:"code,omitempty"`
	Message       string    `json:"message"` // safe message, no secrets/IPs
	Stack         string    `json:"stack,omitempty"`
	Method        string    `json:"method,omitempty"`
	RouteTemplate string    `json:"routeTemplate,omitempty"`
	RequestPath   string    `json:"requestPath,omitempty"`
	Status        int       `json:"status,omitempty"`
	Source        string    `json:"source,omitempty"`
	AccountID     string    `json:"accountId,omitempty"`
	APIKeyID      string    `json:"apiKeyId,omitempty"`
	ClientName    string    `json:"clientName,omitempty"`
	CountryCode   string    `json:"countryCode,omitempty"`
	NetworkID     string    `json:"networkId,omitempty"`
	AppVersion    string    `json:"appVersion,omitempty"`
	Fingerprint   string    `json:"fingerprint"`
	Resolution    string    `json:"resolution"`
	Notes         string    `json:"notes,omitempty"`
}

// JobEvent describes a background job lifecycle update. Recording is
// upsert-by-JobID so queued/running/finished updates land on one row.
type JobEvent struct {
	JobID          string
	RequestID      string
	ParentJobID    string
	Kind           string
	ResourceKey    string
	ResourceURL    string
	Queue          string
	Priority       string
	EnqueuedAt     time.Time
	StartedAt      time.Time
	FinishedAt     time.Time
	Worker         int
	Attempt        int
	Status         string
	StatusCode     int
	FailureReason  string
	Deduplicated   bool
	PanicRecovered bool
	DedupeKey      string
}

// LogEvent is one structured application log line destined for the explorer.
type LogEvent struct {
	ID            int64     `json:"id"`
	At            time.Time `json:"at"`
	Level         string    `json:"level"`
	Caller        string    `json:"caller,omitempty"`
	Message       string    `json:"message"`
	RequestID     string    `json:"requestId,omitempty"`
	TraceID       string    `json:"traceId,omitempty"`
	JobID         string    `json:"jobId,omitempty"`
	Route         string    `json:"route,omitempty"`
	Path          string    `json:"path,omitempty"`
	Status        int       `json:"status,omitempty"`
	ErrorCategory string    `json:"errorCategory,omitempty"`
	CountryCode   string    `json:"countryCode,omitempty"`
	NetworkID     string    `json:"networkId,omitempty"`
	AccountID     string    `json:"accountId,omitempty"`
	ClientName    string    `json:"clientName,omitempty"`
	APIKeyID      string    `json:"apiKeyId,omitempty"`
	CacheStatus   string    `json:"cacheStatus,omitempty"`
	InstanceID    string    `json:"instanceId,omitempty"`
	AppVersion    string    `json:"appVersion,omitempty"`
	Fields        string    `json:"fields,omitempty"` // redacted JSON of remaining fields
	DedupeKey     string    `json:"-"`
}

// NewID returns a random 16-byte hex identifier with a type prefix.
func NewID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return prefix + "_" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

// NewRequestID returns a compact random request identifier.
func NewRequestID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
	}
	return hex.EncodeToString(b[:])
}
