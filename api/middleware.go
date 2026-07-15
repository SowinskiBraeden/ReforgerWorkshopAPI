package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

type contextKey string

const (
	ContextBillingAuthKey contextKey = "billing_auth"
)

type BillingAuth struct {
	Plan      string
	AccountID string
	KeyID     string
}

type RateIdentity struct {
	Bucket        string
	Limit         int
	Burst         int
	RejectStatus  int
	RejectCode    string
	RejectMessage string
}

type IdentityResolver func(r *http.Request, clientIP string) RateIdentity

type MiddlewareConfig struct {
	Config           config.Config
	IdentityResolver IdentityResolver
}

type MiddlewareChain struct {
	cfg               config.Config
	metrics           *Metrics
	identityResolver  IdentityResolver
	trustedProxies    []*net.IPNet
	internalCIDRs     []*net.IPNet
	ownClientPatterns []string
	clients           map[string]*rateClient
	mu                sync.Mutex
}

type rateClient struct {
	limiter  *rate.Limiter
	lastSeen time.Time
	limit    int
	burst    int
}

func NewMiddleware(cfg config.Config, metrics ...*Metrics) *MiddlewareChain {
	var collector *Metrics
	if len(metrics) > 0 {
		collector = metrics[0]
	}
	m := &MiddlewareChain{
		cfg:               cfg,
		metrics:           collector,
		trustedProxies:    parseTrustedProxies(cfg.TrustedProxyCIDRs),
		internalCIDRs:     parseTrustedProxies(cfg.MetricsInternalCIDRs),
		ownClientPatterns: normalizeClientPatterns(cfg.MetricsOwnClientPatterns),
		clients:           make(map[string]*rateClient),
	}
	m.identityResolver = func(_ *http.Request, clientIP string) RateIdentity {
		return RateIdentity{
			Bucket: "anonymous:" + clientIP,
			Limit:  cfg.AnonymousRateLimitPerMinute,
			Burst:  cfg.AnonymousRateBurst,
		}
	}
	go m.cleanup()
	return m
}

func (m *MiddlewareChain) SetIdentityResolver(resolver IdentityResolver) {
	if resolver != nil {
		m.identityResolver = resolver
	}
}

func BillingAuthFromContext(ctx context.Context) (BillingAuth, bool) {
	auth, ok := ctx.Value(ContextBillingAuthKey).(BillingAuth)
	return auth, ok
}

// Middleware is kept for older tests/imports. New code should use NewMiddleware.
func Middleware(next http.Handler) http.Handler {
	return NewMiddleware(*config.New()).Wrap(next)
}

func (m *MiddlewareChain) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := requestID(r)
		clientIP := ""
		countryCode := ""
		r.Header.Set("X-Request-Id", requestID)
		recorder := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		recorder.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
		recorder.Header().Set("X-Request-Id", requestID)
		defer func() {
			latency := time.Since(start)
			if clientIP == "" {
				clientIP = m.ClientIP(r)
			}
			if countryCode == "" {
				countryCode = m.CountryCode(r)
			}
			m.metrics.RecordRequestMetric(RequestMetricDetails{
				Duration:      latency,
				ClientIP:      clientIP,
				CountryCode:   countryCode,
				UserAgent:     r.UserAgent(),
				Source:        m.TrafficSource(r, clientIP),
				Method:        r.Method,
				Path:          r.URL.Path,
				RawQuery:      r.URL.RawQuery,
				StatusCode:    recorder.statusCode,
				CacheStatus:   recorder.Header().Get("X-Cache"),
				EndpointGroup: EndpointGroupForRequest(r.URL.Path, r.URL.RawQuery),
			})
			zap.S().Infow("request completed",
				"requestId", requestID,
				"clientIP", clientIP,
				"countryCode", countryCode,
				"method", r.Method,
				"path", r.URL.Path,
				"query", r.URL.RawQuery,
				"status", recorder.statusCode,
				"latencyMs", latency.Milliseconds(),
				"userAgent", r.UserAgent(),
			)
		}()

		m.setSecurityHeaders(recorder)
		if !m.applyCORS(recorder, r) {
			config.WriteError(recorder, r, http.StatusForbidden, "CORS_FORBIDDEN", "Origin is not allowed.")
			return
		}
		if r.Method == http.MethodOptions {
			recorder.WriteHeader(http.StatusNoContent)
			return
		}
		if len(r.URL.RawQuery) > m.cfg.MaxQueryLength {
			config.WriteError(recorder, r, http.StatusRequestURITooLong, "QUERY_TOO_LONG", "Query string is too long.")
			return
		}
		if m.cfg.MaxBodyBytes > 0 {
			r.Body = http.MaxBytesReader(recorder, r.Body, m.cfg.MaxBodyBytes)
		}

		clientIP = m.ClientIP(r)
		countryCode = m.CountryCode(r)
		identity := m.identityResolver(r, clientIP)
		if identity.RejectStatus > 0 {
			config.WriteError(recorder, r, identity.RejectStatus, identity.RejectCode, identity.RejectMessage)
			return
		}
		plan := strings.TrimPrefix(identity.Bucket, "plan:")
		if i := strings.Index(plan, ":"); i >= 0 {
			plan = plan[:i]
		}
		if plan == "free" || plan == "developer" || plan == "pro" {
			recorder.Header().Set("X-API-Plan", plan)
		}
		if identity.Limit <= 0 {
			identity.Limit = m.cfg.AnonymousRateLimitPerMinute
		}
		if identity.Burst <= 0 {
			identity.Burst = m.cfg.AnonymousRateBurst
		}
		if !m.allow(recorder, r, identity) {
			zap.S().Infow("rate limit rejected", "requestId", requestID, "clientIP", clientIP, "path", r.URL.Path, "bucket", identity.Bucket)
			return
		}

		next.ServeHTTP(recorder, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
	wrote      bool
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	if r.wrote {
		return
	}
	r.wrote = true
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func (m *MiddlewareChain) ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remoteIP := net.ParseIP(strings.TrimSpace(host))
	if remoteIP == nil {
		return host
	}
	if !m.isTrustedProxy(remoteIP) {
		return remoteIP.String()
	}
	if cfIP := net.ParseIP(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); cfIP != nil {
		return cfIP.String()
	}
	for _, part := range strings.Split(r.Header.Get("X-Forwarded-For"), ",") {
		if ip := net.ParseIP(strings.TrimSpace(part)); ip != nil {
			return ip.String()
		}
	}
	if realIP := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); realIP != nil {
		return realIP.String()
	}
	return remoteIP.String()
}

func (m *MiddlewareChain) CountryCode(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remoteIP := net.ParseIP(strings.TrimSpace(host))
	if remoteIP == nil || !m.isTrustedProxy(remoteIP) {
		return "ZZ"
	}
	return requestCountryCodeFromHeaders(r)
}

func (m *MiddlewareChain) TrafficSource(r *http.Request, clientIP string) string {
	if userAgentMatches(r.UserAgent(), m.ownClientPatterns) {
		return TrafficSourceOwnPanel
	}
	ip := net.ParseIP(strings.TrimSpace(clientIP))
	if ip == nil {
		return TrafficSourceUnknown
	}
	if ip.IsLoopback() {
		return TrafficSourceInternalLoopback
	}
	for _, cidr := range m.internalCIDRs {
		if cidr.Contains(ip) {
			if ip.IsLoopback() {
				return TrafficSourceInternalLoopback
			}
			return TrafficSourceInternal
		}
	}
	if ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return TrafficSourceInternal
	}
	return TrafficSourceExternal
}

func normalizeClientPatterns(patterns []string) []string {
	out := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern != "" {
			out = append(out, pattern)
		}
	}
	return out
}

func userAgentMatches(userAgent string, patterns []string) bool {
	userAgent = strings.ToLower(strings.TrimSpace(userAgent))
	if userAgent == "" {
		return false
	}
	for _, pattern := range patterns {
		if pattern != "" && strings.Contains(userAgent, pattern) {
			return true
		}
	}
	return false
}

func requestCountryCodeFromHeaders(r *http.Request) string {
	for _, header := range []string{
		"CF-IPCountry",
		"CloudFront-Viewer-Country",
		"X-Vercel-IP-Country",
		"X-Country-Code",
	} {
		if code := normalizeMetricsCountryCode(r.Header.Get(header)); code != "" {
			return code
		}
	}
	return "ZZ"
}

func normalizeMetricsCountryCode(raw string) string {
	code := strings.ToUpper(strings.TrimSpace(raw))
	if len(code) != 2 || code == "XX" {
		return ""
	}
	for _, r := range code {
		if r < 'A' || r > 'Z' {
			return ""
		}
	}
	return code
}

func (m *MiddlewareChain) allow(w http.ResponseWriter, r *http.Request, identity RateIdentity) bool {
	m.mu.Lock()
	client, ok := m.clients[identity.Bucket]
	if !ok || client.limit != identity.Limit || client.burst != identity.Burst {
		client = &rateClient{
			limiter: rate.NewLimiter(rate.Every(time.Minute/time.Duration(identity.Limit)), identity.Burst),
			limit:   identity.Limit,
			burst:   identity.Burst,
		}
		m.clients[identity.Bucket] = client
	}
	client.lastSeen = time.Now()
	reservation := client.limiter.Reserve()
	remaining := int(client.limiter.Tokens())
	m.mu.Unlock()

	w.Header().Set("RateLimit-Limit", fmt.Sprintf("%d", identity.Limit))
	w.Header().Set("RateLimit-Remaining", fmt.Sprintf("%d", maxInt(remaining, 0)))
	w.Header().Set("RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(time.Minute).Unix()))

	delay := reservation.Delay()
	if !reservation.OK() || delay > 0 {
		reservation.Cancel()
		retryAfter := int(delay.Seconds()) + 1
		if retryAfter < 1 {
			retryAfter = 1
		}
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
		config.WriteError(w, r, http.StatusTooManyRequests, "RATE_LIMITED", "Too many requests.")
		return false
	}
	return true
}

func (m *MiddlewareChain) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		m.mu.Lock()
		for key, client := range m.clients {
			if time.Since(client.lastSeen) > m.cfg.RateLimitClientTTL {
				delete(m.clients, key)
			}
		}
		m.mu.Unlock()
	}
}

func (m *MiddlewareChain) setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: https:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'")
}

func (m *MiddlewareChain) applyCORS(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if requestOrigin(r) == origin {
		return true
	}
	for _, allowed := range m.cfg.AllowedOrigins {
		if origin == allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Authorization, X-API-Client, X-API-Key")
			return true
		}
	}
	return false
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
		scheme = strings.Split(forwarded, ",")[0]
	}
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	if host == "" {
		return ""
	}
	return (&url.URL{Scheme: strings.TrimSpace(scheme), Host: host}).String()
}

func (m *MiddlewareChain) isTrustedProxy(ip net.IP) bool {
	for _, cidr := range m.trustedProxies {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

func parseTrustedProxies(raw string) []*net.IPNet {
	var cidrs []*net.IPNet
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if ip := net.ParseIP(part); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			cidrs = append(cidrs, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, cidr, err := net.ParseCIDR(part)
		if err == nil {
			cidrs = append(cidrs, cidr)
		}
	}
	return cidrs
}

func requestID(r *http.Request) string {
	if existing := strings.TrimSpace(r.Header.Get("X-Request-Id")); existing != "" && len(existing) <= 128 {
		return existing
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
