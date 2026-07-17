package telemetry

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClassifySource(t *testing.T) {
	cases := []struct {
		name string
		in   ClassifyInput
		want string
	}{
		{"health route", ClassifyInput{IsHealthRoute: true, IsAPIRoute: true}, SourceHealth},
		{"admin route", ClassifyInput{IsAdminRoute: true}, SourceAdmin},
		{"internal key", ClassifyInput{AuthType: AuthInternalKey, IsAPIRoute: true}, SourceInternalService},
		{"signed internal header", ClassifyInput{InternalHeader: true, IsAPIRoute: true}, SourceInternalService},
		{"api key", ClassifyInput{AuthType: AuthAPIKey, IsAPIRoute: true}, SourceExternalAPI},
		{"same-origin web", ClassifyInput{SameOrigin: true, IsAPIRoute: true, UserAgent: "Mozilla/5.0 Chrome/120"}, SourceInternalWeb},
		{"googlebot page", ClassifyInput{UserAgent: "Mozilla/5.0 (compatible; Googlebot/2.1)"}, SourceCrawler},
		{"gptbot page", ClassifyInput{UserAgent: "GPTBot/1.0"}, SourceAICrawler},
		{"amazonbot api", ClassifyInput{UserAgent: "Mozilla/5.0 compatible; Amazonbot/0.1", IsAPIRoute: true}, SourceAICrawler},
		{"uptimerobot", ClassifyInput{UserAgent: "UptimeRobot/2.0", IsAPIRoute: true}, SourceMonitoring},
		{"curl api", ClassifyInput{UserAgent: "curl/8.0.1", IsAPIRoute: true}, SourceAnonymousAPI},
		{"browser api", ClassifyInput{UserAgent: "Mozilla/5.0 Chrome/120 Safari/537", IsAPIRoute: true}, SourceBrowser},
		{"browser page", ClassifyInput{UserAgent: "Mozilla/5.0 Chrome/120 Safari/537"}, SourceWebsite},
		{"server-to-server anon", ClassifyInput{UserAgent: "MyIntegration/2.1", IsAPIRoute: true}, SourceAnonymousAPI},
		{"internal ip api", ClassifyInput{IsInternalIP: true, IsAPIRoute: true, UserAgent: "node"}, SourceInternalService},
	}
	for _, tc := range cases {
		if got := ClassifySource(tc.in); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestExternalCallersCannotSpoofInternalHeader(t *testing.T) {
	secret := "internal-secret"
	r := httptest.NewRequest(http.MethodGet, "/v1/mods", nil)
	now := time.Now()

	r.Header.Set("X-Internal-Auth", "20260101T000000Z.deadbeef")
	if VerifyInternalHeader(r, secret, now) {
		t.Fatal("forged header accepted")
	}
	r.Header.Set("X-Internal-Auth", SignInternalHeader("wrong-secret", now))
	if VerifyInternalHeader(r, secret, now) {
		t.Fatal("header signed with wrong secret accepted")
	}
	r.Header.Set("X-Internal-Auth", SignInternalHeader(secret, now.Add(-time.Hour)))
	if VerifyInternalHeader(r, secret, now) {
		t.Fatal("expired header accepted")
	}
	r.Header.Set("X-Internal-Auth", SignInternalHeader(secret, now))
	if !VerifyInternalHeader(r, secret, now) {
		t.Fatal("valid header rejected")
	}
	if VerifyInternalHeader(r, "", now) {
		t.Fatal("header accepted with no configured secret")
	}
}

func TestCountsAsActivityExclusions(t *testing.T) {
	if CountsAsActivity(SourceHealth, 200, false) {
		t.Error("health checks must not count as activity")
	}
	if CountsAsActivity(SourceCrawler, 200, false) {
		t.Error("crawlers must not count as activity")
	}
	if CountsAsActivity(SourceMonitoring, 200, false) {
		t.Error("monitors must not count as activity")
	}
	if CountsAsActivity(SourceExternalAPI, 401, false) {
		t.Error("rejected auth must not count as activity")
	}
	if CountsAsActivity(SourceExternalAPI, 200, true) {
		t.Error("rate-limited requests must not count as activity")
	}
	if !CountsAsActivity(SourceExternalAPI, 200, false) {
		t.Error("successful API usage must count")
	}
	if !CountsAsActivity(SourceWebsite, 404, false) {
		t.Error("4xx (non-auth) browsing still counts as activity")
	}
}

func TestClientNameFromUserAgent(t *testing.T) {
	cases := map[string]string{
		"Mozilla/5.0 (Windows) Chrome/120 Safari/537.36":   "browser",
		"Mozilla/5.0 compatible; Amazonbot/0.1 Chrome/119": "amazonbot",
		"curl/8.0.1":          "curl/8.0.1",
		"ReforgerPanel/2.3.1": "ReforgerPanel/2.3.1",
		"":                    "unknown",
	}
	for ua, want := range cases {
		if got := ClientNameFromUserAgent(ua); got != want {
			t.Errorf("ClientNameFromUserAgent(%q) = %q, want %q", ua, got, want)
		}
	}
}
