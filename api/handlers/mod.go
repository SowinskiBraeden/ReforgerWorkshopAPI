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

// ModsHandler returns ModPreview array from initial workshop page
func ModsHandler(w http.ResponseWriter, r *http.Request) {
	results := util.ScrapeMods(1)

	b, err := json.Marshal(models.ModsPreviewsResponse{
		Message:    results.Summary,
		Mods:       results.Mods,
		Page:       results.Page,
		TotalPages: results.TotalPages,
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
	pageNumber, err := strconv.Atoi(mux.Vars(r)["page"])
	if err != nil {
		config.ErrorStatus("failed to convert page number to int", http.StatusInternalServerError, w, err)
		return
	}

	results := util.ScrapeMods(pageNumber)

	b, err := json.Marshal(models.ModsPreviewsResponse{
		Message:    results.Summary,
		Mods:       results.Mods,
		Page:       results.Page,
		TotalPages: results.TotalPages,
	})
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	if results.Summary == "No mods found." {
		w.WriteHeader(http.StatusNotFound)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	w.Write(b)
}

// ModByIDHandler returns a single Mod
func ModByIDHandler(w http.ResponseWriter, r *http.Request) {
	modID := mux.Vars(r)["id"]

	var baseURL string = "reforger.armaplatform.com"
	var mod models.Mod = util.GetMod(fmt.Sprintf("https://%s/workshop/%s", baseURL, modID))

	if mod.Name == "" {
		b, err := json.Marshal(models.NoModFoundResponse{Message: "Not found"})
		if err != nil {
			config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write(b)
		return
	}

	b, err := json.Marshal(models.ModResponse{Message: "success", Mod: mod})
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}
