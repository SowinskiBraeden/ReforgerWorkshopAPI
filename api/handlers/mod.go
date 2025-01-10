package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/models"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/util"
	"github.com/gorilla/mux"
)

type parameters struct {
	search string
	sort   string
}

// Add additional url parameters to links if exists
func addLinkParams(links map[string]string, link string, params parameters) map[string]string {
	if params.search != "" {
		links[link] = fmt.Sprintf("%s?search=%s", links[link], params.search)
	}

	if params.sort != "" && params.search != "" {
		links[link] = fmt.Sprintf("%s&sort=%s", links[link], params.sort)
	} else if params.sort != "" {
		links[link] = fmt.Sprintf("%s?sort=%s", links[link], params.sort)
	}

	return links
}

func makeLinks(currentPage int, totalPages int, params parameters) map[string]string {
	links := make(map[string]string)

	// Create required links and add url parameters if provided
	if currentPage <= totalPages && currentPage > 1 {
		links["prev"] = fmt.Sprintf("%s/mods/%d", config.GetFullURL(), currentPage-1)
		links = addLinkParams(links, "prev", params)
	}

	if currentPage >= 1 && currentPage < totalPages {
		links["next"] = fmt.Sprintf("%s/mods/%d", config.GetFullURL(), currentPage+1)
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

// ModsHandler returns ModPreview array from initial workshop page
func ModsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	search := r.URL.Query().Get("search")
	sort := r.URL.Query().Get("sort")
	if !validSortOption(sort) {
		sort = ""
	}

	results, err := util.ScrapeMods(1, search, sort, []string{})
	if err != nil {
		config.ErrorStatus("failed to scrape mods", http.StatusInternalServerError, w, err)
		return
	}

	links := makeLinks(results.CurrentPage, results.TotalPages, parameters{search: search, sort: sort})

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
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// ModByPageHandler returns ModPreview array from given page number
func ModsByPageHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "application/json")
	pageNumber, err := strconv.Atoi(mux.Vars(r)["page"])
	if err != nil {
		config.ErrorStatus("failed to convert page number to int", http.StatusInternalServerError, w, err)
		return
	}

	search := r.URL.Query().Get("search")
	sort := r.URL.Query().Get("sort")
	if !validSortOption(sort) {
		sort = ""
	}

	results, err := util.ScrapeMods(pageNumber, search, sort, []string{})
	if err != nil {
		config.ErrorStatus("failed to scrape mods", http.StatusInternalServerError, w, err)
		return
	}

	if !results.Found {
		w.WriteHeader(http.StatusNotFound)
		b, err := json.Marshal(models.ErrorResponse{
			Status: "fail",
			Error: models.Error{
				Code:   http.StatusNotFound,
				Title:  "No mods found.",
				Detail: "No mods have been found, you may have requests a page number that does not exist.",
			},
		})
		if err != nil {
			config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
			return
		}
		w.Write(b)
		return
	}

	links := makeLinks(results.CurrentPage, results.TotalPages, parameters{search: search, sort: sort})

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
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// ModByIDHandler returns a single Mod
func ModByIDHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "application/json")
	modID := mux.Vars(r)["id"]

	var baseURL string = "reforger.armaplatform.com"
	var mod models.Mod = *util.GetMod(fmt.Sprintf("https://%s/workshop/%s", baseURL, modID))

	if mod.Name == "" {
		b, err := json.Marshal(models.ErrorResponse{
			Status: "fail",
			Error: models.Error{
				Code:   http.StatusNotFound,
				Title:  "No mods found.",
				Detail: "No mods have been found, the provided mod ID did not return any results.",
			},
		})
		if err != nil {
			config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write(b)
		return
	}

	b, err := json.Marshal(models.ModResponse{
		Status: "success",
		Data:   mod,
	})
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}
