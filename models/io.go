package models

// This is io.go (input/output) for json queries and responses

// HealthCheckResponse returns the health check response duh
type HealthCheckData struct {
	Code  int  `json:"code"`
	Alive bool `json:"alive"`
}

type HealthCheckResponse struct {
	Status string          `json:"status"`
	Data   HealthCheckData `json:"data"`
}

// ModsPreviewResponse is the response structure returning an array of ModPreviews
type ModsPreviewsResponse struct {
	Status string            `json:"status"`
	Meta   Meta              `json:"meta"`
	Data   []ModPreview      `json:"data"`
	Links  map[string]string `json:"links"` // use map[string]string so we only need to add required links
}

type Meta struct {
	TotalPages     int `json:"totalPages"`     // e.g. {322} pages
	CurrentPage    int `json:"currentPage"`    // e.g. page {1}
	TotalMods      int `json:"totalMods"`      // e.g. {1570} mods
	ShownMods      int `json:"shownMods"`      // e.g. showing {16} mods or data.length
	ModsIndexStart int `json:"modsIndexStart"` // e.g. showing from mods {1}
	ModsIndexEnd   int `json:"modsIndexEnd"`   // e.g. showing to mods {16}
}

// ModResponse is the response structure returning a single Mod
type ModResponse struct {
	Status string `json:"status"`
	Data   Mod    `json:"mod"`
}

// ErrorResponse returns the error message and error detals
type ErrorResponse struct {
	Status string `json:"status"`
	Error  Error  `json:"error"`
}

type Error struct {
	Code   int    `json:"code"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}
