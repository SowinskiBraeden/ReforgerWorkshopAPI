package handlers

import (
	htmltemplate "html/template"
	"net/http"
	"regexp"
	"strings"

	"github.com/gorilla/mux"
)

// toolPages are the interactive tool pages. The interactive parts are
// rendered by page scripts against the public API; the server-rendered HTML
// below carries the crawlable copy, headings, and internal links.
var toolPages = []publicPage{
	{
		Path:        "/mods/",
		Slug:        "mods",
		Title:       "Arma Reforger Mods Browser | Search Workshop Mods",
		Description: "Search Arma Reforger Workshop mods, view mod IDs, versions, dependencies, and details, then add mods directly to your server config.",
		H1:          "Arma Reforger Mods Browser",
		Keywords:    []string{"Arma Reforger mods", "Arma Reforger Workshop browser", "Reforger mod search", "Workshop mod dependencies", "Arma Reforger mod IDs", "config.json mods"},
		ChangeFreq:  "daily",
		Priority:    "0.9",
		ToolName:    "Arma Reforger Mods Browser",
		FullWidth:   true,
		Content:     modBrowserHTML,
		Scripts:     []string{"/static/tools/common.js", "/static/tools/mod-browser.js"},
		FAQ: []faqItem{
			{
				Question: "Is this the official Arma Reforger Workshop?",
				Answer:   "No. This mod browser is independent and unofficial. It shows cached metadata from public Workshop pages and links back to the official Workshop for every mod.",
			},
			{
				Question: "Is the mod data real-time?",
				Answer:   "No. Responses are cached by design and may be minutes to hours old. When fresh data is being fetched, the browser shows a loading state and retries automatically.",
			},
			{
				Question: "How do I add a mod to my server?",
				Answer:   "Copy the mod ID or the JSON snippet from any mod card, or use the Add to Config button to send the mod into the config builder on this site. Your server config.json lists mods under game.mods.",
			},
			{
				Question: "Does this browser rank or review mods?",
				Answer:   "No. It shows metadata from public Workshop pages, including the Workshop rating where available, and avoids fabricated rankings or usage claims.",
			},
		},
	},
	{
		Path:        "/config-validator/",
		Slug:        "config-validator",
		Title:       "Arma Reforger Config.json Validator | Find Server Config Errors",
		Description: "Validate an Arma Reforger server config.json, find JSON syntax errors, duplicate mod IDs, missing fields, and invalid mod entries.",
		H1:          "Arma Reforger Config.json Validator",
		Keywords:    []string{"Arma Reforger config validator", "config.json validator", "Arma Reforger server config", "Reforger JSON validator", "Arma Reforger mod ID validation"},
		ChangeFreq:  "monthly",
		Priority:    "0.9",
		ToolName:    "Arma Reforger Config Validator",
		FullWidth:   true,
		Content:     configValidatorHTML,
		Scripts:     []string{"/static/tools/common.js", "/static/tools/config-validate.js", "/static/tools/config-validator.js"},
		FAQ: []faqItem{
			{
				Question: "Is my config uploaded anywhere?",
				Answer:   "No. Syntax and structure validation runs entirely in your browser. If you choose to check mod IDs against the Workshop, only the mod IDs are sent to the API, never the rest of your config.",
			},
			{
				Question: "What does the validator check?",
				Answer:   "JSON syntax with line positions, the overall config structure, port ranges, required game fields like scenarioId, and the game.mods array including duplicate or malformed mod IDs. It does not enforce rules that are not publicly documented.",
			},
			{
				Question: "My config is valid but the server still fails to start. Why?",
				Answer:   "A structurally valid config can still reference a missing scenario, an unavailable mod version, or a port that is already in use. The troubleshooting guide on this site covers the most common startup failures.",
			},
		},
	},
	{
		Path:        "/config-generator/",
		Slug:        "config-generator",
		Title:       "Arma Reforger Config Generator | Build Server Config.json",
		Description: "Create an Arma Reforger server config.json, manage Workshop mods, validate settings, and export a ready-to-use server configuration.",
		H1:          "Arma Reforger Server Config Generator",
		Keywords:    []string{"Arma Reforger config generator", "Arma Reforger config.json builder", "server config generator", "Reforger config editor", "Arma Reforger server tools"},
		ChangeFreq:  "monthly",
		Priority:    "0.9",
		ToolName:    "Arma Reforger Config Generator",
		FullWidth:   true,
		Content:     configGeneratorHTML,
		Scripts:     []string{"/static/tools/common.js", "/static/tools/config-validate.js", "/static/tools/mod-list.js", "/static/tools/mod-search.js", "/static/tools/config-generator.js"},
		FAQ: []faqItem{
			{
				Question: "Where is my config stored while I edit it?",
				Answer:   "In your browser, in local storage. Nothing is uploaded. The working config is shared with the mod browser and mod manager on this site, so Add to Config buttons all update the same draft.",
			},
			{
				Question: "Can I import an existing config.json?",
				Answer:   "Yes. Paste or upload your current config. Fields the form does not know about are preserved untouched and appear in the JSON preview and in the exported file.",
			},
			{
				Question: "Does the generator support comments in config.json?",
				Answer:   "No. JSON does not support comments, and the Arma Reforger server expects plain JSON. The generator will not pretend otherwise; use the name field on mod entries to label them instead.",
			},
		},
	},
	{
		Path:        "/mod-manager/",
		Slug:        "mod-manager",
		Title:       "Arma Reforger Mod Manager | Build and Export Your Mod List",
		Description: "Search, add, remove, reorder, and validate Arma Reforger Workshop mods, then export the finished mods array for config.json.",
		H1:          "Arma Reforger Mod Manager",
		Keywords:    []string{"Arma Reforger mod manager", "config.json mods manager", "Arma Reforger dependencies", "Workshop mod list editor", "Reforger server mods"},
		ChangeFreq:  "monthly",
		Priority:    "0.85",
		ToolName:    "Arma Reforger Mod Manager",
		FullWidth:   true,
		Content:     modManagerHTML,
		Scripts:     []string{"/static/tools/common.js", "/static/tools/config-validate.js", "/static/tools/mod-list.js", "/static/tools/mod-search.js", "/static/tools/mod-manager.js"},
		FAQ: []faqItem{
			{
				Question: "How do mods get into this list?",
				Answer:   "Search Workshop mods by name right on this page, paste a Workshop ID, use Add to Config from the mod browser, or import an existing config.json in the config builder. All of them edit the same working config in your browser.",
			},
			{
				Question: "Does mod order in config.json matter?",
				Answer:   "The server resolves dependencies on its own, but a readable order helps humans maintain the list. This tool lets you reorder entries and keeps the order you choose in the exported JSON.",
			},
			{
				Question: "Can it add missing dependencies automatically?",
				Answer:   "It checks dependencies using Workshop data and lists anything your config does not include, but it only suggests additions. Dependency data comes from public Workshop pages and can lag behind, so you stay in control of what is added.",
			},
		},
	},
	{
		Path:        "/api/",
		Slug:        "api-quickstart",
		Title:       "Arma Reforger Mods API | Workshop Metadata for Developers",
		Description: "Use a documented Arma Reforger Workshop API for mod search, metadata, dependencies, versions, and server-tool integrations.",
		H1:          "Arma Reforger Mods API",
		Keywords:    []string{"Reforger Mods API quickstart", "Arma Reforger API", "Workshop data API", "Arma Reforger mod search API", "202 Accepted refresh jobs"},
		ChangeFreq:  "monthly",
		Priority:    "0.9",
		FullWidth:   true,
		Content:     apiQuickstartHTML,
		FAQ: []faqItem{
			{
				Question: "Is the Reforger Mods API official?",
				Answer:   "No. It is an independent, unofficial API that normalizes metadata from public Arma Reforger Workshop pages. It links back to the official Workshop and is not affiliated with Bohemia Interactive.",
			},
			{
				Question: "Is the API free to use?",
				Answer:   "Yes. Anonymous clients get 60 requests per minute per IP by default. Cache responses on your side, respect Retry-After, and send an identifying User-Agent or X-API-Client header.",
			},
			{
				Question: "Why do some requests return 202 Accepted?",
				Answer:   "A 202 means the data was not cached yet and a background refresh job was queued. Wait the number of seconds in the Retry-After header, then retry the same URL. It is not an error.",
			},
		},
	},
}

// Workshop mod IDs are 16 hexadecimal characters. The JSON API accepts a
// wider ID charset for forward compatibility, but HTML detail pages only
// exist for well-formed Workshop IDs so crawlers cannot generate junk URLs.
var validWorkshopPageID = regexp.MustCompile(`^[0-9A-Fa-f]{16}$`)

func (a *App) serveModDetailPage(w http.ResponseWriter, r *http.Request) {
	rawID := mux.Vars(r)["id"]
	if !validWorkshopPageID.MatchString(rawID) {
		a.serveNotFound(w, r)
		return
	}
	id := strings.ToUpper(rawID)
	path := "/mods/" + id + "/"
	if r.URL.Path != path {
		http.Redirect(w, r, path, http.StatusMovedPermanently)
		return
	}
	if a.redirectToCanonicalPublicHost(w, r, path) {
		return
	}
	page := publicPage{
		Path:        path,
		Slug:        "mods",
		Title:       "Arma Reforger Mod " + id + " | Workshop Mod Details",
		Description: "Details for Arma Reforger Workshop mod " + id + ": author, version, size, dependencies, scenarios, and a ready-to-copy config.json mods entry.",
		H1:          "Arma Reforger Mod " + id,
		Keywords:    []string{"Arma Reforger mod " + id, "Workshop mod details", "Arma Reforger mod dependencies", "config.json mod entry"},
		FullWidth:   true,
		Content:     modDetailContent(id),
		Scripts:     []string{"/static/tools/common.js", "/static/tools/mod-detail.js"},
	}
	a.renderPublicPage(w, r, page, false, http.StatusOK)
}

func modDetailContent(id string) htmltemplate.HTML {
	return htmlFragmentTemplate("tools/mod-detail.html", struct {
		ModID string
	}{ModID: id})
}

var modBrowserHTML = htmlFragment("tools/mod-browser.html")

var configValidatorHTML = htmlFragment("tools/config-validator.html")

var configGeneratorHTML = htmlFragment("tools/config-generator.html")

var modManagerHTML = htmlFragment("tools/mod-manager.html")

var apiQuickstartHTML = htmlFragment("tools/api-quickstart.html")
