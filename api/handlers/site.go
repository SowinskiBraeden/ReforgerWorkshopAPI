package handlers

import (
	"encoding/json"
	htmltemplate "html/template"
	"net"
	"net/http"
	"net/url"
	"strings"
	texttemplate "text/template"
	"time"
)

type publicPage struct {
	Path         string
	Slug         string
	Title        string
	Description  string
	H1           string
	ChangeFreq   string
	Priority     string
	MarkdownPage string
	Content      htmltemplate.HTML
}

type apiEndpointDoc struct {
	Method      string
	Path        string
	Summary     string
	Parameters  string
	CachePolicy string
}

type sitePageData struct {
	Page           publicPage
	CanonicalURL   string
	PublicBaseURL  string
	APIBaseURL     string
	OfficialURL    string
	StructuredData htmltemplate.JS
	Endpoints      []apiEndpointDoc
	GeneratedAt    string
	NoIndex        bool
}

const officialWorkshopURL = "https://reforger.armaplatform.com/workshop"

var publicPages = []publicPage{
	{
		Path:        "/",
		Slug:        "home",
		Title:       "Arma Reforger Mods API | Reforger Mods API",
		Description: "Reforger Mods API is an unofficial, cached JSON data source for Arma Reforger Workshop mod metadata used by tools, bots, dashboards, and server workflows.",
		H1:          "Arma Reforger mod metadata, ready for your app.",
		ChangeFreq:  "weekly",
		Priority:    "1.0",
		Content:     homeLandingHTML,
	},
	{
		Path:        "/arma-reforger-mods/",
		Slug:        "mods",
		Title:       "Arma Reforger Mods | Searchable Workshop Metadata",
		Description: "Learn how Reforger Mods API helps players, server owners, and tool builders work with structured Arma Reforger Workshop mod metadata.",
		H1:          "Arma Reforger Mods",
		ChangeFreq:  "weekly",
		Priority:    "0.9",
		Content:     modsLandingHTML,
	},
	{
		Path:         "/arma-reforger-mods-api/",
		Slug:         "api",
		Title:        "Arma Reforger Mods API Documentation | Reforger Mods API",
		Description:  "Use the unofficial Reforger Mods API to fetch cached Arma Reforger Workshop mod lists, search results, mod details, dependencies, and refresh-job status.",
		H1:           "Arma Reforger Mods API",
		ChangeFreq:   "weekly",
		Priority:     "0.95",
		MarkdownPage: "documentation/api",
		Content:      apiFallbackHTML,
	},
	{
		Path:         "/docs/",
		Slug:         "docs",
		Title:        "Documentation | Reforger Mods API",
		Description:  "Technical documentation for Reforger Mods API endpoints, authentication, rate limits, caching, ETags, stale responses, refresh jobs, errors, and acceptable use.",
		H1:           "Reforger Mods API Documentation",
		ChangeFreq:   "monthly",
		Priority:     "0.85",
		MarkdownPage: "documentation/api",
		Content:      docsFallbackHTML,
	},
	{
		Path:         "/docs/mod-structures/",
		Slug:         "mod-structures",
		Title:        "Arma Reforger Mod Data Structures | Reforger Mods API",
		Description:  "Reference for Arma Reforger Workshop mod preview, detail, dependency, and scenario JSON structures returned by Reforger Mods API.",
		H1:           "Mod Structures",
		ChangeFreq:   "monthly",
		Priority:     "0.75",
		MarkdownPage: "documentation/mods",
		Content:      modStructuresFallbackHTML,
	},
	{
		Path:         "/docs/changelog/",
		Slug:         "changelog",
		Title:        "Changelog | Reforger Mods API",
		Description:  "Release notes for Reforger Mods API, including versioned endpoints, cache behavior, rate limiting, and reliability changes.",
		H1:           "Changelog",
		ChangeFreq:   "monthly",
		Priority:     "0.5",
		MarkdownPage: "documentation/changelog",
		Content:      changelogFallbackHTML,
	},
	{
		Path:        "/docs/methodology/",
		Slug:        "methodology",
		Title:       "Data Source and Methodology | Reforger Mods API",
		Description: "How Reforger Mods API retrieves, caches, and exposes unofficial Arma Reforger Workshop mod metadata, including freshness expectations and known limitations.",
		H1:          "Data Source and Methodology",
		ChangeFreq:  "monthly",
		Priority:    "0.7",
		Content:     methodologyHTML,
	},
	{
		Path:         "/privacy/",
		Slug:         "privacy",
		Title:        "Privacy Policy | Reforger Mods API",
		Description:  "Privacy policy for Reforger Mods API, an independent Arma Reforger Workshop metadata API.",
		H1:           "Privacy Policy",
		ChangeFreq:   "yearly",
		Priority:     "0.3",
		MarkdownPage: "privacy",
		Content:      privacyFallbackHTML,
	},
	{
		Path:         "/terms/",
		Slug:         "terms",
		Title:        "Terms of Service | Reforger Mods API",
		Description:  "Terms of service for Reforger Mods API, an independent Arma Reforger Workshop metadata API.",
		H1:           "Terms of Service",
		ChangeFreq:   "yearly",
		Priority:     "0.3",
		MarkdownPage: "terms",
		Content:      termsFallbackHTML,
	},
}

func endpointDocs() []apiEndpointDoc {
	return []apiEndpointDoc{
		{Method: "GET", Path: "/v1/health", Summary: "Process health check. It does not request Workshop data.", Parameters: "None.", CachePolicy: "no-store"},
		{Method: "GET", Path: "/v1/mods", Summary: "First page of Arma Reforger Workshop mod previews.", Parameters: "Optional search text and sort.", CachePolicy: "List cache TTL plus stale serving window."},
		{Method: "GET", Path: "/v1/mods/{page}", Summary: "A specific page of Workshop mod previews.", Parameters: "page must be a positive integer; optional search and sort.", CachePolicy: "List cache TTL plus stale serving window."},
		{Method: "GET", Path: "/v1/search?search={query}", Summary: "Convenience route for first-page search results.", Parameters: "search text; optional sort.", CachePolicy: "Same response shape and cache policy as /v1/mods."},
		{Method: "GET", Path: "/v1/mod/{mod_id}", Summary: "Detailed metadata for one Workshop mod.", Parameters: "mod_id accepts letters, numbers, underscores, and dashes.", CachePolicy: "Mod cache TTL plus stale serving window."},
		{Method: "GET", Path: "/v1/refresh/jobs/{id}", Summary: "Status for a background refresh job returned from a 202 response.", Parameters: "refresh job id from Location or response body.", CachePolicy: "no-store"},
	}
}

func staticFileHandler() http.Handler {
	files := http.StripPrefix("/static/", http.FileServer(http.Dir("./static/")))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		files.ServeHTTP(w, r)
	})
}

func (a *App) publicPageBySlug(slug string) (publicPage, bool) {
	for _, page := range publicPages {
		if page.Slug == slug {
			return page, true
		}
	}
	return publicPage{}, false
}

func (a *App) servePublicPage(slug string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, ok := a.publicPageBySlug(slug)
		if !ok {
			a.serveNotFound(w, r)
			return
		}
		if r.URL.Path != page.Path {
			http.Redirect(w, r, page.Path, http.StatusMovedPermanently)
			return
		}
		if page.Slug == "home" {
			if target := legacyPageRedirect(r.URL.Query().Get("page")); target != "" {
				http.Redirect(w, r, target, http.StatusMovedPermanently)
				return
			}
		}
		if a.redirectToCanonicalPublicHost(w, r, page.Path) {
			return
		}
		a.renderPublicPage(w, r, page, false, http.StatusOK)
	}
}

func legacyPageRedirect(page string) string {
	switch strings.Trim(strings.ToLower(page), "/") {
	case "documentation/api":
		return "/arma-reforger-mods-api/"
	case "documentation/mods":
		return "/docs/mod-structures/"
	case "documentation":
		return "/docs/"
	case "documentation/changelog":
		return "/docs/changelog/"
	case "privacy":
		return "/privacy/"
	case "terms":
		return "/terms/"
	}
	return ""
}

func (a *App) serveNotFound(w http.ResponseWriter, r *http.Request) {
	page := publicPage{
		Path:        r.URL.Path,
		Slug:        "not-found",
		Title:       "Page Not Found | Reforger Mods API",
		Description: "The requested Reforger Mods API page was not found.",
		H1:          "Page not found",
		Content:     `<h1>Page not found</h1><p>The requested page does not exist. Start from the <a href="/">homepage</a> or open the <a href="/docs/">documentation</a>.</p>`,
	}
	a.renderPublicPage(w, r, page, true, http.StatusNotFound)
}

func (a *App) renderPublicPage(w http.ResponseWriter, r *http.Request, page publicPage, noIndex bool, status int) {
	publicBaseURL := configuredPublicBaseURL(a)
	apiBaseURL := configuredAPIBaseURL(a)
	canonical := joinBasePath(publicBaseURL, page.Path)
	if page.Slug == "not-found" {
		canonical = joinBasePath(publicBaseURL, "/")
	}

	data := sitePageData{
		Page:           page,
		CanonicalURL:   canonical,
		PublicBaseURL:  publicBaseURL,
		APIBaseURL:     apiBaseURL,
		OfficialURL:    officialWorkshopURL,
		StructuredData: structuredData(page, canonical, publicBaseURL),
		Endpoints:      endpointDocs(),
		GeneratedAt:    time.Now().UTC().Format("2006-01-02"),
		NoIndex:        noIndex,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if noIndex {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=300")
	}
	w.WriteHeader(status)
	if err := siteTemplate.Execute(w, data); err != nil {
		http.Error(w, "failed to render page", http.StatusInternalServerError)
	}
}

func (a *App) serveRobots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write([]byte("User-agent: *\nAllow: /\n\nSitemap: " + joinBasePath(configuredPublicBaseURL(a), "/sitemap.xml") + "\n"))
}

func (a *App) serveSitemap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	data := struct {
		PublicBaseURL string
		Pages         []publicPage
		LastMod       string
	}{
		PublicBaseURL: configuredPublicBaseURL(a),
		Pages:         publicPages,
		LastMod:       "2026-07-09",
	}
	_ = sitemapTemplate.Execute(w, data)
}

func (a *App) redirectToCanonicalPublicHost(w http.ResponseWriter, r *http.Request, path string) bool {
	if !a.Config.PublicCanonicalRedirects {
		return false
	}
	publicBaseURL := configuredPublicBaseURL(a)
	canonical, err := url.Parse(publicBaseURL)
	if err != nil || canonical.Host == "" {
		return false
	}
	requestHost := hostWithoutPort(r.Host)
	canonicalHost := hostWithoutPort(canonical.Host)
	if requestHost == "" || strings.EqualFold(requestHost, canonicalHost) {
		return false
	}
	target := *canonical
	target.Path = path
	target.RawQuery = r.URL.RawQuery
	http.Redirect(w, r, target.String(), http.StatusPermanentRedirect)
	return true
}

func configuredPublicBaseURL(a *App) string {
	base := strings.TrimRight(a.Config.PublicBaseURL, "/")
	if base == "" {
		base = strings.TrimRight(a.Config.FullURL, "/")
	}
	if base == "" {
		base = "http://localhost:8000"
	}
	return base
}

func configuredAPIBaseURL(a *App) string {
	base := strings.TrimRight(a.Config.APIBaseURL, "/")
	if base == "" {
		base = strings.TrimRight(a.Config.FullURL, "/")
	}
	if base == "" {
		base = "http://localhost:8000"
	}
	return base
}

func joinBasePath(base string, path string) string {
	base = strings.TrimRight(base, "/")
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

func hostWithoutPort(host string) string {
	host = strings.TrimSpace(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

func structuredData(page publicPage, canonical string, publicBaseURL string) htmltemplate.JS {
	graph := []map[string]any{
		{
			"@type": "Organization",
			"@id":   publicBaseURL + "/#organization",
			"name":  "Cedarline",
			"url":   "https://cedarline.digital",
		},
		{
			"@type":       "WebSite",
			"@id":         publicBaseURL + "/#website",
			"name":        "Reforger Mods API",
			"url":         publicBaseURL + "/",
			"description": publicPages[0].Description,
			"publisher": map[string]string{
				"@id": publicBaseURL + "/#organization",
			},
		},
	}
	if page.Slug == "home" || page.Slug == "api" {
		graph = append(graph, map[string]any{
			"@type":               "SoftwareApplication",
			"@id":                 publicBaseURL + "/#software",
			"name":                "Reforger Mods API",
			"url":                 publicBaseURL + "/",
			"applicationCategory": "DeveloperApplication",
			"operatingSystem":     "Any",
			"description":         publicPages[0].Description,
			"publisher": map[string]string{
				"@id": publicBaseURL + "/#organization",
			},
		})
	}
	if strings.HasPrefix(page.Path, "/docs/") {
		graph = append(graph, map[string]any{
			"@type": "BreadcrumbList",
			"itemListElement": []map[string]any{
				{"@type": "ListItem", "position": 1, "name": "Documentation", "item": publicBaseURL + "/docs/"},
				{"@type": "ListItem", "position": 2, "name": page.H1, "item": canonical},
			},
		})
	}
	doc := map[string]any{"@context": "https://schema.org", "@graph": graph}
	b, err := json.Marshal(doc)
	if err != nil {
		return "{}"
	}
	return htmltemplate.JS(b)
}

var sitemapTemplate = texttemplate.Must(texttemplate.New("sitemap").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
{{- range .Pages }}
  <url>
    <loc>{{ $.PublicBaseURL }}{{ .Path }}</loc>
    <lastmod>{{ $.LastMod }}</lastmod>
    <changefreq>{{ .ChangeFreq }}</changefreq>
    <priority>{{ .Priority }}</priority>
  </url>
{{- end }}
</urlset>
`))

var siteTemplate = htmltemplate.Must(htmltemplate.New("site").Parse(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>{{ .Page.Title }}</title>
    <link href="/static/bootstrap/css/bootstrap.min.css" rel="stylesheet">
    <link href="/static/bootstrap-icons/bootstrap-icons.css" rel="stylesheet">
    <link href="/static/highlight.js/styles/atom-one-dark.css" rel="stylesheet">
    <link href="/static/global/global.css" rel="stylesheet">
    <link href="/static/index.css" rel="stylesheet">
    <link rel="icon" type="image/png" sizes="32x32" href="/static/assets/reforger-mods-favicon-32.png">
    <link rel="icon" type="image/png" sizes="64x64" href="/static/assets/reforger-mods-favicon-64.png">
    <link rel="apple-touch-icon" href="/static/assets/reforger-mods-favicon-256.png">
    <link rel="canonical" href="{{ .CanonicalURL }}">
    <meta name="author" content="Cedarline">
    <meta name="description" content="{{ .Page.Description }}">
    {{ if .NoIndex }}<meta name="robots" content="noindex, nofollow">{{ else }}<meta name="robots" content="index, follow, max-image-preview:large">{{ end }}
    <meta name="application-name" content="Reforger Mods API">
    <meta property="theme-color" content="#26C29A">
    <meta property="og:title" content="{{ .Page.Title }}">
    <meta property="og:description" content="{{ .Page.Description }}">
    <meta property="og:url" content="{{ .CanonicalURL }}">
    <meta property="og:site_name" content="Reforger Mods API">
    <meta property="og:image" content="{{ .PublicBaseURL }}/static/assets/reforger-mods-favicon-256.png">
    <meta property="og:type" content="website">
    <meta name="twitter:card" content="summary">
    <meta name="twitter:title" content="{{ .Page.Title }}">
    <meta name="twitter:description" content="{{ .Page.Description }}">
    <meta name="twitter:image" content="{{ .PublicBaseURL }}/static/assets/reforger-mods-favicon-256.png">
    <script type="application/ld+json">{{ .StructuredData }}</script>
  </head>
  <body data-bs-theme="dark" data-site-url="{{ .PublicBaseURL }}/" data-api-base-url="{{ .APIBaseURL }}" data-default-page="{{ .Page.MarkdownPage }}">
    <div class="p-2 d-lg-inline d-none"></div>
    <header class="site-header border-bottom fixed-top d-lg-inline d-none blueprint-navbar">
      <div class="container">
        <div class="site-header-inner">
          <a href="/" class="site-brand link-body-emphasis text-decoration-none">
            <img src="/static/assets/reforger-mods-icon-dark.png" width="30" height="30" class="site-logo site-logo-dark" alt="Reforger Mods API">
            <img src="/static/assets/reforger-mods-icon-light.png" width="30" height="30" class="site-logo site-logo-light" alt="Reforger Mods API">
            <span class="site-brand-text">Reforger Mods API</span>
          </a>
          <ul class="site-nav nav">
            <li><a href="/" class="nav-link link-body-emphasis" data-nav-page="home">Overview</a></li>
            <li><a href="/arma-reforger-mods/" class="nav-link link-body-emphasis" data-nav-page="mods">Mods</a></li>
            <li><a href="/arma-reforger-mods-api/" class="nav-link link-body-emphasis" data-nav-page="documentation/api">API</a></li>
            <li><a href="/docs/changelog/" class="nav-link link-body-emphasis" data-nav-page="documentation/changelog">Changelog</a></li>
          </ul>
          <ul class="site-actions navbar-nav flex-row flex-wrap ms-md-auto">
            <li class="nav-item"><a class="site-contact-link" href="mailto:support@cedarline.digital">Contact</a></li>
            <li class="nav-item dropdown">
              <button class="btn btn-link nav-link py-2 px-0 px-lg-2 dropdown-toggle d-flex align-items-center site-theme-button" id="bd-theme" type="button" aria-expanded="false" data-bs-toggle="dropdown" data-bs-display="static" aria-label="Toggle theme (dark)">
                <i class="bi bi-moon-stars-fill my-1 theme-icon-active"></i>
                <span class="d-lg-none ms-2" id="bd-theme-text">Toggle theme</span>
              </button>
              <ul class="dropdown-menu dropdown-menu-end position-absolute gap-1 p-2 rounded-3 mx-0 border-0 shadow w-220px" aria-labelledby="bd-theme-text">
                <li><button type="button" class="theme-switcher-btn dropdown-item rounded-2 d-flex align-items-center" data-bs-theme-value="light" aria-pressed="false"><i class="bi bi-sun-fill me-2 opacity-50"></i> Light</button></li>
                <li><button type="button" class="theme-switcher-btn dropdown-item rounded-2 d-flex align-items-center active" data-bs-theme-value="dark" aria-pressed="true"><i class="bi bi-moon-stars-fill me-2 opacity-50"></i> Dark</button></li>
                <li><button type="button" class="theme-switcher-btn dropdown-item rounded-2 d-flex align-items-center" data-bs-theme-value="auto" aria-pressed="false"><i class="bi bi-circle-half me-2 opacity-50"></i> Auto</button></li>
              </ul>
            </li>
          </ul>
        </div>
      </div>
    </header>
    <div class="p-2 d-lg-none d-inline"></div>
    <nav class="navbar fixed-top d-lg-none d-inline blueprint-navbar-mobile">
      <div class="container-fluid">
        <a href="/" class="site-brand link-body-emphasis text-decoration-none"><img src="/static/assets/reforger-mods-icon-dark.png" width="34" height="34" class="site-logo site-logo-dark" alt="Reforger Mods API"><img src="/static/assets/reforger-mods-icon-light.png" width="34" height="34" class="site-logo site-logo-light" alt="Reforger Mods API"><span class="site-brand-text">Reforger Mods API</span></a>
        <div class="dropdown">
          <button class="btn btn-secondary bg-dark-subtle border-0 nav-dropdown-btn" type="button" data-bs-toggle="dropdown" aria-expanded="false">
            <span class="navbar-toggler-icon"></span>
          </button>
          <ul class="dropdown-menu dropdown-menu-end gap-1 p-2 rounded-3 mx-0 border-0 shadow w-220px">
            <li><a class="dropdown-item rounded-2" href="/">Home</a></li>
            <li><a class="dropdown-item rounded-2" href="/arma-reforger-mods/">Mods</a></li>
            <li><a class="dropdown-item rounded-2" href="/arma-reforger-mods-api/">API</a></li>
            <li><a class="dropdown-item rounded-2" href="/docs/changelog/">Changelog</a></li>
            <li><a class="dropdown-item rounded-2" href="/privacy/">Privacy</a></li>
            <li><a class="dropdown-item rounded-2" href="/terms/">Terms</a></li>
          </ul>
        </div>
      </div>
    </nav>
    <main class="site-main">
      <div class="container docs-shell">
        <div class="row g-4">
          <div class="col-lg-2 col-md-4 col-sm-12">
            <aside class="docs-sidebar">
              <div class="docs-category"><div class="docs-version">API v1</div></div>
              <div class="docs-category">
                <div class="docs-category-title">Project</div>
                <a href="/"><button type="button" class="btn btn-sm text-start docs-nav">Overview</button></a>
                <a href="/arma-reforger-mods/"><button type="button" class="btn btn-sm text-start docs-nav">Arma Reforger Mods</button></a>
              </div>
              <div class="docs-category">
                <div class="docs-category-title">Documentation</div>
                <a href="/arma-reforger-mods-api/"><button type="button" class="btn btn-sm text-start docs-nav">API</button></a>
                <a href="/docs/mod-structures/"><button type="button" class="btn btn-sm text-start docs-nav">Mod Structures</button></a>
                <a href="/docs/changelog/"><button type="button" class="btn btn-sm text-start docs-nav">Changelog</button></a>
                <a href="/docs/methodology/"><button type="button" class="btn btn-sm text-start docs-nav">Methodology</button></a>
              </div>
              <div class="docs-category">
                <div class="docs-category-title">Legal</div>
                <a href="/privacy/"><button type="button" class="btn btn-sm text-start docs-nav">Privacy</button></a>
                <a href="/terms/"><button type="button" class="btn btn-sm text-start docs-nav">Terms</button></a>
              </div>
            </aside>
          </div>
          <div class="container d-lg-none d-md-none d-sm-inline mb-4"><div class="border-bottom"></div></div>
          <div class="col-lg-10 col-md-8 col-sm-12 docs-content" id="content">{{ .Page.Content }}</div>
        </div>
      </div>
    </main>
    <div class="container">
      <footer class="site-footer border-top">
        <div class="site-footer-main">
          <div class="site-footer-brand">
            <a href="/" class="d-flex align-items-center link-body-emphasis text-decoration-none">
              <img src="/static/assets/reforger-mods-icon-dark.png" width="26" height="26" class="site-logo site-logo-dark" alt="Reforger Mods API">
              <img src="/static/assets/reforger-mods-icon-light.png" width="26" height="26" class="site-logo site-logo-light" alt="Reforger Mods API">
            </a>
            <div>
              <div class="site-footer-title">© 2025-2026 reforgermods.net</div>
              <div class="site-footer-disclaimer">Reforger Mods API is an independent, unofficial API service and is not affiliated with Bohemia Interactive.</div>
            </div>
          </div>
          <div class="site-footer-links">
            <a href="https://cedarline.digital" class="site-footer-link">by cedarline.digital</a>
            <a href="/privacy/" class="site-footer-link">Privacy</a>
            <a href="/terms/" class="site-footer-link">Terms</a>
            <a href="mailto:support@cedarline.digital" class="site-footer-link">Contact</a>
          </div>
        </div>
      </footer>
    </div>
    <script src="/static/bootstrap/js/bootstrap.bundle.js"></script>
    <script src="/static/bootstrap/etc/theme-switcher.js"></script>
    <script src="/static/marked/marked.min.js"></script>
    <script src="/static/highlight.js/highlight.min.js"></script>
    <script src="/static/index.js"></script>
  </body>
</html>
`))

const homeLandingHTML = htmltemplate.HTML(`<section class="landing-hero">
  <div class="landing-hero-copy">
    <div class="landing-kicker">Reforger Mods API</div>
    <h1>Arma Reforger mod metadata, ready for your app.</h1>
    <p class="landing-lede">An unofficial developer-facing data source for Arma Reforger Workshop mod information. It turns public Workshop metadata into cached JSON for tools, bots, dashboards, panels, and server workflows.</p>
    <div class="landing-actions">
      <a href="/arma-reforger-mods-api/" class="landing-primary-action"><i class="bi bi-terminal"></i> API Reference</a>
      <a href="/docs/changelog/" class="landing-secondary-action">Changelog</a>
      <a href="/arma-reforger-mods/" class="landing-secondary-action">Arma Reforger mods</a>
    </div>
  </div>
  <div class="landing-panel" aria-label="API example">
    <div class="landing-panel-header">
      <div class="landing-panel-chrome"><span></span><span></span><span></span></div>
      <div class="landing-panel-label">Example request</div>
    </div>
    <code>GET /v1/mods?search=radio&amp;sort=newest</code>
    <div class="landing-panel-meta">
      <span><i class="bi bi-check2-circle"></i> Normalized JSON responses</span>
      <span><i class="bi bi-clock"></i> Cached and refresh-aware</span>
      <span><i class="bi bi-link-45deg"></i> Links back to the official Workshop</span>
    </div>
  </div>
</section>
<div class="landing-metrics" aria-label="API defaults">
  <div><span class="landing-status-label">Version</span><strong>/v1</strong></div>
  <div><span class="landing-status-label">Public limit</span><strong>60 / min default</strong></div>
  <div><span class="landing-status-label">Cold cache</span><strong>202 + Retry-After</strong></div>
</div>
<p class="landing-note">Reforger Mods API is independent and unofficial. It is not affiliated with Bohemia Interactive, and it is not a replacement for the official Workshop.</p>
<h2>What it is</h2>
<p>Reforger Mods API retrieves public Arma Reforger Workshop mod pages, normalizes useful fields, and serves them through cache-friendly JSON endpoints. It is for software that needs mod names, authors, IDs, Workshop links, images, versions, sizes, tags, dependencies, and scenario metadata when those fields are available.</p>
<h2>Who it helps</h2>
<div class="landing-grid">
  <a href="/arma-reforger-mods/" class="landing-link-card"><i class="bi bi-search"></i><span>Players and server owners</span><small>Understand how the service can support mod lists, server checks, and search workflows.</small></a>
  <a href="/arma-reforger-mods-api/" class="landing-link-card"><i class="bi bi-braces"></i><span>Developers</span><small>Open the Markdown-controlled API docs for endpoints, cache headers, errors, and examples.</small></a>
  <a href="/docs/mod-structures/" class="landing-link-card"><i class="bi bi-diagram-3"></i><span>Tool builders</span><small>Review the response structures used by bots, panels, dashboards, and integrations.</small></a>
</div>`)

const modsLandingHTML = htmltemplate.HTML(`<h1>Arma Reforger Mods</h1>
<p class="landing-lede">Reforger Mods API helps people work with Arma Reforger Workshop mod data in software. It is not the official mod browser, but it makes public Workshop metadata easier to search, validate, and integrate.</p>
<h2>Practical use cases</h2>
<ul>
  <li>Find structured metadata for a Workshop mod or search result.</li>
  <li>Build a mod list tool for a community, server, or dashboard.</li>
  <li>Validate server configuration inputs against Workshop IDs.</li>
  <li>Add mod lookup to Discord bots and server panels.</li>
  <li>Track dependencies where the public Workshop detail page exposes them.</li>
</ul>
<div class="landing-grid">
  <a href="/arma-reforger-mods-api/" class="landing-link-card"><i class="bi bi-terminal"></i><span>API documentation</span><small>Endpoints and request handling.</small></a>
  <a href="/docs/methodology/" class="landing-link-card"><i class="bi bi-info-circle"></i><span>Data source and limitations</span><small>How cache freshness and upstream Workshop data work.</small></a>
  <a href="https://reforger.armaplatform.com/workshop" class="landing-link-card"><i class="bi bi-box-arrow-up-right"></i><span>Official Workshop</span><small>Use the official site for browsing, publishing, and account workflows.</small></a>
</div>
<h2>FAQ</h2>
<h3>Is this official?</h3>
<p>No. It is independent and unofficial.</p>
<h3>Is the data real-time?</h3>
<p>No. Responses are cached by design and may be stale.</p>
<h3>Does it rank or review mods?</h3>
<p>No. It exposes metadata from public Workshop pages and avoids fabricated rankings or usage claims.</p>`)

const methodologyHTML = htmltemplate.HTML(`<h1>Data Source and Methodology</h1>
<p>Reforger Mods API retrieves publicly visible Arma Reforger Workshop listing and detail pages, then normalizes available mod metadata into JSON. The service is independent and unofficial.</p>
<h2>What data is retrieved</h2>
<p>Current responses can include names, authors, Workshop URLs, API URLs, images, ratings, versions, game versions, sizes, subscriber and download counts, created and modified dates, summaries, descriptions, licenses, tags, dependencies, and scenarios when those fields are present upstream.</p>
<h2>Caching and freshness</h2>
<p>Responses are cached in memory. Cold cache misses can return <code>202 Accepted</code> with <code>Retry-After</code>; clients should wait and retry the original URL. Stale data may be served while a background refresh runs.</p>
<h2>Known limitations</h2>
<ul>
  <li>Workshop layout, fields, sorting, and availability can change upstream.</li>
  <li>Data is not guaranteed complete or real-time.</li>
  <li>The API should not be used for ownership, entitlement, moderation, identity, or account decisions.</li>
</ul>
<h2>Corrections and issues</h2>
<p>Report API or documentation issues to <a href="mailto:support@cedarline.digital">support@cedarline.digital</a>. Use official Bohemia Interactive channels for account, publishing, moderation, or Workshop ownership issues.</p>`)

const apiFallbackHTML = htmltemplate.HTML(`<h1>Arma Reforger Mods API</h1><p>The API reference is loaded from <code>static/pages/documentation/api.md</code>.</p>`)
const docsFallbackHTML = htmltemplate.HTML(`<h1>Reforger Mods API Documentation</h1><p>The documentation is loaded from Markdown files in <code>static/pages/documentation/</code>.</p>`)
const modStructuresFallbackHTML = htmltemplate.HTML(`<h1>Mod Structures</h1><p>The model reference is loaded from <code>static/pages/documentation/mods.md</code>.</p>`)
const changelogFallbackHTML = htmltemplate.HTML(`<h1>Changelog</h1><p>The changelog is loaded from <code>static/pages/documentation/changelog.md</code>.</p>`)
const privacyFallbackHTML = htmltemplate.HTML(`<h1>Privacy Policy</h1><p>The privacy policy is loaded from <code>static/pages/privacy.md</code>.</p>`)
const termsFallbackHTML = htmltemplate.HTML(`<h1>Terms of Service</h1><p>The terms are loaded from <code>static/pages/terms.md</code>.</p>`)
