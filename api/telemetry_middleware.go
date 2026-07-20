package api

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/telemetry"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

// RequestAnnotations is a mutable carrier the telemetry wrapper places into
// the request context. Inner layers (rate limiter, auth resolver, handlers)
// annotate it; the wrapper reads it once after the response is written and
// emits exactly one request event.
type RequestAnnotations struct {
	mu sync.Mutex

	AuthType       string
	AccountID      string
	APIKeyID       string
	APIClientID    string
	Plan           string
	ClientVerified bool

	RateLimited    bool
	RateLimitLimit int
	RateBucket     string

	ResultCount int // -1 = not applicable

	ErrorCategory string
	ErrorCode     string
}

type annotationsKey struct{}

// AnnotationsFromContext returns the request annotations, or nil outside the
// telemetry wrapper (tests, direct handler calls). All setters are nil-safe.
func AnnotationsFromContext(ctx context.Context) *RequestAnnotations {
	annotations, _ := ctx.Value(annotationsKey{}).(*RequestAnnotations)
	return annotations
}

func (a *RequestAnnotations) SetAuth(authType string, accountID string, keyID string, clientID string, plan string, verified bool) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.AuthType, a.AccountID, a.APIKeyID, a.APIClientID, a.Plan, a.ClientVerified =
		authType, accountID, keyID, clientID, plan, verified
}

func (a *RequestAnnotations) SetRateLimit(rejected bool, limit int, bucket string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if rejected {
		a.RateLimited = true
	}
	a.RateLimitLimit = limit
	a.RateBucket = bucket
}

func (a *RequestAnnotations) SetResultCount(count int) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ResultCount = count
}

func (a *RequestAnnotations) SetError(category string, code string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ErrorCategory, a.ErrorCode = category, code
}

// annotationValues is the lock-free snapshot of RequestAnnotations taken
// once after the response is written.
type annotationValues struct {
	AuthType       string
	AccountID      string
	APIKeyID       string
	APIClientID    string
	Plan           string
	ClientVerified bool
	RateLimited    bool
	RateLimitLimit int
	RateBucket     string
	ResultCount    int
	ErrorCategory  string
	ErrorCode      string
}

func (a *RequestAnnotations) snapshot() annotationValues {
	a.mu.Lock()
	defer a.mu.Unlock()
	return annotationValues{
		AuthType: a.AuthType, AccountID: a.AccountID, APIKeyID: a.APIKeyID,
		APIClientID: a.APIClientID, Plan: a.Plan, ClientVerified: a.ClientVerified,
		RateLimited: a.RateLimited, RateLimitLimit: a.RateLimitLimit, RateBucket: a.RateBucket,
		ResultCount: a.ResultCount, ErrorCategory: a.ErrorCategory, ErrorCode: a.ErrorCode,
	}
}

// TelemetryMiddleware is the outermost HTTP wrapper. It wraps the router
// itself (so NotFound and unrouted requests are covered), recovers panics,
// and records exactly one request event per inbound request. Cache lookups,
// refresh jobs, and other internal activity never pass through it.
type TelemetryMiddleware struct {
	cfg            config.Config
	recorder       *telemetry.Recorder
	anonymizer     *telemetry.Anonymizer
	router         *mux.Router
	trustedProxies []*net.IPNet
	internalCIDRs  []*net.IPNet
	version        string
	instanceID     string
	publicHost     string
}

func NewTelemetryMiddleware(cfg config.Config, recorder *telemetry.Recorder, router *mux.Router, version string) *TelemetryMiddleware {
	publicHost := ""
	if parsed, err := url.Parse(cfg.PublicBaseURL); err == nil {
		publicHost = parsed.Host
	}
	return &TelemetryMiddleware{
		cfg:            cfg,
		recorder:       recorder,
		anonymizer:     telemetry.NewAnonymizer(firstNonEmpty(cfg.TelemetryHashSecret, cfg.APIKeyHashSecret), cfg.AnonIDRotation),
		router:         router,
		trustedProxies: parseTrustedProxies(cfg.TrustedProxyCIDRs),
		internalCIDRs:  parseTrustedProxies(cfg.MetricsInternalCIDRs),
		version:        version,
		instanceID:     cfg.InstanceID,
		publicHost:     publicHost,
	}
}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func incomingRequestID(r *http.Request) string {
	if existing := strings.TrimSpace(r.Header.Get("X-Request-Id")); requestIDPattern.MatchString(existing) {
		return existing
	}
	return telemetry.NewRequestID()
}

// countingRecorder captures status code and response size.
type countingRecorder struct {
	http.ResponseWriter
	statusCode int
	bytes      int64
	wrote      bool
}

func (r *countingRecorder) WriteHeader(statusCode int) {
	if r.wrote {
		return
	}
	r.wrote = true
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *countingRecorder) Write(p []byte) (int, error) {
	if !r.wrote {
		r.wrote = true
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += int64(n)
	return n, err
}

func (t *TelemetryMiddleware) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := incomingRequestID(r)
		traceID := strings.TrimSpace(r.Header.Get("X-Trace-Id"))
		if !requestIDPattern.MatchString(traceID) {
			traceID = ""
		}
		r.Header.Set("X-Request-Id", requestID)
		recorder := &countingRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		recorder.Header().Set("X-Request-Id", requestID)

		annotations := &RequestAnnotations{AuthType: telemetry.AuthNone, ResultCount: -1}
		ctx := context.WithValue(r.Context(), annotationsKey{}, annotations)
		r = r.WithContext(ctx)

		panicked := false
		var panicValue any
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					panicked = true
					panicValue = recovered
					if !recorder.wrote {
						config.WriteError(recorder, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error.")
					}
				}
			}()
			t.router.ServeHTTP(recorder, r)
		}()

		t.finish(r, recorder, annotations, requestID, traceID, start, panicked, panicValue)
	})
}

func (t *TelemetryMiddleware) finish(r *http.Request, recorder *countingRecorder, annotations *RequestAnnotations, requestID string, traceID string, start time.Time, panicked bool, panicValue any) {
	latency := time.Since(start)
	now := start.UTC()
	note := annotations.snapshot()

	routeTemplate, routeVars := t.routeTemplate(r)
	if telemetry.IsIgnoredMetricsPath(r.URL.Path) {
		return
	}
	sanitizedQuery := telemetry.SanitizeQuery(r.URL.RawQuery)
	sanitizedPath := telemetry.SanitizePath(r.URL.Path)
	endpointGroup := telemetry.EndpointGroup(routeTemplate, sanitizedQuery)

	// The client IP is used only inside this function to derive privacy-safe
	// fields; it is never stored or logged.
	clientIP, viaProxy := t.clientIP(r)
	networkID := t.anonymizer.NetworkID(clientIP, now)
	countryCode := t.countryCode(r, viaProxy)
	asn, networkName := t.networkInfo(r, viaProxy)
	isHosting := telemetry.ClassifyHosting(networkName, asn)

	authType := note.AuthType
	if authType == "" {
		authType = telemetry.AuthNone
	}
	source := telemetry.ClassifySource(telemetry.ClassifyInput{
		RouteTemplate:  routeTemplate,
		Path:           r.URL.Path,
		UserAgent:      r.UserAgent(),
		AuthType:       authType,
		IsInternalIP:   t.isInternalIP(clientIP),
		InternalHeader: telemetry.VerifyInternalHeader(r, t.cfg.InternalTrafficSecret, time.Now()),
		SameOrigin:     t.sameOrigin(r),
		IsAPIRoute:     isAPIRoute(routeTemplate),
		IsAdminRoute:   strings.HasPrefix(routeTemplate, "/internal"),
		IsHealthRoute:  endpointGroup == "health",
	})

	clientName, clientVersion, verified := t.clientIdentity(r, note)
	status := recorder.statusCode
	errorCategory, errorCode := note.ErrorCategory, note.ErrorCode
	if errorCategory == "" {
		errorCategory = categorizeStatus(status, note.RateLimited, panicked)
	}

	event := telemetry.RequestEvent{
		RequestID:      requestID,
		TraceID:        traceID,
		At:             now,
		Method:         strings.ToUpper(r.Method),
		RouteTemplate:  routeTemplate,
		RequestPath:    sanitizedPath,
		Query:          sanitizedQuery,
		EndpointGroup:  endpointGroup,
		Status:         status,
		DurationMs:     float64(latency.Microseconds()) / 1000,
		ResponseBytes:  recorder.bytes,
		RequestBytes:   r.ContentLength,
		APIVersion:     apiVersionFor(routeTemplate),
		Source:         source,
		ClientKind:     telemetry.ClassifyClientKind(r.UserAgent()),
		AuthType:       authType,
		AccountID:      note.AccountID,
		APIKeyID:       note.APIKeyID,
		APIClientID:    note.APIClientID,
		ClientName:     clientName,
		ClientVersion:  clientVersion,
		ClientVerified: verified,
		UserAgent:      telemetry.SanitizeText(r.UserAgent(), 240),
		CountryCode:    normalizeCountryCode(countryCode),
		ASN:            asn,
		NetworkName:    networkName,
		NetworkID:      networkID,
		IsHosting:      isHosting,
		ViaProxy:       viaProxy,
		CacheStatus:    strings.ToUpper(strings.TrimSpace(recorder.Header().Get("X-Cache"))),
		RefreshResult:  strings.ToUpper(strings.TrimSpace(recorder.Header().Get("X-Refresh-Status"))),
		RateLimited:    note.RateLimited,
		RateLimitLimit: note.RateLimitLimit,
		RateBucket:     sanitizeRateBucket(note.RateBucket),
		ErrorCategory:  errorCategory,
		ErrorCode:      errorCode,
		SearchTerm:     searchTermFor(endpointGroup, r.URL.Query()),
		ResultCount:    note.ResultCount,
		ModID:          modIDFor(routeTemplate, routeVars),
		AppVersion:     t.version,
		InstanceID:     t.instanceID,
	}
	t.recorder.RecordRequest(event)

	logger := zap.S().With(
		"requestId", requestID,
		"networkId", networkID,
		"countryCode", event.CountryCode,
		"method", event.Method,
		"route", routeTemplate,
		"path", sanitizedPath,
		"query", sanitizedQuery,
		"status", status,
		"latencyMs", latency.Milliseconds(),
		"source", source,
		"client", clientName,
		"userAgent", event.UserAgent,
	)
	if panicked {
		logger.Errorw("request panicked", "panic", telemetry.SanitizeText(stringFromAny(panicValue), 300))
	} else {
		logger.Infow("request completed")
	}

	if status >= 500 || panicked {
		severity := "error"
		message := http.StatusText(status)
		stack := ""
		if panicked {
			severity = "fatal"
			message = "panic: " + telemetry.SanitizeText(stringFromAny(panicValue), 300)
			stack = telemetry.SanitizeText(string(debug.Stack()), 8000)
		}
		t.recorder.RecordError(telemetry.ErrorEvent{
			RequestID:     requestID,
			TraceID:       traceID,
			At:            now,
			Severity:      severity,
			Category:      errorCategory,
			Code:          errorCode,
			Message:       message,
			Stack:         stack,
			Method:        event.Method,
			RouteTemplate: routeTemplate,
			RequestPath:   sanitizedPath,
			Status:        status,
			Source:        source,
			AccountID:     note.AccountID,
			APIKeyID:      note.APIKeyID,
			ClientName:    clientName,
			CountryCode:   event.CountryCode,
			NetworkID:     networkID,
			AppVersion:    t.version,
		})
	}
}

// routeTemplate resolves the mux route template and path vars for the
// request, or a stable marker for unmatched requests.
func (t *TelemetryMiddleware) routeTemplate(r *http.Request) (string, map[string]string) {
	var match mux.RouteMatch
	if t.router.Match(r, &match) && match.Route != nil {
		if template, err := match.Route.GetPathTemplate(); err == nil && template != "" {
			return template, match.Vars
		}
	}
	return "(unmatched)", nil
}

// clientIP resolves the peer address, honoring forwarding headers only when
// the direct peer is a trusted proxy. The result is used transiently and
// never persisted.
func (t *TelemetryMiddleware) clientIP(r *http.Request) (string, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remoteIP := net.ParseIP(strings.TrimSpace(host))
	if remoteIP == nil {
		return "", false
	}
	if !t.isTrustedProxy(remoteIP) {
		return remoteIP.String(), false
	}
	if cfIP := net.ParseIP(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); cfIP != nil {
		return cfIP.String(), true
	}
	for _, part := range strings.Split(r.Header.Get("X-Forwarded-For"), ",") {
		if ip := net.ParseIP(strings.TrimSpace(part)); ip != nil {
			return ip.String(), true
		}
	}
	if realIP := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); realIP != nil {
		return realIP.String(), true
	}
	return remoteIP.String(), true
}

func (t *TelemetryMiddleware) isTrustedProxy(ip net.IP) bool {
	for _, cidr := range t.trustedProxies {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

func (t *TelemetryMiddleware) isInternalIP(clientIP string) bool {
	ip := net.ParseIP(strings.TrimSpace(clientIP))
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return true
	}
	for _, cidr := range t.internalCIDRs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

func (t *TelemetryMiddleware) countryCode(r *http.Request, viaProxy bool) string {
	if !viaProxy {
		return "ZZ"
	}
	return requestCountryCodeFromHeaders(r)
}

// networkInfo reads ASN/organization from edge headers when present. These
// require a proxy/worker that injects them; absent headers leave the fields
// empty ("unavailable" in the UI).
func (t *TelemetryMiddleware) networkInfo(r *http.Request, viaProxy bool) (string, string) {
	if !viaProxy {
		return "", ""
	}
	asn := ""
	for _, header := range []string{"CF-ASN", "X-Client-ASN", "X-ASN"} {
		if value := telemetry.SanitizeText(r.Header.Get(header), 20); value != "" {
			asn = value
			break
		}
	}
	name := ""
	for _, header := range []string{"CF-ASN-Org", "X-Client-Network", "X-ASN-Org"} {
		if value := telemetry.SanitizeText(r.Header.Get(header), 120); value != "" {
			name = value
			break
		}
	}
	return asn, name
}

// sameOrigin reports whether the request originated from the site's own pages.
func (t *TelemetryMiddleware) sameOrigin(r *http.Request) bool {
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "same-origin") {
		return true
	}
	for _, header := range []string{"Origin", "Referer"} {
		value := strings.TrimSpace(r.Header.Get(header))
		if value == "" {
			continue
		}
		parsed, err := url.Parse(value)
		if err != nil {
			continue
		}
		if parsed.Host != "" && (parsed.Host == r.Host || (t.publicHost != "" && parsed.Host == t.publicHost)) {
			return true
		}
	}
	return false
}

// clientIdentity resolves the client application name/version. Names are only
// verified when the request authenticated with an API key.
func (t *TelemetryMiddleware) clientIdentity(r *http.Request, note annotationValues) (string, string, bool) {
	name := telemetry.SanitizeText(firstNonEmpty(
		r.Header.Get("X-ReforgerMods-Client"),
		r.Header.Get("X-API-Client"),
	), 80)
	version := telemetry.SanitizeText(r.Header.Get("X-ReforgerMods-Client-Version"), 40)
	verified := note.ClientVerified && note.APIKeyID != ""
	if name == "" {
		name = telemetry.ClientNameFromUserAgent(r.UserAgent())
		verified = false
	}
	return name, version, verified && name != ""
}

// jsonAccountBillingRoutes are the non-HTML account/billing endpoints; the
// HTML pages (/account/billing/, /account/api-keys/, /billing/success/) are
// website traffic.
var jsonAPIRouteTemplates = map[string]struct{}{
	"/mod/{id}": {}, "/search": {}, "/rate-limits": {}, "/refresh/jobs/{id}": {},
	"/health":        {},
	"/account/login": {}, "/account/verify": {}, "/account/logout": {},
	"/account/session": {}, "/account/api-keys": {}, "/account/api-keys/{id}": {},
	"/billing/checkout": {}, "/billing/session": {}, "/billing/portal": {},
	"/stripe/webhook": {},
}

func isAPIRoute(routeTemplate string) bool {
	if strings.HasPrefix(routeTemplate, "/v1/") {
		return true
	}
	_, ok := jsonAPIRouteTemplates[routeTemplate]
	return ok
}

func apiVersionFor(routeTemplate string) string {
	if strings.HasPrefix(routeTemplate, "/v1/") {
		return "v1"
	}
	if isAPIRoute(routeTemplate) {
		return "legacy"
	}
	return ""
}

func categorizeStatus(status int, rateLimited bool, panicked bool) string {
	switch {
	case panicked:
		return "panic"
	case rateLimited || status == http.StatusTooManyRequests:
		return "rate_limited"
	case status == http.StatusNotFound:
		return "not_found"
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "auth"
	case status >= 500:
		return "server_error"
	case status >= 400:
		return "client_error"
	default:
		return ""
	}
}

func searchTermFor(endpointGroup string, query url.Values) string {
	if endpointGroup != "search" {
		return ""
	}
	return telemetry.NormalizeSearchTerm(query.Get("search"))
}

var modIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

func modIDFor(routeTemplate string, routeVars map[string]string) string {
	if !strings.HasSuffix(routeTemplate, "/mod/{id}") && !strings.HasPrefix(routeTemplate, "/mods/{id}") {
		return ""
	}
	id := strings.ToUpper(strings.TrimSpace(routeVars["id"]))
	if modIDPattern.MatchString(id) {
		return id
	}
	return ""
}

// sanitizeRateBucket strips embedded IPs from anonymous rate buckets before
// storage; only account/key-scoped buckets keep their identifier.
func sanitizeRateBucket(bucket string) string {
	if bucket == "" {
		return ""
	}
	parts := strings.SplitN(bucket, ":", 3)
	switch {
	case parts[0] == "anonymous":
		return "anonymous"
	case parts[0] == "plan" && len(parts) >= 2 && parts[1] == "free":
		return "plan:free" // the third segment is the client IP
	case parts[0] == "plan":
		return telemetry.SanitizeText(bucket, 100)
	default:
		return telemetry.SanitizeText(parts[0], 40)
	}
}

func stringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case error:
		return v.Error()
	case nil:
		return ""
	default:
		return "unknown panic"
	}
}
