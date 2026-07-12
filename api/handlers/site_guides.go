package handlers

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
		Path:        "/guides/deploy-first-arma-reforger-server/",
		Slug:        "guide-first-server",
		Title:       "How to Run and Deploy Your First Arma Reforger Server",
		Description: "Set up your first Arma Reforger dedicated server: install the server, create config.json, open the right ports, launch it, verify it appears online, and keep it running.",
		H1:          "How to Run and Deploy Your First Arma Reforger Server",
		Keywords:    []string{"Arma Reforger server setup", "deploy Arma Reforger server", "Arma Reforger dedicated server", "Arma Reforger server ports", "first Arma Reforger server"},
		ChangeFreq:  "monthly",
		Priority:    "0.7",
		FullWidth:   true,
		Content:     guideFirstServerHTML,
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

var guidesIndexHTML = htmlFragment("guides/index.html")

var guideConfigJSONHTML = htmlFragmentTemplate("guides/arma-reforger-config-json.html", siteFragmentData{OfficialServerConfigDocsURL: officialServerConfigDocsURL})

var guideFirstServerHTML = htmlFragmentTemplate("guides/deploy-first-arma-reforger-server.html", siteFragmentData{OfficialServerConfigDocsURL: officialServerConfigDocsURL})

var guideAddModsHTML = htmlFragmentTemplate("guides/how-to-add-mods.html", siteFragmentData{OfficialServerConfigDocsURL: officialServerConfigDocsURL})

var guideTroubleshootingHTML = htmlFragmentTemplate("guides/config-json-troubleshooting.html", siteFragmentData{OfficialServerConfigDocsURL: officialServerConfigDocsURL})

var guideServerModsHTML = htmlFragment("guides/arma-reforger-server-mods.html")

var guideAPIIntegrationHTML = htmlFragment("guides/api-integration.html")

var guide202HTML = htmlFragment("guides/handling-202-refresh-jobs.html")
