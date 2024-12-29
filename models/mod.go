package models

// As seen on front page of https://reforger.armaplatform.com/workshop
type ModPreview struct {
	Name     string `json:"name"`
	Author   string `json:"author"`
	ImageURL string `json:"image_url"`
	ModURL   string `json:"mod_url"`
	Size     string `json:"size"`
	Rating   string `json:"rating"`
}

// As seen when viewing single mod at https://reforger.armaplatform.com/workshop/<mod-id>-<mod-name>
type Mod struct {
	Name         string   `json:"name"`
	Author       string   `json:"author"`
	ModURL       string   `json:"mod_url"`
	ImageURL     string   `json:"image_url"`
	Rating       string   `json:"rating"`
	Version      string   `json:"version"`
	GameVersion  string   `json:"game_version"`
	Size         string   `json:"size"`
	Subscribers  int      `json:"subscribers"`
	Downloads    int      `json:"downloads"`
	Created      string   `json:"created"`
	LastModified string   `json:"last_modified"`
	ID           string   `json:"id"`
	Summary      string   `json:"summary"`
	Description  string   `json:"description"`
	License      string   `json:"license"`
	Tags         []string `json:"tags"`
}

// Sruct returned from utils ScapeMods
type WebScrapeResults struct {
	Found          bool
	Mods           []ModPreview
	CurrentPage    int
	TotalPages     int
	TotalMods      int
	ShownMods      int
	ModsIndexStart int
	ModsIndexEnd   int
}
