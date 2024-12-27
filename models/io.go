package models

// This is io.go (input/output) for json queries and responses

// HealthCheckResponse returns the health check response duh
type HealthCheckResponse struct {
	Alive bool `json:"alive"`
}

// ModsPreviewResponse is the response structure returning an array of ModPreviews
type ModsPreviewsResponse struct {
	Message    string       `json:"message"`
	Mods       []ModPreview `json:"mods"`
	Page       int          `json:"page"`
	TotalPages int          `json:"total_pages"`
}

// ModResponse is the response structure returning a single Mod
type ModResponse struct {
	Message string `json:"message"`
	Mod     Mod    `json:"mod"`
}

type MessageResponse struct {
	Message string `json:"message"`
}

// ErrorMessageResponse returns the error message
type ErrorMessageResponse struct {
	Message string `json:"message"`
	Error   string `json:"error"`
}
