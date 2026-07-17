package telemetry

import (
	"strings"
	"testing"
	"time"
)

func TestSanitizeQueryRedactsSensitiveParams(t *testing.T) {
	sensitive := []string{
		"key", "api_key", "apikey", "token", "authorization", "password",
		"secret", "session", "code", "signature", "stripe_id", "email",
		"client_secret", "session_token",
	}
	for _, name := range sensitive {
		out := SanitizeQuery(name + "=super-secret-value&search=radio")
		if strings.Contains(out, "super-secret-value") {
			t.Errorf("param %q leaked: %s", name, out)
		}
		if !strings.Contains(out, "search=radio") {
			t.Errorf("param %q: benign value lost: %s", name, out)
		}
	}
}

func TestSanitizeQueryUnparseable(t *testing.T) {
	if out := SanitizeQuery("a=%zz;bad"); out != "[unparseable]" && strings.Contains(out, "zz;bad") {
		t.Errorf("unparseable query stored raw: %q", out)
	}
}

func TestSanitizePathRedactsVerifyTokens(t *testing.T) {
	out := SanitizePath("/account/verify/abcdef-token-value")
	if strings.Contains(out, "abcdef-token-value") {
		t.Errorf("verify token leaked: %s", out)
	}
}

func TestNetworkIDRotatesAndIsStableWithinWindow(t *testing.T) {
	anonymizer := NewAnonymizer("secret", "monthly")
	january := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	february := time.Date(2026, 2, 10, 0, 0, 0, 0, time.UTC)

	a := anonymizer.NetworkID("203.0.113.10", january)
	b := anonymizer.NetworkID("203.0.113.99", january) // same /24
	c := anonymizer.NetworkID("203.0.113.10", february)
	d := anonymizer.NetworkID("198.51.100.10", january)

	if a == "" || len(a) != 24 {
		t.Fatalf("unexpected id %q", a)
	}
	if a != b {
		t.Error("same /24 network should share an id within a window")
	}
	if a == c {
		t.Error("id must rotate between windows")
	}
	if a == d {
		t.Error("different networks must not collide")
	}
	if strings.Contains(a, "203") {
		t.Error("id must not embed the address")
	}
}

func TestNetworkIDWithoutSecretIsEmpty(t *testing.T) {
	if id := NewAnonymizer("", "monthly").NetworkID("203.0.113.10", time.Now()); id != "" {
		t.Errorf("expected empty id without secret, got %q", id)
	}
}

func TestScrubIPs(t *testing.T) {
	in := `{"bucket":"anonymous:203.0.113.10","note":"2001:db8::1 dialed in","version":"1.2.3"}`
	out := ScrubIPs(in)
	if strings.Contains(out, "203.0.113.10") || strings.Contains(out, "2001:db8::1") {
		t.Errorf("IPs survived scrub: %s", out)
	}
	if !strings.Contains(out, `"version":"1.2.3"`) {
		t.Errorf("non-IP content damaged: %s", out)
	}
	// A full IPv6 address preceded by a colon separator (rate-limit buckets).
	bucket := `{"bucket":"anonymous:2604:3d08:617e:9500:8548:ffba:eb03:cfe6"}`
	if scrubbed := ScrubIPs(bucket); strings.Contains(scrubbed, "3d08") {
		t.Errorf("IPv6 bucket survived scrub: %s", scrubbed)
	}
	// Timestamps must survive.
	if scrubbed := ScrubIPs("at 10:00:05 the job ran"); !strings.Contains(scrubbed, "10:00:05") {
		t.Errorf("timestamp over-redacted: %s", scrubbed)
	}
}

func TestNormalizeSearchTermDropsEmails(t *testing.T) {
	if term := NormalizeSearchTerm("someone@example.com"); term != "" {
		t.Errorf("email stored as search term: %q", term)
	}
	if term := NormalizeSearchTerm("  RadIo   Backpacks "); term != "radio backpacks" {
		t.Errorf("normalization failed: %q", term)
	}
}

func TestIsRedactedLogField(t *testing.T) {
	for _, field := range []string{"clientIP", "clientip", "password", "authorization", "token"} {
		if !IsRedactedLogField(field) {
			t.Errorf("field %q must be redacted", field)
		}
	}
	if IsRedactedLogField("requestId") {
		t.Error("requestId must not be redacted")
	}
}
