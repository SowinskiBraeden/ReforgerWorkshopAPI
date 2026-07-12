package handlers

import (
	"bytes"
	"encoding/json"
	htmltemplate "html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	texttemplate "text/template"
	"time"
)

type publicPage struct {
	Path        string
	Slug        string
	Title       string
	Description string
	H1          string
	Keywords    []string
	LastMod     string
	Image       string
	ChangeFreq  string
	Priority    string
	Content     htmltemplate.HTML
	// ToolName marks the page as an interactive tool and is used for
	// SoftwareApplication structured data.
	ToolName string
	// FAQ entries are rendered below the page content and emitted as
	// FAQPage structured data so markup and visible copy stay in sync.
	FAQ []faqItem
	// Scripts are page-specific script files loaded after the shared ones.
	Scripts []string
	// FullWidth drops the docs sidebar and gives the page the wide tool
	// layout. Prose sections inside keep a readable max-width via CSS.
	FullWidth bool
}

type faqItem struct {
	Question string
	Answer   string
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
	Keywords       string
	LastMod        string
	PageImageURL   string
	StaticVersion  string
	Endpoints      []apiEndpointDoc
	GeneratedAt    string
	NoIndex        bool
}

const officialWorkshopURL = "https://reforger.armaplatform.com/workshop"
const defaultPageLastMod = "2026-07-12"
const defaultPageImagePath = "/static/assets/reforger-mods-favicon-256.png"

// publicPages is the full sitemap-facing page registry: core pages defined
// here, plus the interactive tool pages and guides defined in their own files.
var publicPages = append(append(append([]publicPage{}, corePages...), toolPages...), guidePages...)

var corePages = []publicPage{
	{
		Path:        "/",
		Slug:        "home",
		Title:       "Arma Reforger Mods API and Server Admin Tools | Reforger Mods API",
		Description: "Reforger Mods API is an unofficial, cached JSON data source for Arma Reforger Workshop mod metadata, with a free mod browser, config.json validator, and server config generator.",
		H1:          "Arma Reforger mod data and server tools.",
		Keywords:    []string{"Arma Reforger mods", "Reforger Mods API", "Arma Reforger Workshop API", "Arma Reforger server tools", "config.json tools"},
		ChangeFreq:  "weekly",
		Priority:    "1.0",
		Image:       "/static/assets/web/home-hero.jpg",
		FullWidth:   true,
		Content:     homeLandingHTML,
	},
	{
		Path:        "/arma-reforger-mods-api/",
		Slug:        "api",
		Title:       "Arma Reforger Mods API Documentation | Reforger Mods API",
		Description: "Use the unofficial Reforger Mods API to fetch cached Arma Reforger Workshop mod lists, search results, mod details, dependencies, and refresh-job status.",
		H1:          "Arma Reforger Mods API",
		Keywords:    []string{"Arma Reforger Mods API", "Arma Reforger Workshop data", "Workshop mod API", "mod metadata API", "Reforger API documentation"},
		ChangeFreq:  "weekly",
		Priority:    "0.95",
		FullWidth:   true,
		Content:     apiReferenceHTML,
	},
	{
		Path:        "/docs/changelog/",
		Slug:        "changelog",
		Title:       "Changelog | Reforger Mods API",
		Description: "Release notes for Reforger Mods API, including versioned endpoints, cache behavior, rate limiting, and reliability changes.",
		H1:          "Changelog",
		Keywords:    []string{"Reforger Mods API changelog", "API release notes", "Arma Reforger API updates"},
		ChangeFreq:  "monthly",
		Priority:    "0.5",
		FullWidth:   true,
		Content:     changelogFallbackHTML,
	},
	{
		Path:        "/privacy/",
		Slug:        "privacy",
		Title:       "Privacy Policy | Reforger Mods API",
		Description: "Privacy policy for Reforger Mods API, an independent Arma Reforger Workshop metadata API.",
		H1:          "Privacy Policy",
		Keywords:    []string{"Reforger Mods API privacy", "Arma Reforger tools privacy"},
		ChangeFreq:  "yearly",
		Priority:    "0.3",
		Content:     privacyFallbackHTML,
	},
	{
		Path:        "/terms/",
		Slug:        "terms",
		Title:       "Terms of Service | Reforger Mods API",
		Description: "Terms of service for Reforger Mods API, an independent Arma Reforger Workshop metadata API.",
		H1:          "Terms of Service",
		Keywords:    []string{"Reforger Mods API terms", "Arma Reforger tools terms"},
		ChangeFreq:  "yearly",
		Priority:    "0.3",
		Content:     termsFallbackHTML,
	},
}

func endpointDocs() []apiEndpointDoc {
	return []apiEndpointDoc{
		{Method: "GET", Path: "/v1/health", Summary: "Process health check. It does not request Workshop data.", Parameters: "None.", CachePolicy: "no-store"},
		{Method: "GET", Path: "/v1/mods", Summary: "First page of Arma Reforger Workshop mod previews.", Parameters: "Optional search text, sort, and tags/category filter.", CachePolicy: "List cache TTL plus stale serving window."},
		{Method: "GET", Path: "/v1/mods/{page}", Summary: "A specific page of Workshop mod previews.", Parameters: "page must be a positive integer; optional search, sort, and tags/category filter.", CachePolicy: "List cache TTL plus stale serving window."},
		{Method: "GET", Path: "/v1/search?search={query}", Summary: "Convenience route for first-page search results.", Parameters: "search text; optional sort and tags/category filter.", CachePolicy: "Same response shape and cache policy as /v1/mods."},
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
		return "/arma-reforger-mods-api/#mod-object"
	case "documentation":
		return "/arma-reforger-mods-api/"
	case "documentation/changelog":
		return "/docs/changelog/"
	case "privacy":
		return "/privacy/"
	case "terms":
		return "/terms/"
	}
	return ""
}

// serveComingSoon renders the placeholder page for API keys and pricing.
// It is intentionally not part of publicPages, so it stays out of the
// sitemap and is served with noindex until the feature ships.
func (a *App) serveComingSoon(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/coming-soon/" {
		http.Redirect(w, r, "/coming-soon/", http.StatusMovedPermanently)
		return
	}
	page := publicPage{
		Path:        "/coming-soon/",
		Slug:        "coming-soon",
		Title:       "Coming Soon | Reforger Mods API",
		Description: "API keys and paid tiers for Reforger Mods API are in development. The public tier stays free at 60 requests per minute.",
		H1:          "Coming soon",
		FullWidth:   true,
		Content:     comingSoonHTML,
	}
	a.renderPublicPage(w, r, page, true, http.StatusOK)
}

func (a *App) serveNotFound(w http.ResponseWriter, r *http.Request) {
	page := publicPage{
		Path:        r.URL.Path,
		Slug:        "not-found",
		Title:       "Page Not Found | Reforger Mods API",
		Description: "The requested Reforger Mods API page was not found.",
		H1:          "Page not found",
		Content:     notFoundHTML,
		FullWidth:   true,
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
		Keywords:       pageKeywords(page),
		LastMod:        pageLastMod(page),
		PageImageURL:   pageImageURL(page, publicBaseURL),
		StaticVersion:  staticAssetVersion(),
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

	var body bytes.Buffer
	if err := siteTemplate.Execute(&body, data); err != nil {
		http.Error(w, "failed to render page", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write(body.Bytes())
}

func (a *App) serveRobots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write([]byte("User-agent: *\nAllow: /\n\nSitemap: " + joinBasePath(configuredPublicBaseURL(a), "/sitemap.xml") + "\n"))
}

func (a *App) serveSitemap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	pages := make([]sitemapPage, 0, len(publicPages))
	for _, page := range publicPages {
		pages = append(pages, sitemapPage{
			Path:       page.Path,
			LastMod:    pageLastMod(page),
			ChangeFreq: page.ChangeFreq,
			Priority:   page.Priority,
		})
	}
	data := struct {
		PublicBaseURL string
		Pages         []sitemapPage
	}{
		PublicBaseURL: configuredPublicBaseURL(a),
		Pages:         pages,
	}
	_ = sitemapTemplate.Execute(w, data)
}

type sitemapPage struct {
	Path       string
	LastMod    string
	ChangeFreq string
	Priority   string
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

func pageLastMod(page publicPage) string {
	if page.LastMod != "" {
		return page.LastMod
	}
	return defaultPageLastMod
}

func staticAssetVersion() string {
	return strings.ReplaceAll(defaultPageLastMod, "-", "")
}

func pageKeywords(page publicPage) string {
	if len(page.Keywords) > 0 {
		return strings.Join(page.Keywords, ", ")
	}
	return "Arma Reforger, Arma Reforger mods, Arma Reforger Workshop, Reforger Mods API"
}

func pageImageURL(page publicPage, publicBaseURL string) string {
	image := strings.TrimSpace(page.Image)
	if image == "" {
		image = defaultPageImagePath
	}
	if strings.HasPrefix(image, "http://") || strings.HasPrefix(image, "https://") {
		return image
	}
	return joinBasePath(publicBaseURL, image)
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
			"potentialAction": map[string]any{
				"@type":       "SearchAction",
				"target":      publicBaseURL + "/arma-reforger-mods/?search={search_term_string}",
				"query-input": "required name=search_term_string",
			},
			"publisher": map[string]string{
				"@id": publicBaseURL + "/#organization",
			},
		},
		{
			"@type":        "WebPage",
			"@id":          canonical + "#webpage",
			"url":          canonical,
			"name":         page.Title,
			"description":  page.Description,
			"keywords":     pageKeywords(page),
			"dateModified": pageLastMod(page),
			"inLanguage":   "en-US",
			"isPartOf": map[string]string{
				"@id": publicBaseURL + "/#website",
			},
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
	if page.Slug == "api" || page.Slug == "api-quickstart" {
		graph = append(graph, map[string]any{
			"@type":         "WebAPI",
			"@id":           publicBaseURL + "/#webapi",
			"name":          "Reforger Mods API",
			"url":           publicBaseURL + "/",
			"description":   "A public read-only JSON API for Arma Reforger Workshop mod metadata, mod details, dependencies, and scenarios.",
			"documentation": publicBaseURL + "/arma-reforger-mods-api/",
			"provider": map[string]string{
				"@id": publicBaseURL + "/#organization",
			},
		})
	}
	if page.ToolName != "" {
		graph = append(graph, map[string]any{
			"@type":               "SoftwareApplication",
			"@id":                 canonical + "#tool",
			"name":                page.ToolName,
			"url":                 canonical,
			"applicationCategory": "UtilitiesApplication",
			"operatingSystem":     "Any",
			"description":         page.Description,
			"keywords":            pageKeywords(page),
			"isAccessibleForFree": true,
			"offers": map[string]any{
				"@type":         "Offer",
				"price":         "0",
				"priceCurrency": "USD",
			},
			"publisher": map[string]string{
				"@id": publicBaseURL + "/#organization",
			},
		})
	}
	if len(page.FAQ) > 0 {
		questions := make([]map[string]any, 0, len(page.FAQ))
		for _, item := range page.FAQ {
			questions = append(questions, map[string]any{
				"@type": "Question",
				"name":  item.Question,
				"acceptedAnswer": map[string]any{
					"@type": "Answer",
					"text":  item.Answer,
				},
			})
		}
		graph = append(graph, map[string]any{
			"@type":      "FAQPage",
			"@id":        canonical + "#faq",
			"mainEntity": questions,
		})
	}
	if strings.HasPrefix(page.Path, "/docs/") {
		graph = append(graph, map[string]any{
			"@type": "BreadcrumbList",
			"@id":   canonical + "#breadcrumb",
			"itemListElement": []map[string]any{
				{"@type": "ListItem", "position": 1, "name": "Documentation", "item": publicBaseURL + "/docs/"},
				{"@type": "ListItem", "position": 2, "name": page.H1, "item": canonical},
			},
		})
	}
	if strings.HasPrefix(page.Path, "/guides/") && page.Path != "/guides/" {
		graph = append(graph, map[string]any{
			"@type": "BreadcrumbList",
			"@id":   canonical + "#breadcrumb",
			"itemListElement": []map[string]any{
				{"@type": "ListItem", "position": 1, "name": "Guides", "item": publicBaseURL + "/guides/"},
				{"@type": "ListItem", "position": 2, "name": page.H1, "item": canonical},
			},
		})
	}
	if page.ToolName != "" && page.Path != "/" {
		graph = append(graph, map[string]any{
			"@type": "BreadcrumbList",
			"@id":   canonical + "#breadcrumb",
			"itemListElement": []map[string]any{
				{"@type": "ListItem", "position": 1, "name": "Reforger Mods", "item": publicBaseURL + "/"},
				{"@type": "ListItem", "position": 2, "name": page.ToolName, "item": canonical},
			},
		})
	}
	if strings.HasPrefix(page.Path, "/arma-reforger-mods/") && page.Path != "/arma-reforger-mods/" {
		graph = append(graph, map[string]any{
			"@type": "BreadcrumbList",
			"@id":   canonical + "#breadcrumb",
			"itemListElement": []map[string]any{
				{"@type": "ListItem", "position": 1, "name": "Arma Reforger Mods", "item": publicBaseURL + "/arma-reforger-mods/"},
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

type siteFragmentData struct {
	OfficialServerConfigDocsURL string
}

func htmlFragment(name string) htmltemplate.HTML {
	return htmlFragmentTemplate(name, nil)
}

func htmlFragmentTemplate(name string, data any) htmltemplate.HTML {
	content, err := os.ReadFile(htmlTemplatePath(name))
	if err != nil {
		panic(err)
	}
	if data == nil {
		return htmltemplate.HTML(content)
	}
	tmpl := htmltemplate.Must(htmltemplate.New(filepath.Base(name)).Parse(string(content)))
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, data); err != nil {
		panic(err)
	}
	return htmltemplate.HTML(rendered.String())
}

func htmlTemplatePath(name string) string {
	candidates := []string{
		filepath.Join("static", "html", name),
		filepath.Join("..", "..", "static", "html", name),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return candidates[0]
}

var sitemapTemplate = texttemplate.Must(texttemplate.ParseFiles(htmlTemplatePath("templates/sitemap.xml")))

var siteTemplate = htmltemplate.Must(htmltemplate.ParseFiles(htmlTemplatePath("templates/site.html")))

var homeLandingHTML = htmlFragment("core/home.html")

var apiReferenceHTML = htmlFragment("core/api-reference.html")
var comingSoonHTML = htmlFragment("core/coming-soon.html")
var notFoundHTML = htmlFragment("core/not-found.html")

var changelogFallbackHTML = htmlFragment("core/changelog.html")
var privacyFallbackHTML = htmlFragment("core/privacy.html")
var termsFallbackHTML = htmlFragment("core/terms.html")
