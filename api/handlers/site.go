package handlers

import (
	"bytes"
	"encoding/json"
	htmltemplate "html/template"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	// NoIndex keeps utility/account pages renderable while excluding them
	// from the sitemap and search indexing.
	NoIndex bool
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
const defaultPageLastMod = "2026-07-15"
const defaultPageImagePath = "/static/assets/reforger-mods-favicon-256.png"

// publicPages is the full sitemap-facing page registry: core pages defined
// here, plus the interactive tool pages and guides defined in their own files.
var publicPages = append(append(append([]publicPage{}, corePages...), toolPages...), guidePages...)

var corePages = []publicPage{
	{
		Path:        "/",
		Slug:        "home",
		Title:       "Reforger Mods | Arma Reforger Mods API and Server Tools",
		Description: "Search Arma Reforger mods, build and validate server config.json files, manage mod lists, and integrate Workshop metadata through a public developer API.",
		H1:          "Arma Reforger Mods API and Server Tools",
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
		Title:       "Arma Reforger Mods API Reference | Workshop Metadata Endpoints",
		Description: "Reference documentation for the Arma Reforger Mods API, including mod search, details, dependencies, cache headers, rate limits, and refresh jobs.",
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
		Description: "Privacy policy for Reforger Mods API, including API keys, billing, support, logs, and Workshop metadata handling.",
		H1:          "Privacy Policy",
		Keywords:    []string{"Reforger Mods API privacy", "API keys privacy", "Arma Reforger tools privacy"},
		ChangeFreq:  "yearly",
		Priority:    "0.3",
		FullWidth:   true,
		Content:     privacyFallbackHTML,
	},
	{
		Path:        "/terms/",
		Slug:        "terms",
		Title:       "Terms of Service | Reforger Mods API",
		Description: "Terms of service for Reforger Mods API, including API usage, API keys, paid plans, billing, rate limits, and acceptable use.",
		H1:          "Terms of Service",
		Keywords:    []string{"Reforger Mods API terms", "API keys terms", "Arma Reforger tools terms"},
		ChangeFreq:  "yearly",
		Priority:    "0.3",
		FullWidth:   true,
		Content:     termsFallbackHTML,
	},
	{
		Path:        "/pricing/",
		Slug:        "pricing",
		Title:       "Reforger Mods API Pricing | Developer and Pro API Keys",
		Description: "Compare free, Developer, and Pro access for Reforger Mods API, including rate limits, API key counts, and account billing.",
		H1:          "Reforger Mods API Plans",
		Keywords:    []string{"Reforger Mods API pricing", "API key pricing", "Arma Reforger API plans"},
		ChangeFreq:  "monthly",
		Priority:    "0.7",
		FullWidth:   true,
		Content:     pricingHTML,
		Scripts:     []string{"/static/billing.js"},
		FAQ: []faqItem{
			{
				Question: "Do I need an account to use the API?",
				Answer:   "No. Anonymous cached access is free and needs no key. Paid plans use passwordless email sign-in: your email at checkout is your account, and a one-time sign-in link manages your keys.",
			},
			{
				Question: "How do I get my API key after subscribing?",
				Answer:   "Your key is shown once right after checkout, and a sign-in link is emailed to you. You can sign in with your email any time to create, name, or revoke keys from any device.",
			},
			{
				Question: "What happens if I lose my API key?",
				Answer:   "Keys are stored hashed and cannot be shown again. Sign in with your email on the API keys page, revoke the lost key, and create a new one. It takes under a minute.",
			},
			{
				Question: "How many API keys do I get?",
				Answer:   "Developer includes 2 active keys and Pro includes 10, so you can use a separate key per app or service. All keys share your account's rate limit, and revoking a key frees its slot immediately.",
			},
			{
				Question: "Can I cancel my subscription?",
				Answer:   "Yes, any time from the Stripe Customer Portal on the account billing page. Paid keys keep working until the end of the current billing period.",
			},
		},
	},
	{
		Path:        "/billing/success/",
		Slug:        "billing-success",
		Title:       "Billing Success | Reforger Mods API",
		Description: "Retrieve your Reforger Mods API key after a successful Stripe Checkout subscription.",
		H1:          "Billing Success",
		Keywords:    []string{"Reforger Mods API key", "billing success"},
		ChangeFreq:  "yearly",
		Priority:    "0.1",
		FullWidth:   true,
		NoIndex:     true,
		Content:     billingSuccessHTML,
		Scripts:     []string{"/static/billing.js"},
	},
	{
		Path:        "/account/billing/",
		Slug:        "account-billing",
		Title:       "Account Billing | Reforger Mods API",
		Description: "Open the Stripe Customer Portal for Reforger Mods API billing management.",
		H1:          "Account Billing",
		Keywords:    []string{"Reforger Mods API billing", "Stripe customer portal"},
		ChangeFreq:  "yearly",
		Priority:    "0.1",
		FullWidth:   true,
		NoIndex:     true,
		Content:     accountBillingHTML,
		Scripts:     []string{"/static/billing.js"},
	},
	{
		Path:        "/account/api-keys/",
		Slug:        "account-api-keys",
		Title:       "API Keys | Reforger Mods API",
		Description: "View, create, and revoke Reforger Mods API keys.",
		H1:          "API Keys",
		Keywords:    []string{"Reforger Mods API keys", "API key management"},
		ChangeFreq:  "yearly",
		Priority:    "0.1",
		FullWidth:   true,
		NoIndex:     true,
		Content:     accountAPIKeysHTML,
		Scripts:     []string{"/static/billing.js"},
	},
	{
		Path:        "/support/",
		Slug:        "support",
		Title:       "Support | Reforger Mods API",
		Description: "Support information for Reforger Mods API, including API key questions, billing help, abuse reports, privacy requests, and technical issues.",
		H1:          "Support",
		Keywords:    []string{"Reforger Mods API support", "API key support", "Arma Reforger API support"},
		ChangeFreq:  "yearly",
		Priority:    "0.3",
		FullWidth:   true,
		Content:     supportFallbackHTML,
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
		a.renderPublicPage(w, r, page, page.NoIndex, http.StatusOK)
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
	case "support":
		return "/support/"
	}
	return ""
}

// serveComingSoon renders the API key options page.
// It is intentionally not part of publicPages, so it stays out of the
// sitemap and is served with noindex until API key self-serve is fully live.
func (a *App) serveComingSoon(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/coming-soon/" {
		http.Redirect(w, r, "/coming-soon/", http.StatusMovedPermanently)
		return
	}
	page := publicPage{
		Path:        "/coming-soon/",
		Slug:        "coming-soon",
		Title:       "Get an API Key | Reforger Mods API",
		Description: "Monthly API key options for Reforger Mods API, with higher request limits for projects that need more throughput than the public tier.",
		H1:          "Get an API Key",
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
	_, _ = w.Write([]byte("User-agent: *\nAllow: /\nDisallow: /account/\nDisallow: /billing/session\nDisallow: /billing/portal\nDisallow: /billing/success/\nDisallow: /stripe/\nDisallow: /internal/\nDisallow: /v1/\n\nSitemap: " + joinBasePath(configuredPublicBaseURL(a), "/sitemap.xml") + "\n"))
}

func (a *App) serveSitemap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	pages := make([]sitemapPage, 0, len(publicPages))
	for _, page := range publicPages {
		if page.NoIndex {
			continue
		}
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

var staticVersionOnce sync.Once
var staticVersionValue string

// staticAssetVersion cache-busts static asset URLs with the newest mtime
// under ./static, so edited JS/CSS is picked up on the next restart without
// hand-bumping a date. Falls back to the page lastmod date when the static
// directory is unavailable (e.g. in tests).
func staticAssetVersion() string {
	staticVersionOnce.Do(func() {
		staticVersionValue = strings.ReplaceAll(defaultPageLastMod, "-", "")
		var latest time.Time
		_ = filepath.WalkDir("./static", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if info, err := d.Info(); err == nil && info.ModTime().After(latest) {
				latest = info.ModTime()
			}
			return nil
		})
		if !latest.IsZero() {
			staticVersionValue = strconv.FormatInt(latest.Unix(), 10)
		}
	})
	return staticVersionValue
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
			"name":  "Cedarline Digital",
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
				"target":      publicBaseURL + "/mods/?search={search_term_string}",
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
	if strings.HasPrefix(page.Path, "/mods/") && page.Path != "/mods/" {
		graph = append(graph, map[string]any{
			"@type": "BreadcrumbList",
			"@id":   canonical + "#breadcrumb",
			"itemListElement": []map[string]any{
				{"@type": "ListItem", "position": 1, "name": "Arma Reforger Mods", "item": publicBaseURL + "/mods/"},
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
var supportFallbackHTML = htmlFragment("core/support.html")
var pricingHTML = htmlFragment("core/pricing.html")
var billingSuccessHTML = htmlFragment("core/billing-success.html")
var accountBillingHTML = htmlFragment("core/account-billing.html")
var accountAPIKeysHTML = htmlFragment("core/account-api-keys.html")
