package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/api"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/models"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/util"
	"github.com/gorilla/mux"
)

type parameters struct {
	search string
	sort   string
	tags   []string
}

// Add additional url parameters to links if exists
func addLinkParams(links map[string]string, link string, params parameters) map[string]string {
	values := url.Values{}
	if params.search != "" {
		values.Set("search", params.search)
	}
	if params.sort != "" {
		values.Set("sort", params.sort)
	}
	for _, tag := range params.tags {
		values.Add("tags", tag)
	}
	if encoded := values.Encode(); encoded != "" {
		links[link] = links[link] + "?" + encoded
	}

	return links
}

func makeLinks(currentPage int, totalPages int, params parameters) map[string]string {
	links := make(map[string]string)

	// Create required links and add url parameters if provided
	if currentPage <= totalPages && currentPage > 1 {
		links["prev"] = fmt.Sprintf("%s/v1/mods/%d", config.GetFullURL(), currentPage-1)
		links = addLinkParams(links, "prev", params)
	}

	if currentPage >= 1 && currentPage < totalPages {
		links["next"] = fmt.Sprintf("%s/v1/mods/%d", config.GetFullURL(), currentPage+1)
		links = addLinkParams(links, "next", params)
	}

	return links
}

const (
	SortPopular     string = "popularity"
	SortNewest      string = "newest"
	SortSubscribers string = "subscribers"
	SortVersionSize string = "version_size"
)

func validSortOption(sort string) bool {
	return sort == SortPopular || sort == SortNewest || sort == SortSubscribers || sort == SortVersionSize || sort == ""
}

var validModID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// ModsHandler returns ModPreview array from initial workshop page
func (a *App) ModsHandler(w http.ResponseWriter, r *http.Request) {
	a.serveModsPage(w, r, 1)
}

// ModByPageHandler returns ModPreview array from given page number
func (a *App) ModsByPageHandler(w http.ResponseWriter, r *http.Request) {
	pageNumber, err := strconv.Atoi(mux.Vars(r)["page"])
	if err != nil || pageNumber < 1 || pageNumber > 10000 {
		config.WriteError(w, r, http.StatusBadRequest, "INVALID_PAGE", "Page must be a positive integer.")
		return
	}
	a.serveModsPage(w, r, pageNumber)
}

func (a *App) SearchHandler(w http.ResponseWriter, r *http.Request) {
	a.serveModsPage(w, r, 1)
}

func (a *App) serveModsPage(w http.ResponseWriter, r *http.Request, pageNumber int) {
	search := api.NormalizeSearch(r.URL.Query().Get("search"), 120)
	sort := api.NormalizeSort(r.URL.Query().Get("sort"), map[string]bool{
		SortPopular: true, SortNewest: true, SortSubscribers: true, SortVersionSize: true,
	})
	tags := r.URL.Query()["tags"]
	if category := r.URL.Query().Get("category"); category != "" {
		tags = append(tags, category)
	}
	tags = api.NormalizeTags(tags, 40)
	if r.URL.Query().Get("search") != "" && search == "" {
		config.WriteError(w, r, http.StatusBadRequest, "INVALID_SEARCH", "Search query is empty after normalization.")
		return
	}

	key := api.ModsCacheKey(pageNumber, search, sort, tags)
	policy := api.SelectCacheTTL(a.Config, "mods", search+strings.Join(tags, " "), http.StatusOK)
	fetch := func(ctx context.Context) api.CachedResponse {
		results, err := util.ScrapeModsContext(ctx, pageNumber, search, sort, tags)
		if err != nil {
			return api.CachedResponse{Err: err, ErrorCode: "UPSTREAM_UNAVAILABLE", Message: "Workshop list data is temporarily unavailable."}
		}

		if !results.Found {
			b, _ := json.Marshal(models.ErrorResponse{Error: models.Error{Code: "NOT_FOUND", Message: "No mods found.", RequestID: ""}})
			return api.CachedResponse{StatusCode: http.StatusNotFound, Body: b, TTL: a.Config.NotFoundCacheTTL, Stale: 0}
		}

		links := makeLinks(results.CurrentPage, results.TotalPages, parameters{search: search, sort: sort, tags: tags})

		b, err := json.Marshal(models.ModsPreviewsResponse{
			Status: "success",
			Meta: models.Meta{
				TotalPages:     results.TotalPages,
				CurrentPage:    results.CurrentPage,
				TotalMods:      results.TotalMods,
				ShownMods:      results.ShownMods,
				ModsIndexStart: results.ModsIndexStart,
				ModsIndexEnd:   results.ModsIndexEnd,
			},
			Data:  results.Mods,
			Links: links,
		})
		if err != nil {
			return api.CachedResponse{Err: err, ErrorCode: "INTERNAL_ERROR", Message: "Failed to encode response."}
		}
		a.persistListPage(ctx, key, pageNumber, search, sort, results, b, policy)
		return api.CachedResponse{StatusCode: http.StatusOK, Body: b}
	}
	a.Cache.ServeWithFallback(w, r, key, policy.Fresh, policy.Stale, fetch, a.localSearchFallback(key, pageNumber, search, sort, tags, policy))
}

// ModByIDHandler returns a single Mod
func (a *App) ModByIDHandler(w http.ResponseWriter, r *http.Request) {
	modID := strings.TrimSpace(mux.Vars(r)["id"])
	if !validModID.MatchString(modID) {
		config.WriteError(w, r, http.StatusBadRequest, "INVALID_MOD_ID", "Mod ID is malformed.")
		return
	}

	key := api.ModCacheKey(modID)
	policy := api.SelectCacheTTL(a.Config, "mod", "", http.StatusOK)
	a.Cache.Serve(w, r, key, policy.Fresh, policy.Stale, func(ctx context.Context) api.CachedResponse {
		mod, err := fetchWorkshopModByID(ctx, modID)
		if err != nil {
			return api.CachedResponse{Err: err, ErrorCode: "UPSTREAM_UNAVAILABLE", Message: "Workshop mod data is temporarily unavailable."}
		}

		if mod.Name == "" {
			b, _ := json.Marshal(models.ErrorResponse{Error: models.Error{Code: "NOT_FOUND", Message: "No mod found for the provided ID.", RequestID: ""}})
			return api.CachedResponse{StatusCode: http.StatusNotFound, Body: b, TTL: a.Config.NotFoundCacheTTL, Stale: 0}
		}

		b, err := json.Marshal(models.ModResponse{
			Status: "success",
			Data:   *mod,
		})
		if err != nil {
			return api.CachedResponse{Err: err, ErrorCode: "INTERNAL_ERROR", Message: "Failed to encode response."}
		}
		a.persistModDetail(ctx, modID, *mod, b, policy)
		return api.CachedResponse{StatusCode: http.StatusOK, Body: b}
	})
}
