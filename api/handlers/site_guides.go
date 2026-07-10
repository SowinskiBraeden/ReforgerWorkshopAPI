package handlers

import (
	htmltemplate "html/template"
)

const officialServerConfigDocsURL = "https://community.bistudio.com/wiki/Arma_Reforger:Server_Config"

// guidePages are long-form, server-rendered guide pages. Content is written
// for this site; ranges and field names for the server config come from the
// official Bohemia Interactive server documentation, which every guide links.
var guidePages = []publicPage{
	{
		Path:        "/guides/",
		Slug:        "guides",
		Title:       "Arma Reforger Server and API Guides | Reforger Mods API",
		Description: "Practical guides for Arma Reforger server admins and developers: config.json reference, adding mods, troubleshooting startup failures, and integrating the Reforger Mods API.",
		H1:          "Guides",
		Keywords:    []string{"Arma Reforger guides", "Arma Reforger server guide", "config.json guide", "Reforger Mods API guide", "Arma Reforger modding server"},
		ChangeFreq:  "weekly",
		Priority:    "0.8",
		FullWidth:   true,
		Content:     guidesIndexHTML,
	},
	{
		Path:        "/guides/arma-reforger-config-json/",
		Slug:        "guide-config-json",
		Title:       "Arma Reforger config.json Explained | Server Config Guide",
		Description: "What every major field in an Arma Reforger server config.json does: network and ports, A2S, RCON, game settings, scenarioId, gameProperties, mods, and operating options.",
		H1:          "Arma Reforger config.json Explained",
		Keywords:    []string{"Arma Reforger config.json", "Arma Reforger server config", "gameProperties", "operating config", "scenarioId", "joinQueue"},
		ChangeFreq:  "monthly",
		Priority:    "0.7",
		FullWidth:   true,
		Content:     guideConfigJSONHTML,
	},
	{
		Path:        "/guides/how-to-add-mods/",
		Slug:        "guide-add-mods",
		Title:       "How to Add Mods to an Arma Reforger Server | Step by Step",
		Description: "Add Workshop mods to an Arma Reforger dedicated server: find the mod ID, write the game.mods entry in config.json, handle dependencies, and verify the server loads them.",
		H1:          "How to Add Mods to an Arma Reforger Server",
		Keywords:    []string{"add mods to Arma Reforger server", "Arma Reforger game.mods", "Workshop mod ID", "Arma Reforger mod dependencies", "config.json mods"},
		ChangeFreq:  "monthly",
		Priority:    "0.7",
		FullWidth:   true,
		Content:     guideAddModsHTML,
	},
	{
		Path:        "/guides/config-json-troubleshooting/",
		Slug:        "guide-config-troubleshooting",
		Title:       "Arma Reforger config.json Troubleshooting | Common Errors",
		Description: "Fix the most common Arma Reforger server config.json problems: JSON syntax errors, trailing commas, smart quotes, misplaced mods arrays, bad ports, and scenario ID mistakes.",
		H1:          "Arma Reforger config.json Troubleshooting",
		Keywords:    []string{"Arma Reforger config errors", "config.json troubleshooting", "Arma Reforger server startup", "scenarioId errors", "JSON syntax errors"},
		ChangeFreq:  "monthly",
		Priority:    "0.7",
		FullWidth:   true,
		Content:     guideTroubleshootingHTML,
	},
	{
		Path:        "/guides/arma-reforger-server-mods/",
		Slug:        "guide-server-mods",
		Title:       "Running Arma Reforger Server Mods | Dependencies and Updates",
		Description: "How mods behave on an Arma Reforger dedicated server: how downloads work, what dependencies mean for your mod list, version pinning, and keeping a modded server stable.",
		H1:          "Running Arma Reforger Server Mods",
		Keywords:    []string{"Arma Reforger server mods", "mod dependencies", "Workshop mod updates", "Arma Reforger version pinning", "modded server stability"},
		ChangeFreq:  "monthly",
		Priority:    "0.7",
		FullWidth:   true,
		Content:     guideServerModsHTML,
	},
	{
		Path:        "/guides/api-integration/",
		Slug:        "guide-api-integration",
		Title:       "Reforger Mods API Integration Guide | Build on Workshop Data",
		Description: "Integrate the Reforger Mods API into your app or server tool: endpoints, pagination, caching and ETags, client identity, rate limits, and robust error handling.",
		H1:          "Reforger Mods API Integration Guide",
		Keywords:    []string{"Reforger Mods API integration", "Arma Reforger Workshop API", "API pagination", "ETag caching", "API rate limits"},
		ChangeFreq:  "monthly",
		Priority:    "0.7",
		FullWidth:   true,
		Content:     guideAPIIntegrationHTML,
	},
	{
		Path:        "/guides/handling-202-refresh-jobs/",
		Slug:        "guide-202-refresh-jobs",
		Title:       "Handling 202 Accepted and Refresh Jobs | Reforger Mods API",
		Description: "How the Reforger Mods API cold-cache flow works: why you get 202 Accepted, how to use Retry-After, when to poll the refresh job URL, and retry patterns that will not get rate limited.",
		H1:          "Handling 202 Accepted and Refresh Jobs",
		Keywords:    []string{"202 Accepted", "Reforger Mods API refresh jobs", "Retry-After", "cold cache API", "Arma Reforger API retries"},
		ChangeFreq:  "monthly",
		Priority:    "0.7",
		FullWidth:   true,
		Content:     guide202HTML,
	},
}

const guidesIndexHTML = htmltemplate.HTML(`<h1>Guides</h1>
<p class="landing-lede">Practical, no-fluff guides for Arma Reforger server admins and for developers building on Workshop data. Every guide links to the tool that does the work for you.</p>
<h2>Server administration</h2>
<div class="landing-grid">
  <a href="/guides/arma-reforger-config-json/" class="landing-link-card"><i class="bi bi-filetype-json"></i><span>config.json explained</span><small>What every major field does: ports, A2S, RCON, game settings, scenarioId, gameProperties, and mods.</small></a>
  <a href="/guides/how-to-add-mods/" class="landing-link-card"><i class="bi bi-plus-square"></i><span>How to add mods</span><small>Find the mod ID, write the game.mods entry, handle dependencies, and verify the server loads it.</small></a>
  <a href="/guides/config-json-troubleshooting/" class="landing-link-card"><i class="bi bi-wrench-adjustable"></i><span>config.json troubleshooting</span><small>Trailing commas, smart quotes, misplaced mods arrays, bad ports, and scenario mistakes.</small></a>
  <a href="/guides/arma-reforger-server-mods/" class="landing-link-card"><i class="bi bi-collection"></i><span>Running server mods</span><small>How mod downloads work, what dependencies mean, version pinning, and staying stable.</small></a>
</div>
<h2>API and development</h2>
<div class="landing-grid">
  <a href="/guides/api-integration/" class="landing-link-card"><i class="bi bi-journal-code"></i><span>API integration</span><small>Endpoints, pagination, caching, ETags, client identity, and error handling for real apps.</small></a>
  <a href="/guides/handling-202-refresh-jobs/" class="landing-link-card"><i class="bi bi-arrow-repeat"></i><span>202 and refresh jobs</span><small>Why cold-cache requests return 202 Accepted and how to retry correctly.</small></a>
</div>
<h2>Do it with the tools instead</h2>
<p>Most of what these guides describe by hand can be done in the browser: search mods in the <a href="/arma-reforger-mods/">mod browser</a>, build a config in the <a href="/config-generator/">config generator</a>, manage the mods array in the <a href="/mod-manager/">mod manager</a>, and check the result with the <a href="/config-validator/">config validator</a>.</p>`)

const guideConfigJSONHTML = htmltemplate.HTML(`<nav class="tool-breadcrumb guide-breadcrumb" aria-label="Breadcrumb"><a href="/guides/">Guides</a> <span>/</span> <span>config.json Explained</span></nav>
<h1>Arma Reforger config.json Explained</h1>
<p class="landing-lede">An Arma Reforger dedicated server is configured by a single JSON file, usually called <code>config.json</code>, passed to the server with the <code>-config</code> startup parameter. This guide explains the major sections and the fields admins actually change. The authoritative field list is the <a href="` + officialServerConfigDocsURL + `" rel="noopener">official Bohemia Interactive server documentation</a>.</p>
<p>Prefer not to write it by hand? The <a href="/config-generator/">config generator</a> builds this file with a form, and the <a href="/config-validator/">config validator</a> checks an existing one.</p>
<h2>Minimal working shape</h2>
<pre><code class="language-json">{
  "bindAddress": "0.0.0.0",
  "bindPort": 2001,
  "publicAddress": "",
  "publicPort": 2001,
  "game": {
    "name": "My Reforger Server",
    "maxPlayers": 32,
    "scenarioId": "{ECC61978EDCC2B5A}Missions/23_Campaign.conf",
    "crossPlatform": true,
    "mods": []
  }
}</code></pre>
<h2>Network settings</h2>
<ul>
  <li><code>bindAddress</code> / <code>bindPort</code> - the address and UDP port the server process listens on. <code>0.0.0.0</code> listens on all interfaces; the conventional game port is <code>2001</code>.</li>
  <li><code>publicAddress</code> / <code>publicPort</code> - what gets advertised to players. Behind NAT or a proxy, set these to the externally reachable address and port. An empty <code>publicAddress</code> lets the backend detect it.</li>
  <li><code>a2s</code> - an object with <code>address</code> and <code>port</code> for Steam-style server queries, so hosting panels and server lists can query state.</li>
</ul>
<h2>RCON</h2>
<p>The optional <code>rcon</code> object enables remote administration. It needs an <code>address</code>, a <code>port</code>, and a <code>password</code>; the official docs also describe <code>permission</code> (admin or monitor), <code>maxClients</code>, and address allow and deny lists. Keep the password out of screenshots and pastebins: the <a href="/config-validator/">validator</a> never uploads your config, but other tools might.</p>
<h2>The game object</h2>
<ul>
  <li><code>name</code> - the server name shown in the browser list.</li>
  <li><code>password</code> - join password for players; <code>passwordAdmin</code> - password for in-game admin login.</li>
  <li><code>admins</code> - an array of player identifiers that get admin rights automatically; the accepted identifier formats are listed in the official docs.</li>
  <li><code>scenarioId</code> - required. The scenario the server runs, in the form <code>{GUID}Missions/File.conf</code>. Base-game scenario IDs are in the official docs; modded scenario IDs are shown on each mod page in the <a href="/arma-reforger-mods/">mod browser</a> when the Workshop lists them.</li>
  <li><code>maxPlayers</code> - player limit; the engine supports up to 128 slots.</li>
  <li><code>visible</code> - whether the server appears in the public server browser.</li>
  <li><code>crossPlatform</code> and <code>supportedPlatforms</code> - enable console players. Platform identifiers include <code>PLATFORM_PC</code>, <code>PLATFORM_XBL</code>, and <code>PLATFORM_PSN</code>. Note that mods that are not console-compatible restrict who can join.</li>
  <li><code>mods</code> - the Workshop mod list. Covered field by field in the <a href="/guides/how-to-add-mods/">adding mods guide</a>.</li>
  <li><code>modsRequiredByDefault</code> - whether listed mods are mandatory for joining clients unless a mod entry overrides it.</li>
</ul>
<h2>gameProperties</h2>
<p>Inside <code>game</code>, the <code>gameProperties</code> object holds tuning options. Frequently used ones:</p>
<ul>
  <li><code>serverMaxViewDistance</code> and <code>networkViewDistance</code> - view distance limits; higher values cost server performance.</li>
  <li><code>serverMinGrassDistance</code> - minimum grass render distance enforced on clients.</li>
  <li><code>enableAI</code> - enables AI systems for scenarios that use AI.</li>
  <li><code>disableThirdPerson</code> - force first person.</li>
  <li><code>fastValidation</code> - speeds up client joins and is recommended by the official docs for most servers.</li>
  <li><code>battlEye</code> - enable or disable BattlEye.</li>
  <li><code>VONDisableUI</code> - hides the general voice-over-network UI when enabled.</li>
  <li><code>VONDisableDirectSpeechUI</code> - hides the direct speech voice UI when enabled.</li>
  <li><code>VONCanTransmitCrossFaction</code> - allows voice transmission across factions when enabled.</li>
  <li><code>missionHeader</code> - scenario-specific overrides passed through to the mission. Leave it as an empty object unless the scenario documentation calls for specific keys.</li>
</ul>
<p>Documented value ranges exist for the view distance and grass settings; the <a href="/config-validator/">validator</a> flags values outside them.</p>
<h2>The operating object</h2>
<p>Top-level <code>operating</code> controls server-process behavior around lobby synchronization, persistence, AI limits, navigation streaming, and join queues. These settings are useful once a server is already working; they are not the first place to debug startup issues.</p>
<ul>
  <li><code>enableAI</code> controls AI processing at the operating layer. Keep it enabled for scenarios that rely on AI.</li>
  <li><code>lobbyPlayerSynchronise</code> controls whether players are synchronized while they are still in the lobby. Leave it enabled unless you are deliberately tuning lobby behavior.</li>
  <li><code>playerSaveTime</code> controls how often player state is saved, in seconds. Lower values save more often but add more regular work for the server.</li>
  <li><code>aiLimit</code> caps how many AI entities the server may run. Higher values can make scenarios feel busier, but they also raise CPU load.</li>
  <li><code>joinQueue</code> contains queue settings such as <code>maxSize</code>. This controls how many players can wait when the server is full instead of being rejected immediately.</li>
  <li>Navmesh streaming options belong here when you need to tune AI navigation behavior for large or custom scenarios. Leave them unset unless a scenario author or the official docs call for a specific value.</li>
</ul>
<p>The <a href="/config-generator/">config generator</a> includes conservative controls for the common operating fields and still preserves imported fields it does not expose in the form. Check the <a href="` + officialServerConfigDocsURL + `" rel="noopener">official Bohemia Interactive server documentation</a> before changing advanced operating values.</p>
<h2>Things JSON will not forgive</h2>
<p>JSON has no comments, no trailing commas, and only straight double quotes. If your server exits immediately at startup, run the file through the <a href="/config-validator/">config validator</a> first, then see the <a href="/guides/config-json-troubleshooting/">troubleshooting guide</a>.</p>`)

const guideAddModsHTML = htmltemplate.HTML(`<nav class="tool-breadcrumb guide-breadcrumb" aria-label="Breadcrumb"><a href="/guides/">Guides</a> <span>/</span> <span>How to Add Mods</span></nav>
<h1>How to Add Mods to an Arma Reforger Server</h1>
<p class="landing-lede">Mods are added to an Arma Reforger dedicated server by listing them in the <code>game.mods</code> array of <code>config.json</code>. The server downloads listed mods from the Workshop at startup, and joining players download them automatically. Here is the whole process, start to finish.</p>
<h2>1. Find the mod and its ID</h2>
<p>Every Workshop mod has a 16-character hexadecimal ID, for example <code>5965550F24A0C152</code>. Find mods and copy their IDs in the <a href="/arma-reforger-mods/">mod browser</a> - every result card has a copy button for the ID and for a ready-made JSON entry - or take the ID from the mod page URL on the official Workshop.</p>
<h2>2. Write the mods entry</h2>
<pre><code class="language-json">"game": {
  "mods": [
    {
      "modId": "5965550F24A0C152",
      "name": "WeaponSwitching"
    }
  ]
}</code></pre>
<ul>
  <li><code>modId</code> - required. The Workshop ID.</li>
  <li><code>name</code> - optional and informational; it keeps the file readable but does not affect what is downloaded.</li>
  <li><code>version</code> - optional. Pins a specific mod version instead of the latest. Leave it out unless you have a reason to pin.</li>
  <li><code>required</code> - optional. Together with <code>game.modsRequiredByDefault</code> it controls whether joining clients must run the mod; see the <a href="` + officialServerConfigDocsURL + `" rel="noopener">official documentation</a> for the exact semantics.</li>
</ul>
<p>The <code>mods</code> array belongs <em>inside</em> the <code>game</code> object. A mods array at the top level of the file is silently ignored - one of the most common mistakes in the <a href="/guides/config-json-troubleshooting/">troubleshooting guide</a>.</p>
<h2>3. Add dependencies</h2>
<p>Some mods require other mods. Check the dependencies section on the mod detail page in the <a href="/arma-reforger-mods/">mod browser</a> and make sure each dependency is either in your list or known to be pulled in automatically. The <a href="/mod-manager/">mod manager</a> has a dependency check that compares your list against Workshop data and suggests anything missing - it suggests rather than auto-adds, because public dependency data can lag behind mod updates.</p>
<h2>4. Restart and verify</h2>
<p>Restart the server after changing the mod list. On startup the server log shows each mod being resolved and downloaded; a mod that cannot be found or downloaded is logged with its ID. First startup after adding large mods takes longer because of the downloads.</p>
<h2>Skip the hand-editing</h2>
<p>The <a href="/config-generator/">config generator</a> manages all of this visually: add mods from the browser with one click, reorder and remove them, resolve current names, and export a valid <code>config.json</code>. Validate the final file with the <a href="/config-validator/">config validator</a> before deploying, and read the <a href="/guides/arma-reforger-server-mods/">server mods guide</a> for how updates and dependencies behave over time.</p>`)

const guideTroubleshootingHTML = htmltemplate.HTML(`<nav class="tool-breadcrumb guide-breadcrumb" aria-label="Breadcrumb"><a href="/guides/">Guides</a> <span>/</span> <span>config.json Troubleshooting</span></nav>
<h1>Arma Reforger config.json Troubleshooting</h1>
<p class="landing-lede">When an Arma Reforger server exits at startup or ignores your settings, the cause is usually one of a handful of config.json problems. Work through this list from the top - or paste the file into the <a href="/config-validator/">config validator</a>, which detects most of these automatically and locally in your browser.</p>
<h2>1. Invalid JSON</h2>
<p>The single most common failure. JSON is strict:</p>
<ul>
  <li><strong>Trailing commas</strong> after the last element of an object or array are not allowed.</li>
  <li><strong>Comments</strong> (<code>//</code> or <code>/* */</code>) are not part of JSON. Remove them.</li>
  <li><strong>Smart quotes</strong> - text pasted through chat apps or word processors often arrives with curly quotes instead of straight <code>"</code>. The server cannot parse them.</li>
  <li><strong>Missing commas or brackets</strong> - one missing <code>}</code> can produce an error message pointing at a completely different line.</li>
</ul>
<p>The <a href="/config-validator/">validator</a> reports the line and column of the first syntax error, which is almost always faster than eyeballing the file.</p>
<h2>2. Mods listed in the wrong place</h2>
<p>The <code>mods</code> array must be inside the <code>game</code> object. A top-level <code>mods</code> key parses fine and is then ignored, so the server starts vanilla with no error. If your server ignores its mod list, check the nesting first.</p>
<h2>3. Malformed or duplicate mod IDs</h2>
<p>Workshop mod IDs are 16 hexadecimal characters. Truncated IDs, IDs with spaces, or full URLs pasted into <code>modId</code> will not resolve. Duplicate entries for the same mod are at best confusing and at worst mask a version conflict. The <a href="/mod-manager/">mod manager</a> flags duplicates, and the validator can check each ID against Workshop data, sending only the IDs.</p>
<h2>4. Port problems</h2>
<ul>
  <li>Ports must be numbers, not strings, and within <code>1 to 65535</code>.</li>
  <li>The game port, A2S port, and RCON port must not collide with each other or with another process on the machine.</li>
  <li>If players cannot see or reach the server, confirm <code>publicAddress</code> and <code>publicPort</code> match your actual firewall and NAT forwarding (UDP).</li>
</ul>
<h2>5. Scenario ID mistakes</h2>
<p><code>game.scenarioId</code> must exactly match an available scenario, in the form <code>{GUID}Missions/File.conf</code>. Typos in the GUID, a missing <code>Missions/</code> segment, or a scenario from a mod that is not in your mods list all prevent startup. Modded scenario IDs are listed on mod detail pages in the <a href="/arma-reforger-mods/">mod browser</a> when the Workshop exposes them.</p>
<h2>6. RCON settings</h2>
<p>If RCON is configured, it needs a valid <code>port</code> and a <code>password</code>; the official docs require the password to be non-trivial and contain no spaces. Remove the whole <code>rcon</code> object if you do not use RCON.</p>
<h2>Still stuck?</h2>
<p>Rebuild the file section by section in the <a href="/config-generator/">config generator</a> - import your broken config first; unknown fields are preserved - and compare against the field explanations in the <a href="/guides/arma-reforger-config-json/">config.json guide</a> and the <a href="` + officialServerConfigDocsURL + `" rel="noopener">official server documentation</a>.</p>`)

const guideServerModsHTML = htmltemplate.HTML(`<nav class="tool-breadcrumb guide-breadcrumb" aria-label="Breadcrumb"><a href="/guides/">Guides</a> <span>/</span> <span>Running Server Mods</span></nav>
<h1>Running Arma Reforger Server Mods</h1>
<p class="landing-lede">A modded Arma Reforger server is easy to start and easy to break. This guide covers how mod loading actually behaves - downloads, dependencies, versions, and updates - so your mod list stays stable over time.</p>
<h2>How mod loading works</h2>
<p>At startup, the server reads <code>game.mods</code> from <code>config.json</code>, downloads any listed Workshop mods it does not have locally, and loads them. Players joining the server automatically download the mods the server marks as required. You do not copy mod files around by hand; the mod list in the config is the source of truth. The mechanics of writing that list are in the <a href="/guides/how-to-add-mods/">adding mods guide</a>.</p>
<h2>Dependencies</h2>
<p>Mods can depend on other mods - a weapon pack depending on a shared framework, for example. The dependency information shown on Workshop pages (and surfaced on mod detail pages in the <a href="/arma-reforger-mods/">mod browser</a>) tells you what else a mod needs. Keeping dependencies explicit in your own list makes load order and updates predictable and makes it obvious why each mod is installed. The <a href="/mod-manager/">mod manager</a> compares your list against Workshop dependency data and suggests anything missing.</p>
<h2>Versions and pinning</h2>
<p>By default the server uses the latest published version of each mod. A mod entry may pin a <code>version</code> instead. Pinning trades freshness for stability: you are immune to a broken update, but you must remember to unpin or bump versions deliberately. Pin only the mods that have burned you before, and record why.</p>
<h2>When mods update</h2>
<ul>
  <li>A mod update can change or remove content that your scenario or other mods rely on. After a big update, test on a restart before peak hours.</li>
  <li>If a modded scenario is your <code>scenarioId</code>, a mod update can rename or move the scenario file, which stops the server from starting. Check the scenario list on the mod detail page after updates.</li>
  <li>Console compatibility can change: a mod that becomes PC-only affects who can join a cross-platform server.</li>
</ul>
<h2>Keeping the list healthy</h2>
<ul>
  <li>Keep <code>name</code> fields on entries so humans can read the list; names are informational only.</li>
  <li>Remove mods you no longer run. Dead weight slows downloads and widens the update surface.</li>
  <li>Watch for duplicates after merging lists from different sources - the <a href="/config-validator/">validator</a> and <a href="/mod-manager/">mod manager</a> both flag them.</li>
  <li>Keep a known-good copy of <code>config.json</code> so you can roll back a bad mod update quickly.</li>
</ul>
<p>Config-file details live in the <a href="/guides/arma-reforger-config-json/">config.json guide</a>; startup failures are covered in the <a href="/guides/config-json-troubleshooting/">troubleshooting guide</a>.</p>`)

const guideAPIIntegrationHTML = htmltemplate.HTML(`<nav class="tool-breadcrumb guide-breadcrumb" aria-label="Breadcrumb"><a href="/guides/">Guides</a> <span>/</span> <span>API Integration</span></nav>
<h1>Reforger Mods API Integration Guide</h1>
<p class="landing-lede">How to build a real integration on the Reforger Mods API: choosing endpoints, paginating, caching, identifying your client, and handling every response you will actually see in production. For a five-minute version, start with the <a href="/api/">quickstart</a>.</p>
<h2>Endpoints in one look</h2>
<pre><code>GET /v1/health                      process health, no Workshop data
GET /v1/mods                        first page of mod previews
GET /v1/mods/{page}                 specific page (positive integer)
GET /v1/search?search={query}       convenience alias for first-page search
GET /v1/mod/{mod_id}                full detail for one mod
GET /v1/refresh/jobs/{id}           status of a background refresh job</code></pre>
<p>List endpoints accept <code>search</code>, <code>sort</code> (<code>popularity</code>, <code>newest</code>, <code>subscribers</code>, <code>version_size</code>), and Workshop tag filters through <code>tags</code> or the single-value <code>category</code> alias. Response shapes are documented in the <a href="/docs/mod-structures/">mod structures reference</a>, and the <a href="/arma-reforger-mods-api/">API reference</a> covers every header and error code.</p>
<h2>Pagination</h2>
<p>List responses include a <code>meta</code> object (<code>totalPages</code>, <code>currentPage</code>, <code>totalMods</code>) and a <code>links</code> object with ready-built <code>next</code> and <code>prev</code> URLs. Follow the links instead of constructing URLs yourself and your client will keep working if parameters evolve.</p>
<h2>Identify your client</h2>
<p>Send a <code>User-Agent</code> or <code>X-API-Client</code> header that names your project and gives a way to reach you:</p>
<pre><code>User-Agent: my-server-panel/2.1 (+https://example.com; admin@example.com)</code></pre>
<p>Identified traffic is easier to support and will never be mistaken for an abusive scraper.</p>
<h2>Cache like you mean it</h2>
<ul>
  <li>Responses carry <code>Cache-Control</code>, <code>Age</code>, and <code>ETag</code>. Honor them: cache list responses for at least their <code>max-age</code>.</li>
  <li>Send <code>If-None-Match</code> with the last <code>ETag</code>; a <code>304 Not Modified</code> costs almost nothing on both sides.</li>
  <li><code>X-Cache</code> tells you whether you got a <code>HIT</code>, <code>STALE</code>, or fresh data. Stale responses are intentional - the API serves them while refreshing in the background so your app never blocks on the upstream Workshop.</li>
  <li>Do not vary query strings just to bust caches; it wastes your rate budget and everyone elses refresh capacity.</li>
</ul>
<h2>Handle every status</h2>
<ul>
  <li><code>200</code> - use the data.</li>
  <li><code>202</code> - cold cache; a refresh job was queued. Wait <code>Retry-After</code> seconds and retry the same URL, a bounded number of times. Full pattern with code in the <a href="/guides/handling-202-refresh-jobs/">202 guide</a>.</li>
  <li><code>304</code> - your cached copy is still valid.</li>
  <li><code>404</code> - the mod or page does not exist; cache the negative result briefly rather than re-asking.</li>
  <li><code>429</code> - you exceeded the rate limit (default 60 requests per minute per IP). Back off for <code>Retry-After</code>.</li>
  <li><code>5xx</code> - treat as temporary; retry with backoff and surface a friendly error, not a crash.</li>
</ul>
<p>All errors share one JSON shape with a <code>code</code>, a <code>message</code>, and the <code>requestId</code> echoed from <code>X-Request-Id</code> - log that ID and include it when reporting problems.</p>
<h2>Worked example</h2>
<p>The <a href="/api/">quickstart</a> has a copy-paste Python function that implements retry-on-202 with a bounded attempt count. The web tools on this site - the <a href="/arma-reforger-mods/">mod browser</a> and the <a href="/config-validator/">validator</a> mod check - use the same pattern, so you can watch the behavior in your network tab.</p>`)

const guide202HTML = htmltemplate.HTML(`<nav class="tool-breadcrumb guide-breadcrumb" aria-label="Breadcrumb"><a href="/guides/">Guides</a> <span>/</span> <span>202 and Refresh Jobs</span></nav>
<h1>Handling 202 Accepted and Refresh Jobs</h1>
<p class="landing-lede">The Reforger Mods API never makes your request wait on a slow upstream Workshop page. If the data you asked for is not cached yet, you get <code>202 Accepted</code> and the API fetches it in the background. Handling this well takes about ten lines of code.</p>
<h2>The flow</h2>
<pre><code>GET /v1/mods?search=radio

HTTP/1.1 202 Accepted
Location: /v1/refresh/jobs/9f0b7d0f6fd4f88a
Retry-After: 2
X-Cache: MISS

{"id":"9f0b7d0f6fd4f88a","status":"queued","resource_url":"/v1/mods?search=radio","retry_after_seconds":2}</code></pre>
<p>If a cold-cache request returns <code>202 Accepted</code>, the API has accepted a background refresh job. Wait the number of seconds in <code>Retry-After</code>, then retry the same URL. You can also inspect the <code>Location</code> job URL, but most clients only need to retry the original request.</p>
<h2>The rules</h2>
<ul>
  <li><strong>202 is not an error.</strong> Do not log it as a failure and do not give up on the first one.</li>
  <li><strong>Respect Retry-After.</strong> Retrying faster does not speed up the refresh; it only spends your rate limit.</li>
  <li><strong>Bound your retries.</strong> Three to five attempts is plenty. If data is still not ready, show a "still refreshing" state and let the user retry later.</li>
  <li><strong>Retry the original URL</strong>, not the job URL. The job endpoint reports status only; it never contains the mod data.</li>
</ul>
<h2>Minimal implementation</h2>
<pre><code class="language-python">import time
import requests

def fetch_with_refresh(url, max_attempts=4):
    headers = {"User-Agent": "my-tool/1.0 (contact@example.com)"}
    for _ in range(max_attempts):
        response = requests.get(url, headers=headers, timeout=15)
        if response.status_code != 202:
            response.raise_for_status()
            return response.json()
        wait = int(response.headers.get("Retry-After", "2"))
        time.sleep(wait)
    raise RuntimeError("Still refreshing after retry limit")

mods = fetch_with_refresh("https://api.reforgermods.net/v1/mods?search=radio")</code></pre>
<h2>When to poll the job URL instead</h2>
<p>Polling <code>Location</code> is useful only when you want to display progress - a dashboard showing "queued, running, succeeded". Job statuses are <code>queued</code>, <code>running</code>, <code>succeeded</code>, <code>failed</code>, and <code>expired</code>. After <code>succeeded</code>, request the original <code>resource_url</code> again; after <code>failed</code>, back off and try the original URL later. Do not poll faster than the advertised <code>retry_after_seconds</code>.</p>
<h2>Why the API works this way</h2>
<p>Workshop pages can be slow or briefly unavailable. By decoupling your request from the upstream fetch, the API can answer instantly from cache, serve slightly stale data while revalidating, and shield the Workshop from request storms. The trade-off is this small retry dance on the very first request for a resource. Details on cache lifetimes are in the <a href="/arma-reforger-mods-api/">API reference</a>; the broader integration picture is in the <a href="/guides/api-integration/">integration guide</a>.</p>`)
