package models

// As seen on front page of https://reforger.armaplatform.com/workshop
type ModPreview struct {
	Name           string `json:"name"`
	Author         string `json:"author"`
	ImageURL       string `json:"imageURL"`
	OriginalModURL string `json:"originalModURL"`
	APIModURL      string `jons:"apiModURL"`
	Size           string `json:"size"`
	Rating         string `json:"rating"`
	ID             string `json:"ID"`
}

type Dependency struct {
	Name           string `json:"name"`
	OriginalModURL string `json:"originalModURL"`
	APIModURL      string `json:"apiModURL"`
}

type Scenario struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ScenarioID  string `json:"scenarioID"`
	Gamemode    string `json:"gamemode"`
	PlayerCount int    `json:"playerCount"`
	ImageURL    string `json:"imageURL"`
}

// As seen when viewing single mod at https://reforger.armaplatform.com/workshop/<mod-id>-<mod-name>
type Mod struct {
	Name           string       `json:"name"`
	Author         string       `json:"author"`
	OriginalModURL string       `json:"originalModURL"`
	APIModURL      string       `json:"apiModURL"`
	ImageURL       string       `json:"imageURL"`
	Rating         string       `json:"rating"`
	Version        string       `json:"version"`
	GameVersion    string       `json:"gameVersion"`
	Size           string       `json:"size"`
	Subscribers    int          `json:"subscribers"`
	Downloads      int          `json:"downloads"`
	Created        string       `json:"created"`
	LastModified   string       `json:"lastModified"`
	ID             string       `json:"id"`
	Summary        string       `json:"summary"`
	Description    string       `json:"description"`
	License        string       `json:"license"`
	Tags           []string     `json:"tags"`
	Dependencies   []Dependency `json:"dependencies"`
	Scenarios      []Scenario   `json:"scenarios"`
}

// Sruct returned from util.ScapeMods
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
