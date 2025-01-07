package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/models"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/util"
	"github.com/gorilla/mux"
)

// ModsHandler returns ModPreview array from initial workshop page
func ModsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	search := r.URL.Query().Get("search")
	search = strings.Replace(search, " ", "+", -1)

	results, err := util.ScrapeMods(1, search)
	if err != nil {
		config.ErrorStatus("failed to scrape mods", http.StatusInternalServerError, w, err)
		return
	}

	links := make(map[string]string)
	if results.CurrentPage <= results.TotalPages && results.CurrentPage > 1 {
		links["prev"] = fmt.Sprintf("%s/mods/%d", config.GetFullURL(), results.CurrentPage-1)
	}

	if results.CurrentPage >= 1 && results.CurrentPage < results.TotalPages {
		links["next"] = fmt.Sprintf("%s/mods/%d", config.GetFullURL(), results.CurrentPage+1)
	}

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
	search = strings.Replace(search, " ", "+", -1)

	results, err := util.ScrapeMods(pageNumber, search)
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

	links := make(map[string]string)
	if results.CurrentPage <= results.TotalPages && results.CurrentPage > 1 {
		links["prev"] = fmt.Sprintf("%s/mods/%d", config.GetFullURL(), results.CurrentPage-1)
	}

	if results.CurrentPage >= 1 && results.CurrentPage < results.TotalPages {
		links["next"] = fmt.Sprintf("%s/mods/%d", config.GetFullURL(), results.CurrentPage+1)
	}

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
