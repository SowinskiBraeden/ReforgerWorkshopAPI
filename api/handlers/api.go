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

	r := mux.NewRouter()

	// healthcheck
	r.HandleFunc("/health", healthCheckHandler)

	apiCreate := r.PathPrefix("/api").Subrouter()

	// API Routes
	apiCreate.Handle("/mod/{id}", api.Middleware(http.HandlerFunc(ModByIDHandler))).Methods("GET")       // Return Mod from ID
	apiCreate.Handle("/mods", api.Middleware(http.HandlerFunc(ModsHandler))).Methods("GET")              // Return ModPreview array from first page
	apiCreate.Handle("/mods/{page}", api.Middleware(http.HandlerFunc(ModsByPageHandler))).Methods("GET") // Return ModPreview array from page {page_number}

	return r
}

func (a *App) Initialize() {
	// initialize api router
	a.Router = a.New()
}

func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	b, _ := json.Marshal(models.HealthCheckResponse{
		Alive: true,
	})
	_, _ = io.Writer.Write(w, b)
}
