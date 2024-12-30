package handlers

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/api"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/models"
	"github.com/gorilla/mux"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
)

// App stores the router and db connection so it can be reused
type App struct {
	Router *mux.Router
	Config config.Config
}

// New creates a new mux router and all the routes
func (a *App) New() *mux.Router {

	router := mux.NewRouter()

	// apiCreate := r.PathPrefix("/api").Subrouter()

	// API Routes
	router.Handle("/health", api.Middleware(http.HandlerFunc(healthCheckHandler))).Methods("GET")     // Check status of API
	router.Handle("/mod/{id}", api.Middleware(http.HandlerFunc(ModByIDHandler))).Methods("GET")       // Return Mod from ID
	router.Handle("/mods", api.Middleware(http.HandlerFunc(ModsHandler))).Methods("GET")              // Return ModPreview array from first page
	router.Handle("/mods/{page}", api.Middleware(http.HandlerFunc(ModsByPageHandler))).Methods("GET") // Return ModPreview array from page {page_number}

	return router
}

func (a *App) Initialize() {
	// initialize api router
	a.Router = a.New()
}

func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	b, _ := json.Marshal(models.HealthCheckResponse{
		Status: "success",
		Data: models.HealthCheckData{
			Code:  http.StatusOK,
			Alive: true,
		},
	})
	_, _ = io.Writer.Write(w, b)
}
