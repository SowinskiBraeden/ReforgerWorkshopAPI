package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
)

func testSiteConfig() config.Config {
	cfg := testHandlerConfig()
	cfg.FullURL = "https://api.reforgermods.test"
	cfg.APIBaseURL = "https://api.reforgermods.test"
	cfg.PublicBaseURL = "https://reforgermods.test"
	cfg.InternalMetricsEnabled = false
	return cfg
}

func testSiteApp(t *testing.T) *App {
	t.Helper()
	app := &App{Config: testSiteConfig()}
	app.Initialize()
	t.Cleanup(func() {
		_ = app.Shutdown(context.Background())
	})
	return app
}

func TestPublicPagesRenderIndexableMetadata(t *testing.T) {
	app := testSiteApp(t)
	for _, page := range publicPages {
		t.Run(page.Path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, page.Path, nil)
			req.Host = "reforgermods.test"
			rec := httptest.NewRecorder()

			app.Router.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			body := rec.Body.String()
			assertContains(t, body, "<title>"+page.Title+"</title>")
			assertContains(t, body, `meta name="description" content="`+page.Description+`"`)
			assertContains(t, body, `rel="canonical" href="https://reforgermods.test`+page.Path+`"`)
			assertContains(t, body, "<h1>"+page.H1+"</h1>")
			assertContains(t, body, `<meta name="robots" content="index, follow, max-image-preview:large">`)
			assertContains(t, body, `href="/static/index.css"`)
			assertContains(t, body, `class="docs-sidebar"`)
			if page.MarkdownPage != "" {
				assertContains(t, body, `data-default-page="`+page.MarkdownPage+`"`)
			}
		})
	}
}

func TestRobotsAndSitemapUseConfiguredPublicOrigin(t *testing.T) {
	app := testSiteApp(t)

	robotsReq := httptest.NewRequest(http.MethodGet, "/robots.txt", nil)
	robotsRec := httptest.NewRecorder()
	app.Router.ServeHTTP(robotsRec, robotsReq)
	if robotsRec.Code != http.StatusOK {
		t.Fatalf("robots status = %d, want 200", robotsRec.Code)
	}
	assertContains(t, robotsRec.Body.String(), "Sitemap: https://reforgermods.test/sitemap.xml")

	sitemapReq := httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil)
	sitemapRec := httptest.NewRecorder()
	app.Router.ServeHTTP(sitemapRec, sitemapReq)
	if sitemapRec.Code != http.StatusOK {
		t.Fatalf("sitemap status = %d, want 200", sitemapRec.Code)
	}
	if !strings.HasPrefix(sitemapRec.Body.String(), `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Fatalf("sitemap XML declaration was not rendered correctly: %q", sitemapRec.Body.String()[:20])
	}
	for _, page := range publicPages {
		assertContains(t, sitemapRec.Body.String(), "<loc>https://reforgermods.test"+page.Path+"</loc>")
	}
}

func TestAPIRoutesRemainJSONAndNoIndex(t *testing.T) {
	app := testSiteApp(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	rec := httptest.NewRecorder()
	app.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	assertContains(t, rec.Header().Get("Content-Type"), "application/json")
	assertContains(t, rec.Body.String(), `"alive":true`)
	if got := rec.Header().Get("X-Robots-Tag"); got != "noindex, nofollow, noarchive" {
		t.Fatalf("X-Robots-Tag = %q, want API noindex", got)
	}
}

func TestCanonicalHostRedirectsOnlyPublicPages(t *testing.T) {
	cfg := testSiteConfig()
	cfg.PublicCanonicalRedirects = true
	app := &App{Config: cfg}
	app.Initialize()
	t.Cleanup(func() {
		_ = app.Shutdown(context.Background())
	})

	publicReq := httptest.NewRequest(http.MethodGet, "https://api.reforgermods.test/arma-reforger-mods-api/", nil)
	publicReq.Host = "api.reforgermods.test"
	publicRec := httptest.NewRecorder()
	app.Router.ServeHTTP(publicRec, publicReq)
	if publicRec.Code != http.StatusPermanentRedirect {
		t.Fatalf("public status = %d, want 308", publicRec.Code)
	}
	if got := publicRec.Header().Get("Location"); got != "https://reforgermods.test/arma-reforger-mods-api/" {
		t.Fatalf("Location = %q, want canonical public URL", got)
	}

	apiReq := httptest.NewRequest(http.MethodGet, "https://reforgermods.test/v1/health", nil)
	apiReq.Host = "reforgermods.test"
	apiReq.RemoteAddr = "203.0.113.10:1234"
	apiRec := httptest.NewRecorder()
	app.Router.ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusOK {
		t.Fatalf("API status = %d, want 200 without canonical redirect", apiRec.Code)
	}
	if apiRec.Header().Get("Location") != "" {
		t.Fatalf("API received unexpected redirect Location %q", apiRec.Header().Get("Location"))
	}
}

func TestLegacyDocumentationQueryRedirects(t *testing.T) {
	app := testSiteApp(t)

	cases := map[string]string{
		"/?page=documentation/api":  "/arma-reforger-mods-api/",
		"/?page=documentation/mods": "/docs/mod-structures/",
	}
	for path, want := range cases {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		app.Router.ServeHTTP(rec, req)

		if rec.Code != http.StatusMovedPermanently {
			t.Fatalf("%s status = %d, want 301", path, rec.Code)
		}
		if got := rec.Header().Get("Location"); got != want {
			t.Fatalf("%s Location = %q, want %q", path, got, want)
		}
	}
}

func TestModStructuresRouteLoadsModsMarkdown(t *testing.T) {
	app := testSiteApp(t)

	req := httptest.NewRequest(http.MethodGet, "/docs/mod-structures/", nil)
	rec := httptest.NewRecorder()
	app.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	assertContains(t, body, `data-default-page="documentation/mods"`)
	assertContains(t, body, `<h1>Mod Structures</h1>`)
	assertContains(t, body, `rel="canonical" href="https://reforgermods.test/docs/mod-structures/"`)
}

func TestNotFoundIsNoIndex(t *testing.T) {
	app := testSiteApp(t)

	req := httptest.NewRequest(http.MethodGet, "/missing-page", nil)
	rec := httptest.NewRecorder()
	app.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := rec.Header().Get("X-Robots-Tag"); got != "noindex, nofollow" {
		t.Fatalf("X-Robots-Tag = %q, want noindex for missing page", got)
	}
}

func assertContains(t *testing.T, got string, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected %q to contain %q", got, want)
	}
}
