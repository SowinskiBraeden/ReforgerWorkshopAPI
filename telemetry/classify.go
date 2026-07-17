package telemetry

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

// crawler / monitor user-agent markers. Matched case-insensitively as
// substrings. AI crawlers are matched before search crawlers so overlapping
// tokens classify as ai-crawler.
var aiCrawlerMarkers = []string{
	"gptbot", "chatgpt-user", "oai-searchbot", "claudebot", "claude-web",
	"anthropic-ai", "perplexitybot", "amazonbot", "ccbot", "bytespider",
	"google-extended", "cohere-ai", "meta-externalagent", "diffbot",
	"timpibot", "omgili", "youbot", "ai2bot", "mistralai",
}

var searchCrawlerMarkers = []string{
	"googlebot", "bingbot", "slurp", "duckduckbot", "baiduspider",
	"yandexbot", "sogou", "exabot", "applebot", "seznambot", "petalbot",
	"qwantbot", "ahrefsbot", "semrushbot", "mj12bot", "dotbot", "rogerbot",
	"screaming frog",
}

var monitorMarkers = []string{
	"uptimerobot", "pingdom", "statuscake", "site24x7", "checkly",
	"betteruptime", "better stack", "freshping", "hetrixtools", "updown.io",
	"gatus", "uptime-kuma",
}

var cliMarkers = []string{
	"curl/", "wget/", "httpie/", "powershell", "insomnia", "postmanruntime",
}

var browserMarkers = []string{
	"mozilla/", "chrome/", "safari/", "firefox/", "edg/", "opera/",
}

var genericBotMarkers = []string{
	"bot", "spider", "crawl", "headless", "phantomjs", "scrapy",
	"python-requests", "python-urllib", "go-http-client", "java/", "libwww",
}

// ClassifyClientKind derives the coarse client kind from a user agent.
func ClassifyClientKind(userAgent string) string {
	ua := strings.ToLower(strings.TrimSpace(userAgent))
	switch {
	case ua == "":
		return ClientKindUnknown
	case matchesAny(ua, monitorMarkers):
		return ClientKindMonitor
	case matchesAny(ua, aiCrawlerMarkers), matchesAny(ua, searchCrawlerMarkers):
		return ClientKindCrawler
	case matchesAny(ua, cliMarkers):
		return ClientKindCLI
	case matchesAny(ua, genericBotMarkers):
		return ClientKindServer
	case matchesAny(ua, browserMarkers):
		return ClientKindBrowser
	default:
		return ClientKindServer
	}
}

// ClassifyInput carries everything the source classifier may use.
type ClassifyInput struct {
	RouteTemplate  string
	Path           string
	UserAgent      string
	AuthType       string
	IsInternalIP   bool // loopback or configured internal CIDR
	InternalHeader bool // valid signed X-Internal-Auth header
	SameOrigin     bool // request originated from the site's own pages
	IsAPIRoute     bool
	IsAdminRoute   bool
	IsHealthRoute  bool
}

// ClassifySource assigns exactly one source category to a request. Trusted
// signals (routes, credentials, signed headers) win over user-agent guessing.
func ClassifySource(in ClassifyInput) string {
	ua := strings.ToLower(strings.TrimSpace(in.UserAgent))
	switch {
	case in.IsAdminRoute:
		return SourceAdmin
	case in.IsHealthRoute:
		return SourceHealth
	case in.AuthType == AuthInternalKey, in.InternalHeader:
		return SourceInternalService
	case matchesAny(ua, monitorMarkers):
		return SourceMonitoring
	case matchesAny(ua, aiCrawlerMarkers):
		return SourceAICrawler
	case matchesAny(ua, searchCrawlerMarkers):
		return SourceCrawler
	case in.AuthType == AuthAPIKey:
		return SourceExternalAPI
	case in.IsAPIRoute && in.SameOrigin:
		return SourceInternalWeb
	case in.IsAPIRoute && in.IsInternalIP:
		return SourceInternalService
	case !in.IsAPIRoute:
		if matchesAny(ua, genericBotMarkers) {
			return SourceBot
		}
		return SourceWebsite
	case matchesAny(ua, cliMarkers):
		return SourceAnonymousAPI
	case matchesAny(ua, genericBotMarkers):
		return SourceBot
	case matchesAny(ua, browserMarkers):
		return SourceBrowser
	case ua == "":
		return SourceUnknown
	default:
		return SourceAnonymousAPI
	}
}

// HumanSources are counted as real product usage; the rest is machine noise
// excluded from active-entity and retention metrics.
var activitySources = map[string]struct{}{
	SourceWebsite:         {},
	SourceInternalWeb:     {},
	SourceExternalAPI:     {},
	SourceAnonymousAPI:    {},
	SourceBrowser:         {},
	SourceInternalService: {},
}

// CountsAsActivity reports whether a request event should count toward
// active users/clients/networks and retention ("active" definition in
// docs/observability.md).
func CountsAsActivity(source string, status int, rateLimited bool) bool {
	if rateLimited || status >= 500 || status == http.StatusTooManyRequests || status == http.StatusUnauthorized || status == http.StatusForbidden {
		return false
	}
	_, ok := activitySources[source]
	return ok
}

// hostingMarkers classify a network organization as a hosting provider when
// ASN/organization data is available from the edge.
var hostingMarkers = []string{
	"amazon", "aws", "google cloud", "gcp", "microsoft", "azure", "hetzner",
	"ovh", "digitalocean", "linode", "akamai", "vultr", "contabo",
	"scaleway", "oracle", "alibaba", "tencent", "cloudflare", "fastly",
	"leaseweb", "ionos", "netcup", "hostinger", "datacamp", "m247",
	"choopa", "colocrossing", "hosting", "datacenter", "data center", "vps",
	"server", "cloud",
}

// ClassifyHosting returns "hosting", "residential" or "unknown" for a network
// organization name. Without network data the answer is always "unknown".
func ClassifyHosting(networkName string, asn string) string {
	name := strings.ToLower(strings.TrimSpace(networkName))
	if name == "" && strings.TrimSpace(asn) == "" {
		return "unknown"
	}
	if matchesAny(name, hostingMarkers) {
		return "hosting"
	}
	if name == "" {
		return "unknown"
	}
	return "residential"
}

// ClientNameFromUserAgent extracts a display client name from a UA when no
// verified client identification exists. Crawlers/monitors resolve to their
// marker name, browsers collapse to "browser", other products keep their
// first product token.
func ClientNameFromUserAgent(userAgent string) string {
	ua := strings.Join(strings.Fields(strings.TrimSpace(userAgent)), " ")
	if ua == "" {
		return "unknown"
	}
	lower := strings.ToLower(ua)
	for _, markers := range [][]string{aiCrawlerMarkers, searchCrawlerMarkers, monitorMarkers} {
		for _, marker := range markers {
			if strings.Contains(lower, marker) {
				return marker
			}
		}
	}
	if matchesAny(lower, browserMarkers) && !matchesAny(lower, genericBotMarkers) {
		return "browser"
	}
	product := strings.TrimSpace(strings.SplitN(ua, " ", 2)[0])
	if idx := strings.Index(product, "("); idx > 0 {
		product = product[:idx]
	}
	return SanitizeText(product, 80)
}

// VerifyInternalHeader checks the signed service-to-service header:
// X-Internal-Auth: <unix>.<hex(hmac-sha256(secret, unix))>, valid ±5 minutes.
// External callers cannot forge it without the shared secret.
func VerifyInternalHeader(r *http.Request, secret string, now time.Time) bool {
	secret = strings.TrimSpace(secret)
	header := strings.TrimSpace(r.Header.Get("X-Internal-Auth"))
	if secret == "" || header == "" {
		return false
	}
	parts := strings.SplitN(header, ".", 2)
	if len(parts) != 2 {
		return false
	}
	ts, sig := parts[0], parts[1]
	issued, err := time.Parse("20060102T150405Z", ts)
	if err != nil {
		return false
	}
	if delta := now.UTC().Sub(issued); delta > 5*time.Minute || delta < -5*time.Minute {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(ts))
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(strings.ToLower(sig)))
}

// SignInternalHeader produces the value for X-Internal-Auth.
func SignInternalHeader(secret string, now time.Time) string {
	ts := now.UTC().Format("20060102T150405Z")
	mac := hmac.New(sha256.New, []byte(strings.TrimSpace(secret)))
	_, _ = mac.Write([]byte(ts))
	return ts + "." + hex.EncodeToString(mac.Sum(nil))
}

// EndpointGroup buckets a route template into a product-level category.
func EndpointGroup(routeTemplate string, sanitizedQuery string) string {
	route := strings.TrimSuffix(routeTemplate, "/")
	switch route {
	case "/v1/mod/{id}", "/mod/{id}":
		return "mod_detail"
	case "/v1/mods", "/mods", "/v1/mods/{page}", "/mods/{page}":
		if strings.Contains(sanitizedQuery, "search=") {
			return "search"
		}
		return "mod_list"
	case "/v1/search", "/search":
		return "search"
	case "/v1/refresh/jobs/{id}", "/refresh/jobs/{id}":
		return "refresh_job"
	case "/v1/health", "/health":
		return "health"
	case "/v1/rate-limits", "/rate-limits":
		return "rate_limits"
	}
	switch {
	case strings.HasPrefix(route, "/internal"):
		return "admin"
	case strings.HasPrefix(route, "/account") || strings.HasPrefix(route, "/billing") || strings.HasPrefix(route, "/stripe"):
		return "account_billing"
	case strings.HasPrefix(route, "/static") || route == "/robots.txt" || route == "/sitemap.xml" || route == "/ads.txt":
		return "site_assets"
	case route == "" || route == "/":
		return "site_pages"
	case strings.HasPrefix(route, "/mods/{id}"):
		return "site_pages"
	default:
		return "site_pages"
	}
}

func matchesAny(value string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}
