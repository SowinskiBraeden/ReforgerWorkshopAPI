package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/api/handlers"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
	"go.uber.org/zap"
)

const version string = "2.2.0"

func main() {
	a := handlers.App{}
	a.Config = *config.New()
	a.Initialize() // Initialize router

	zap.S().Infow(fmt.Sprintf("ReforgerWorkshopAPI v%s is up and running", version), "url", a.Config.BaseURL, "port", a.Config.Port)
	log.Fatal(http.ListenAndServe(":"+a.Config.Port, a.Router))
}
