package handlers

import (
	"fmt"
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
		Path:        "/arma-reforger-mods/",
		Slug:        "mods",
		Title:       "Arma Reforger Mods | Workshop Mod Browser",
		Description: "Search Arma Reforger Workshop mods, view details and dependencies, copy mod IDs, and add mods to a server config.json with a free mod browser.",
		H1:          "Arma Reforger Mod Browser",
		Keywords:    []string{"Arma Reforger mods", "Arma Reforger Workshop browser", "Reforger mod search", "Workshop mod dependencies", "Arma Reforger mod IDs", "config.json mods"},
		ChangeFreq:  "daily",
		Priority:    "0.9",
		ToolName:    "Arma Reforger Mod Browser",
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
		Title:       "Arma Reforger config.json Validator | Reforger Mods API",
		Description: "Validate an Arma Reforger server config.json in your browser. Catch JSON syntax errors, invalid ports, duplicate mod IDs, and malformed mods entries before starting your server.",
		H1:          "Arma Reforger config.json Validator",
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
		Title:       "Arma Reforger Config Generator | Server config.json Builder",
		Description: "Build an Arma Reforger server config.json with a form-based editor, a managed mods list, live JSON preview, validation, and export. Free and browser-based.",
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
		Title:       "Arma Reforger Mod Manager | Edit the config.json Mods List",
		Description: "Manage the mods array of an Arma Reforger server config.json. Add mods by ID, resolve names from the Workshop, reorder and remove entries, and export the result.",
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
		Title:       "Reforger Mods API Quickstart | Arma Reforger Workshop Data API",
		Description: "Get started with the Reforger Mods API in minutes. Base URL, mod search and detail endpoints, 202 Accepted handling, and copy-paste examples in curl and Python.",
		H1:          "Reforger Mods API Quickstart",
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
	path := "/arma-reforger-mods/" + id + "/"
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
		Title:       "Arma Reforger Mod " + id + " | Reforger Mods API",
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
	// id is validated as 16 hex characters above, so it is safe to embed.
	return htmltemplate.HTML(fmt.Sprintf(`<nav class="tool-breadcrumb" aria-label="Breadcrumb"><a href="/arma-reforger-mods/">Mod Browser</a> <span>/</span> <span>%[1]s</span></nav>
<h1>Arma Reforger Mod %[1]s</h1>
<div id="mod-detail" data-mod-id="%[1]s">
  <div id="md-status" class="tool-status" role="status"></div>
  <div id="md-content"></div>
  <noscript><p>This page loads mod details from the public API in your browser and needs JavaScript. Without it, you can fetch the same data directly as JSON:</p><pre><code>curl https://api.reforgermods.net/v1/mod/%[1]s</code></pre></noscript>
</div>
<h2>Use this mod on your server</h2>
<p>Add the mod to the <code>game.mods</code> array of your server <code>config.json</code>. The <a href="/config-generator/">config generator</a> can build the full file, and the <a href="/config-validator/">config validator</a> checks an existing one. The <a href="/guides/how-to-add-mods/">adding mods guide</a> walks through the process step by step.</p>
<h2>Fetch this mod from the API</h2>
<p>Developers can request the same metadata as JSON. See the <a href="/api/">API quickstart</a> and the <a href="/arma-reforger-mods-api/">full API reference</a>.</p>
<pre><code>GET https://api.reforgermods.net/v1/mod/%[1]s</code></pre>
<p class="landing-note">Data is cached from the public Arma Reforger Workshop and may be stale. This site is independent and unofficial.</p>`, id))
}

const modBrowserHTML = htmltemplate.HTML(`<h1>Arma Reforger Mod Browser</h1>
<p class="landing-lede">Search Arma Reforger Workshop mods, check details and dependencies, copy mod IDs, and send mods straight into a server config.json. Powered by the public <a href="/api/">Reforger Mods API</a>, so everything you see here is also available as JSON.</p>
<div class="tool-panel" id="mod-browser">
  <form id="mb-form" class="tool-toolbar" role="search">
    <input id="mb-search" class="form-control" type="search" placeholder="Search Workshop mods by name..." aria-label="Search mods" autocomplete="off" maxlength="120">
    <select id="mb-category" class="form-select" aria-label="Filter by category">
      <option value="">All categories</option>
      <option value="MISC">Miscellaneous</option>
      <option value="GAMEPLAY">Gameplay</option>
      <option value="WEAPON">Weapons</option>
      <option value="VEHICLE">Vehicles</option>
      <option value="TERRAIN">Maps / terrain</option>
      <option value="EQUIPMENT">Equipment</option>
      <option value="FACTION">Factions</option>
      <option value="SCENARIO">Scenarios</option>
      <option value="AI">AI</option>
      <option value="AUDIO">Audio</option>
    </select>
    <select id="mb-sort" class="form-select" aria-label="Sort order">
      <option value="">Workshop default</option>
      <option value="popularity">Most popular</option>
      <option value="newest">Newest</option>
      <option value="subscribers">Most subscribers</option>
      <option value="version_size">Version size</option>
    </select>
    <div class="btn-group mb-view-toggle" role="group" aria-label="Result view">
      <button id="mb-view-card" class="btn btn-outline-secondary active" type="button" data-view="card" aria-pressed="true" title="Card view"><i class="bi bi-grid-3x3-gap"></i></button>
      <button id="mb-view-list" class="btn btn-outline-secondary" type="button" data-view="list" aria-pressed="false" title="List view"><i class="bi bi-list-ul"></i></button>
    </div>
    <button class="btn btn-primary" type="submit"><i class="bi bi-search"></i> Search</button>
  </form>
  <div id="mb-status" class="tool-status" role="status"></div>
  <div id="mb-results" class="mod-grid"></div>
  <nav id="mb-pagination" class="tool-pagination" aria-label="Result pages"></nav>
  <noscript><p>The interactive mod browser needs JavaScript. The same data is available as plain JSON from the public API:</p><pre><code>curl https://api.reforgermods.net/v1/mods?search=radio</code></pre><p>See the <a href="/arma-reforger-mods-api/">API reference</a> for all endpoints.</p></noscript>
</div>
<h2>What you can do here</h2>
<ul>
  <li>Search Arma Reforger Workshop mods by name or exact Workshop ID, filter by category, and sort by popularity, newest, subscribers, or version size.</li>
  <li>Copy a mod ID, or copy a ready-made <code>config.json</code> mods entry, from any result card.</li>
  <li>Send a mod into the <a href="/config-generator/">config builder</a> with one click; your working mod list lives in the <a href="/mod-manager/">mod manager</a>.</li>
  <li>Open a mod detail page for dependencies, scenarios, versions, and the official Workshop link.</li>
</ul>
<h2>Where the data comes from</h2>
<p>Results are cached metadata from public Arma Reforger Workshop pages, normalized by the <a href="/api/">Reforger Mods API</a>. Lists refresh every few minutes; a first-time search can briefly show a loading state while fresh Workshop data is fetched in the background. The <a href="/docs/methodology/">methodology page</a> explains freshness and limitations, and the <a href="/docs/mod-structures/">mod structures reference</a> documents the underlying JSON.</p>`)

const configValidatorHTML = htmltemplate.HTML(`<h1>Arma Reforger config.json Validator</h1>
<p class="landing-lede">Paste or upload an Arma Reforger server <code>config.json</code> and catch problems before your server refuses to start: JSON syntax errors with line numbers, invalid ports, missing required fields, and duplicate or malformed mod IDs.</p>
<p class="tool-privacy-note"><i class="bi bi-shield-lock"></i> Validation runs entirely in your browser; your config is never uploaded. The optional Workshop check sends only mod IDs to the API.</p>
<div class="tool-panel" id="config-validator">
  <div class="tool-toolbar">
    <label class="btn btn-outline-secondary mb-0" for="cv-file"><i class="bi bi-upload"></i> Upload .json</label>
    <input id="cv-file" type="file" accept=".json,application/json" class="d-none">
    <button id="cv-example" class="btn btn-outline-secondary" type="button">Load example</button>
    <button id="cv-format" class="btn btn-outline-secondary" type="button" title="Pretty-print the JSON"><i class="bi bi-text-indent-left"></i> Format</button>
    <button id="cv-clear" class="btn btn-outline-secondary" type="button">Clear</button>
    <button id="cv-validate" class="btn btn-primary ms-auto" type="button"><i class="bi bi-check2-square"></i> Validate</button>
  </div>
  <div class="cv-layout">
    <textarea id="cv-input" class="form-control tool-code-input" rows="18" spellcheck="false" placeholder="Paste your server config.json here..." aria-label="Server config JSON"></textarea>
    <div>
      <div id="cv-summary" class="tool-summary" aria-live="polite"></div>
      <div id="cv-results" class="tool-results"></div>
      <div id="cv-mods" class="tool-results"></div>
    </div>
  </div>
  <noscript><p>The validator runs locally in your browser and needs JavaScript. For the config format itself, see the <a href="/guides/arma-reforger-config-json/">config.json guide</a>.</p></noscript>
</div>
<h2>What gets validated</h2>
<ul>
  <li><strong>JSON syntax</strong> with the line and column of the first error.</li>
  <li><strong>Structure</strong>: the config must be a JSON object, with objects and arrays where the server expects them.</li>
  <li><strong>Network settings</strong>: <code>bindPort</code>, <code>publicPort</code>, and the A2S and RCON ports must be valid port numbers.</li>
  <li><strong>Game settings</strong>: a <code>game</code> object with a <code>scenarioId</code>, sane <code>maxPlayers</code>, and correctly typed fields.</li>
  <li><strong>Mods</strong>: <code>game.mods</code> must be an array of objects with string <code>modId</code> values; duplicates and malformed IDs are flagged.</li>
  <li><strong>Workshop check</strong> (optional): resolve each mod ID against Workshop data to confirm it exists and see its current name.</li>
</ul>
<p>Checks are deliberately conservative: the validator flags what is publicly documented and clearly wrong, and stays quiet about fields it cannot verify, rather than inventing rules.</p>
<h2>Fixing common problems</h2>
<p>The <a href="/guides/config-json-troubleshooting/">config.json troubleshooting guide</a> covers the usual startup failures: trailing commas, wrong quotes from chat apps and word processors, mods placed at the top level instead of inside <code>game</code>, and scenario IDs that do not match any installed content. To rebuild a broken config from scratch, use the <a href="/config-generator/">config generator</a>, and manage the mods array in the <a href="/mod-manager/">mod manager</a>.</p>`)

const configGeneratorHTML = htmltemplate.HTML(`<h1>Arma Reforger Server Config Generator</h1>
<p class="landing-lede">Create or edit an Arma Reforger server <code>config.json</code> with a form-based editor: network and RCON settings, game options, a managed mods list, live JSON preview, and validation as you type. Export or copy the finished file when you are done.</p>
<p class="tool-privacy-note"><i class="bi bi-shield-lock"></i> Your config is edited and stored in your browser only. Importing an existing file preserves fields the form does not know about.</p>
<div class="tool-panel" id="config-generator">
  <div class="tool-toolbar">
    <button id="cg-blank" class="btn btn-outline-secondary" type="button">Start blank</button>
    <button id="cg-example" class="btn btn-outline-secondary" type="button">Load example</button>
    <label class="btn btn-outline-secondary mb-0" for="cg-file"><i class="bi bi-upload"></i> Import .json</label>
    <input id="cg-file" type="file" accept=".json,application/json" class="d-none">
    <button id="cg-import-toggle" class="btn btn-outline-secondary" type="button">Import JSON</button>
    <button id="cg-json-edit-toggle" class="btn btn-outline-warning ms-auto" type="button"><i class="bi bi-code-slash"></i> Edit JSON directly</button>
  </div>
  <div id="cg-import" class="d-none">
    <textarea id="cg-import-text" class="form-control tool-code-input" rows="8" spellcheck="false" placeholder="Paste an existing config.json to import..."></textarea>
    <div class="tool-toolbar"><button id="cg-import-apply" class="btn btn-primary" type="button">Import</button><button id="cg-import-cancel" class="btn btn-outline-secondary" type="button">Cancel</button></div>
    <div id="cg-import-error" class="tool-status"></div>
  </div>
  <div id="cg-json-edit" class="json-edit-panel d-none">
    <div class="finding finding-warning json-edit-warning"><i class="bi bi-exclamation-triangle"></i><span>Direct JSON editing bypasses the form controls. Apply only after validating the syntax and structure.</span></div>
    <textarea id="cg-json-edit-text" class="form-control tool-code-input" rows="18" spellcheck="false" aria-label="Direct JSON editor"></textarea>
    <div class="tool-toolbar mt-2">
      <button id="cg-json-edit-apply" class="btn btn-primary" type="button"><i class="bi bi-check2"></i> Apply JSON</button>
      <button id="cg-json-edit-format" class="btn btn-outline-secondary" type="button"><i class="bi bi-text-indent-left"></i> Format</button>
      <button id="cg-json-edit-cancel" class="btn btn-outline-secondary" type="button">Cancel</button>
    </div>
    <div id="cg-json-edit-error" class="tool-status"></div>
  </div>
  <div class="row g-4 cg-layout">
    <div class="col-xl-6">
      <div id="cg-tabs" class="cg-tabs" role="tablist" aria-label="Config sections"></div>
      <div id="cg-form"></div>
      <section id="cg-mod-section" class="cg-section-panel d-none">
        <h2 class="tool-section-title">Mods</h2>
        <p class="cg-field-help mb-2">Search the Workshop by name, paste a mod ID, or send mods over from the <a href="/arma-reforger-mods/">mod browser</a>.</p>
        <div id="cg-mod-search"></div>
        <div id="cg-mods"></div>
      </section>
      <section id="cg-startup-section" class="cg-section-panel d-none">
        <h2 class="tool-section-title">Startup command</h2>
        <p class="cg-field-help mb-3">Build the command used to launch the dedicated server. These parameters are separate from <code>config.json</code>; copy them into your startup script or host panel.</p>
        <div id="cg-startup-form"></div>
        <div class="mt-3">
          <label class="form-label" for="cg-startup-command">Generated command line</label>
          <pre class="startup-command"><code id="cg-startup-command"></code></pre>
        </div>
        <div class="tool-toolbar mt-2">
          <button id="cg-copy-startup" class="btn btn-primary" type="button"><i class="bi bi-clipboard"></i> Copy command</button>
          <button id="cg-reset-startup" class="btn btn-outline-secondary" type="button"><i class="bi bi-arrow-counterclockwise"></i> Reset startup params</button>
        </div>
      </section>
    </div>
    <div class="col-xl-6">
      <div class="cg-preview-sticky">
        <div class="tool-toolbar">
          <button id="cg-copy" class="btn btn-primary" type="button"><i class="bi bi-clipboard"></i> Copy JSON</button>
          <button id="cg-download" class="btn btn-outline-secondary" type="button"><i class="bi bi-download"></i> Download config.json</button>
          <button id="cg-copy-mods" class="btn btn-outline-secondary" type="button">Copy mods array</button>
        </div>
        <div id="cg-validation" class="tool-results" aria-live="polite"></div>
        <pre class="tool-preview"><code id="cg-preview"></code></pre>
      </div>
    </div>
  </div>
  <noscript><p>The config generator needs JavaScript. The <a href="/guides/arma-reforger-config-json/">config.json guide</a> documents the file format if you prefer to write it by hand.</p></noscript>
</div>
<h2>How it works</h2>
<ul>
  <li>Start from a blank config, load a minimal working example, or import your existing <code>config.json</code>.</li>
  <li>Edit common fields through the form: bind and public address and port, A2S, RCON, server name, passwords, scenario, player limits, platform settings, and frequently used game properties.</li>
  <li>Manage <code>game.mods</code> visually: search Workshop mods by name without leaving the editor, paste a mod ID, or send mods over from the <a href="/arma-reforger-mods/">mod browser</a>. Resolve names and versions, reorder, remove, and get warned about duplicates.</li>
  <li>Unknown fields from an imported config are preserved exactly; the generator only changes what you edit.</li>
  <li>The preview is always the real output: copy it, or download it as <code>config.json</code>.</li>
</ul>
<p>Before deploying, run the result through the <a href="/config-validator/">config validator</a>, and read the <a href="/guides/arma-reforger-config-json/">config.json field guide</a> for what each setting does.</p>`)

const modManagerHTML = htmltemplate.HTML(`<h1>Arma Reforger Mod Manager</h1>
<p class="landing-lede">A focused editor for the <code>game.mods</code> array of your Arma Reforger server <code>config.json</code>. Search Workshop mods by name and add them directly, paste a mod ID, or collect mods from the <a href="/arma-reforger-mods/">mod browser</a>. Resolve current names and versions, reorder the list, check dependencies, and export the result.</p>
<div class="tool-panel" id="mod-manager">
  <div id="mm-search"></div>
  <div id="mm-status" class="tool-status" role="status"></div>
  <div id="mm-list"></div>
  <div class="tool-toolbar">
    <button id="mm-resolve" class="btn btn-outline-secondary" type="button"><i class="bi bi-arrow-repeat"></i> Resolve all mods</button>
    <button id="mm-deps" class="btn btn-outline-secondary" type="button"><i class="bi bi-diagram-3"></i> Check dependencies</button>
    <button id="mm-copy-mods" class="btn btn-primary" type="button"><i class="bi bi-clipboard"></i> Copy mods array</button>
    <button id="mm-download" class="btn btn-outline-secondary" type="button"><i class="bi bi-download"></i> Download config.json</button>
    <a href="/config-generator/" class="btn btn-outline-secondary"><i class="bi bi-sliders"></i> Open in Config Builder</a>
  </div>
  <div id="mm-deps-results" class="tool-results" aria-live="polite"></div>
  <noscript><p>The mod manager needs JavaScript. The <a href="/guides/how-to-add-mods/">adding mods guide</a> explains how to edit the mods array by hand.</p></noscript>
</div>
<h2>Working with the mods array</h2>
<p>Each entry in <code>game.mods</code> needs at least a <code>modId</code>. A <code>name</code> is optional but keeps the file readable, and a <code>version</code> pins the mod instead of using the latest release. The <a href="/guides/how-to-add-mods/">adding mods guide</a> covers the format in detail, and the <a href="/guides/arma-reforger-server-mods/">server mods guide</a> explains dependencies and update behavior.</p>
<p>This list is the same working config used by the <a href="/config-generator/">config generator</a>, so you can switch between the focused mods view and the full config editor at any time. When the list is final, validate the whole file with the <a href="/config-validator/">config validator</a>.</p>`)

const apiQuickstartHTML = htmltemplate.HTML(`<h1>Reforger Mods API Quickstart</h1>
<p class="landing-lede">A public, read-only JSON API for Arma Reforger Workshop mod metadata: search and list mods, fetch mod details with dependencies and scenarios, and build tools without scraping Workshop pages yourself. Unofficial, cached, and free to use within the rate limits.</p>
<h2>Base URL</h2>
<pre><code>https://api.reforgermods.net/v1</code></pre>
<h2>First request</h2>
<pre><code class="language-bash">curl 'https://api.reforgermods.net/v1/mods?search=radio&amp;sort=newest'</code></pre>
<p>List and search responses return mod previews with names, authors, IDs, sizes, ratings, and links. Fetch full details, including dependencies and scenarios, for one mod:</p>
<pre><code class="language-bash">curl 'https://api.reforgermods.net/v1/mod/5965550F24A0C152'</code></pre>
<h2>Handle 202 Accepted</h2>
<p>If a cold-cache request returns <code>202 Accepted</code>, the API has accepted a background refresh job. Wait the number of seconds in <code>Retry-After</code>, then retry the same URL. You can also inspect the <code>Location</code> job URL, but most clients only need to retry the original request.</p>
<pre><code class="language-python">import time
import requests

def get_json(url, attempts=4):
    headers = {"User-Agent": "my-tool/1.0 (contact@example.com)"}
    for _ in range(attempts):
        response = requests.get(url, headers=headers, timeout=15)
        if response.status_code == 202:
            time.sleep(int(response.headers.get("Retry-After", "2")))
            continue
        response.raise_for_status()
        return response.json()
    raise RuntimeError("Workshop data is still refreshing")

mods = get_json("https://api.reforgermods.net/v1/mods?search=radio")</code></pre>
<h2>Be a good client</h2>
<ul>
  <li>Send an identifying <code>User-Agent</code> or <code>X-API-Client</code> header with a contact hint, so traffic can be attributed and never mistaken for abuse.</li>
  <li>Respect <code>Retry-After</code> on <code>202</code>, <code>429</code>, and <code>503</code> responses, and cap your retries.</li>
  <li>Anonymous clients get 60 requests per minute per IP by default; batch and cache instead of polling.</li>
  <li>Use the <code>ETag</code> and <code>Cache-Control</code> headers: send <code>If-None-Match</code> to get cheap <code>304 Not Modified</code> responses for unchanged data.</li>
</ul>
<h2>Go deeper</h2>
<div class="landing-grid">
  <a href="/arma-reforger-mods-api/" class="landing-link-card"><i class="bi bi-braces"></i><span>Full API reference</span><small>Every endpoint, parameter, cache header, and error code.</small></a>
  <a href="/guides/api-integration/" class="landing-link-card"><i class="bi bi-journal-code"></i><span>Integration guide</span><small>A complete walkthrough for building on the API.</small></a>
  <a href="/guides/handling-202-refresh-jobs/" class="landing-link-card"><i class="bi bi-arrow-repeat"></i><span>202 and refresh jobs</span><small>How the cold-cache flow works and how to handle it well.</small></a>
  <a href="/docs/mod-structures/" class="landing-link-card"><i class="bi bi-diagram-3"></i><span>Mod structures</span><small>The JSON shapes for previews, details, dependencies, and scenarios.</small></a>
</div>
<p class="landing-note">The API also powers the <a href="/arma-reforger-mods/">mod browser</a>, <a href="/config-generator/">config generator</a>, and <a href="/config-validator/">config validator</a> on this site, so tool behavior and API behavior always match.</p>`)
