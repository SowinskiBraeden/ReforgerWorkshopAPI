package api

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

type RateIdentity struct {
	Bucket string
	Limit  int
	Burst  int
}

type IdentityResolver func(r *http.Request, clientIP string) RateIdentity

type MiddlewareConfig struct {
	Config           config.Config
	IdentityResolver IdentityResolver
}

type MiddlewareChain struct {
	cfg              config.Config
	identityResolver IdentityResolver
	trustedProxies   []*net.IPNet
	clients          map[string]*rateClient
	mu               sync.Mutex
}

type rateClient struct {
	limiter  *rate.Limiter
	lastSeen time.Time
	limit    int
	burst    int
}

func NewMiddleware(cfg config.Config) *MiddlewareChain {
	m := &MiddlewareChain{
		cfg:            cfg,
		trustedProxies: parseTrustedProxies(cfg.TrustedProxyCIDRs),
		clients:        make(map[string]*rateClient),
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

// Middleware is kept for older tests/imports. New code should use NewMiddleware.
func Middleware(next http.Handler) http.Handler {
	return NewMiddleware(*config.New()).Wrap(next)
}

func (m *MiddlewareChain) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := requestID(r)
		r.Header.Set("X-Request-Id", requestID)

		m.setSecurityHeaders(w)
		if !m.applyCORS(w, r) {
			config.WriteError(w, r, http.StatusForbidden, "CORS_FORBIDDEN", "Origin is not allowed.")
			return
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if len(r.URL.RawQuery) > m.cfg.MaxQueryLength {
			config.WriteError(w, r, http.StatusRequestURITooLong, "QUERY_TOO_LONG", "Query string is too long.")
			return
		}
		if m.cfg.MaxBodyBytes > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, m.cfg.MaxBodyBytes)
		}

		clientIP := m.ClientIP(r)
		identity := m.identityResolver(r, clientIP)
		if identity.Limit <= 0 {
			identity.Limit = m.cfg.AnonymousRateLimitPerMinute
		}
		if identity.Burst <= 0 {
			identity.Burst = m.cfg.AnonymousRateBurst
		}
		if !m.allow(w, r, identity) {
			zap.S().Infow("rate limit rejected", "requestId", requestID, "clientIP", clientIP, "path", r.URL.Path, "bucket", identity.Bucket)
			return
		}

		next.ServeHTTP(w, r)
		zap.S().Infow("request completed", "requestId", requestID, "clientIP", clientIP, "method", r.Method, "path", r.URL.Path, "latencyMs", time.Since(start).Milliseconds())
	})
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
	for _, allowed := range m.cfg.AllowedOrigins {
		if origin == allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			return true
		}
	}
	return false
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
