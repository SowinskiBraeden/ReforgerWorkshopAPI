package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
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
			assertContains(t, body, `meta name="keywords" content="`+pageKeywords(page)+`"`)
			assertContains(t, body, `meta name="date" content="`+pageLastMod(page)+`"`)
			assertContains(t, body, `rel="canonical" href="https://reforgermods.test`+page.Path+`"`)
			assertContains(t, body, "<h1>"+page.H1+"</h1>")
			assertContains(t, body, `<meta name="robots" content="index, follow, max-image-preview:large">`)
			assertContains(t, body, `<meta property="og:updated_time" content="`+pageLastMod(page)+`">`)
			assertContains(t, body, `"@type":"WebPage"`)
			assertContains(t, body, `href="/static/index.css?v=`+staticAssetVersion()+`"`)
			if page.FullWidth {
				assertContains(t, body, `tool-content-full`)
				if strings.Contains(body, `class="docs-sidebar"`) {
					t.Fatalf("full-width page %s should not render the docs sidebar", page.Path)
				}
			} else {
				assertContains(t, body, `class="docs-sidebar"`)
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
		assertContains(t, sitemapRec.Body.String(), "<lastmod>"+pageLastMod(page)+"</lastmod>")
	}
}

func TestAPIPreflightAllowsSiteToolClientHeader(t *testing.T) {
	cfg := testSiteConfig()
	cfg.AllowedOrigins = []string{"https://reforgermods.test"}
	app := &App{Config: cfg}
	app.Initialize()
	t.Cleanup(func() {
		_ = app.Shutdown(context.Background())
	})

	req := httptest.NewRequest(http.MethodOptions, "/v1/mods/1", nil)
	req.RemoteAddr = "203.0.113.40:1234"
	req.Header.Set("Origin", "https://reforgermods.test")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "x-api-client")
	rec := httptest.NewRecorder()

	app.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://reforgermods.test" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want https://reforgermods.test", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, "OPTIONS") || !strings.Contains(got, "GET") {
		t.Fatalf("Access-Control-Allow-Methods = %q, want GET and OPTIONS", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(strings.ToLower(got), "x-api-client") {
		t.Fatalf("Access-Control-Allow-Headers = %q, want X-API-Client", got)
	}
}

func TestPublicPageStaticImagesExist(t *testing.T) {
	images := map[string]string{"default": defaultPageImagePath}
	for _, page := range publicPages {
		if page.Image != "" {
			images[page.Path] = page.Image
		}
	}
	for pagePath, image := range images {
		t.Run(pagePath, func(t *testing.T) {
			if strings.HasPrefix(image, "http://") || strings.HasPrefix(image, "https://") {
				return
			}
			if !strings.HasPrefix(image, "/static/") {
				t.Fatalf("image %q should be a /static/ path or absolute URL", image)
			}
			if !staticAssetExists(image) {
				t.Fatalf("image %q is not deployable", image)
			}
		})
	}
}

func staticAssetExists(path string) bool {
	for _, prefix := range []string{".", "../.."} {
		if _, err := os.Stat(prefix + path); err == nil {
			return true
		}
	}
	return false
}

func TestToolPagesHaveSEOEnhancements(t *testing.T) {
	app := testSiteApp(t)
	for _, page := range toolPages {
		t.Run(page.Path, func(t *testing.T) {
			if len(page.Keywords) < 3 {
				t.Fatalf("%s has %d keywords, want at least 3", page.Path, len(page.Keywords))
			}

			req := httptest.NewRequest(http.MethodGet, page.Path, nil)
			req.Host = "reforgermods.test"
			rec := httptest.NewRecorder()
			app.Router.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			body := rec.Body.String()
			assertContains(t, body, `meta name="keywords" content="`+pageKeywords(page)+`"`)
			assertContains(t, body, `"@type":"SearchAction"`)
			assertContains(t, body, `"@type":"FAQPage"`)
			if page.ToolName != "" {
				assertContains(t, body, `"@id":"https://reforgermods.test`+page.Path+`#tool"`)
				assertContains(t, body, `"isAccessibleForFree":true`)
				assertContains(t, body, `"applicationCategory":"UtilitiesApplication"`)
			}
		})
	}
}

func TestModDetailPageHasIndexableMetadata(t *testing.T) {
	app := testSiteApp(t)

	req := httptest.NewRequest(http.MethodGet, "/arma-reforger-mods/5965550F24A0C152/", nil)
	req.Host = "reforgermods.test"
	rec := httptest.NewRecorder()
	app.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	assertContains(t, body, `<title>Arma Reforger Mod 5965550F24A0C152 | Reforger Mods API</title>`)
	assertContains(t, body, `rel="canonical" href="https://reforgermods.test/arma-reforger-mods/5965550F24A0C152/"`)
	assertContains(t, body, `meta name="keywords" content="Arma Reforger mod 5965550F24A0C152`)
	assertContains(t, body, `"@type":"WebPage"`)
	assertContains(t, body, `"@type":"BreadcrumbList"`)
	assertContains(t, body, `"name":"Arma Reforger Mods"`)
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
		"/?page=documentation/mods": "/arma-reforger-mods-api/#mod-object",
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

func TestDocsRoutesRedirectToAPIReference(t *testing.T) {
	app := testSiteApp(t)

	cases := map[string]string{
		"/docs/":                "/arma-reforger-mods-api/",
		"/docs":                 "/arma-reforger-mods-api/",
		"/docs/mod-structures/": "/arma-reforger-mods-api/#mod-object",
		"/docs/mods/":           "/arma-reforger-mods-api/#mod-object",
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
