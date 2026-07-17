package telemetry

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Anonymizer converts client IP addresses into privacy-safe derived fields.
// The IP itself is used only in memory during request processing and is never
// stored. The identifier is HMAC-SHA256(secret, rotationWindow || coarseNet)
// so it cannot be reversed without the server-side secret, and it rotates by
// a configurable period so long-term tracking of a network is impossible by
// default.
type Anonymizer struct {
	secret   []byte
	rotation string // "daily" | "weekly" | "monthly" | "none"
}

func NewAnonymizer(secret string, rotation string) *Anonymizer {
	rotation = strings.ToLower(strings.TrimSpace(rotation))
	switch rotation {
	case "daily", "weekly", "monthly", "none":
	default:
		rotation = "monthly"
	}
	return &Anonymizer{secret: []byte(strings.TrimSpace(secret)), rotation: rotation}
}

// NetworkID returns the anonymous network identifier for an IP at a moment in
// time, or "" when the IP is unparseable or no secret is configured.
func (a *Anonymizer) NetworkID(clientIP string, at time.Time) string {
	if a == nil || len(a.secret) == 0 {
		return ""
	}
	network := coarseNetwork(clientIP)
	if network == "" {
		return ""
	}
	mac := hmac.New(sha256.New, a.secret)
	_, _ = mac.Write([]byte(a.rotationWindow(at)))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(network))
	return hex.EncodeToString(mac.Sum(nil))[:24]
}

func (a *Anonymizer) rotationWindow(at time.Time) string {
	at = at.UTC()
	switch a.rotation {
	case "none":
		return "static"
	case "daily":
		return at.Format("2006-01-02")
	case "weekly":
		year, week := at.ISOWeek()
		return fmt.Sprintf("%04d-W%02d", year, week)
	default:
		return at.Format("2006-01")
	}
}

// coarseNetwork maps an IP to its /24 (IPv4) or /48 (IPv6) network so the
// identifier groups a network rather than a single machine.
func coarseNetwork(clientIP string) string {
	ip := net.ParseIP(strings.TrimSpace(clientIP))
	if ip == nil {
		return ""
	}
	if ipv4 := ip.To4(); ipv4 != nil {
		return (&net.IPNet{IP: ipv4.Mask(net.CIDRMask(24, 32)), Mask: net.CIDRMask(24, 32)}).String()
	}
	ipv6 := ip.To16()
	if ipv6 == nil {
		return ""
	}
	return (&net.IPNet{IP: ipv6.Mask(net.CIDRMask(48, 128)), Mask: net.CIDRMask(48, 128)}).String()
}

// redactedQueryParams are matched as substrings of the lower-cased parameter
// name; their values are always replaced before storage.
var redactedQueryParams = []string{
	"key", "api_key", "apikey", "token", "authorization", "auth",
	"password", "secret", "session", "code", "signature", "sign",
	"stripe", "email",
}

const redactedValue = "[redacted]"
const maxStoredQueryLen = 512
const maxStoredValueLen = 120

// SanitizeQuery redacts sensitive parameter values and caps lengths. The
// parameter names are preserved so debugging retains the request shape.
func SanitizeQuery(rawQuery string) string {
	rawQuery = strings.TrimSpace(rawQuery)
	if rawQuery == "" {
		return ""
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		// Unparseable query strings may embed anything; do not store them.
		return "[unparseable]"
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	// Deterministic order keeps identical queries identical after sanitizing.
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		for _, value := range values[name] {
			if b.Len() > 0 {
				b.WriteByte('&')
			}
			if isSensitiveParam(name) {
				value = redactedValue
			} else if len(value) > maxStoredValueLen {
				value = value[:maxStoredValueLen] + "..."
			}
			b.WriteString(url.QueryEscape(name))
			b.WriteByte('=')
			b.WriteString(url.QueryEscape(value))
			if b.Len() >= maxStoredQueryLen {
				return b.String()[:maxStoredQueryLen]
			}
		}
	}
	return b.String()
}

func isSensitiveParam(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, marker := range redactedQueryParams {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

// pathRedactionRules replace sensitive path segments for specific route
// prefixes before storage. The route template remains untouched.
var pathRedactionRules = []struct {
	prefix string
	keep   int // path segments to keep before redacting the rest
}{
	{prefix: "/account/verify", keep: 2},
	{prefix: "/account/api-keys/", keep: 2},
	{prefix: "/internal/admin/users/", keep: 3},
}

// SanitizePath applies route-specific redaction rules and caps the length.
func SanitizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	for _, rule := range pathRedactionRules {
		if !strings.HasPrefix(path, rule.prefix) {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
		if len(parts) > rule.keep {
			parts = append(parts[:rule.keep], redactedValue)
			return "/" + strings.Join(parts, "/")
		}
	}
	if len(path) > 300 {
		path = path[:300]
	}
	return sanitizeControlChars(path)
}

// redactedLogFields are dropped entirely when structured log lines are stored,
// because they may contain raw IPs or credentials.
var redactedLogFields = map[string]struct{}{
	"clientip": {}, "client_ip": {}, "ip": {}, "remoteaddr": {},
	"authorization": {}, "cookie": {}, "x-api-key": {}, "apikey": {},
	"password": {}, "secret": {}, "token": {}, "signature": {},
}

// IsRedactedLogField reports whether a structured log field must not be stored.
func IsRedactedLogField(name string) bool {
	_, ok := redactedLogFields[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// ipPattern matches IPv4 addresses and IPv6-shaped literals (including
// compressed :: forms) embedded in text. Matches may carry stray leading or
// trailing colons from surrounding text (e.g. "bucket:2604:…"); candidates
// are colon-trimmed and validated with net.ParseIP before redaction, so
// look-alikes such as timestamps survive.
var ipPattern = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b|[0-9a-fA-F]{0,4}(?::[0-9a-fA-F]{0,4}){2,8}`)

// ScrubIPs replaces anything that looks like an IP address inside free-form
// text. It is the catch-all guard for log fields whose values may embed
// addresses (e.g. historical rate-limit buckets); structured fields should be
// sanitized at the source, this ensures nothing slips through into storage.
func ScrubIPs(value string) string {
	if value == "" {
		return ""
	}
	return ipPattern.ReplaceAllStringFunc(value, func(match string) string {
		if net.ParseIP(strings.Trim(match, ":")) == nil {
			return match
		}
		return "[ip-redacted]"
	})
}

func sanitizeControlChars(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r < 32 || r == 127 {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// SanitizeText trims control characters and caps a value for storage.
func SanitizeText(value string, maxLen int) string {
	value = sanitizeControlChars(strings.TrimSpace(value))
	if maxLen > 0 && len(value) > maxLen {
		value = value[:maxLen]
	}
	return value
}

// NormalizeSearchTerm lower-cases, collapses whitespace, strips control
// characters and caps the length of a search term for product analytics.
// Terms that look like emails or contain redaction markers are dropped.
func NormalizeSearchTerm(raw string) string {
	term := strings.ToLower(strings.Join(strings.Fields(raw), " "))
	term = sanitizeControlChars(term)
	if term == "" || strings.Contains(term, "@") {
		return ""
	}
	if len(term) > 80 {
		term = term[:80]
	}
	return term
}
