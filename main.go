package main

import (
	"log"
	"net/http"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/api/handlers"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
	"go.uber.org/zap"
)

func main() {
	a := handlers.App{}
	a.Config = *config.New()
	a.Initialize() // Initialize router

	zap.S().Infow("ReforgerWorkshopAPI is up and running", "url", a.Config.BaseURL, "port", a.Config.Port)
	log.Fatal(http.ListenAndServe(":"+a.Config.Port, a.Router))

	// _ = util.GetMod("https://reforger.armaplatform.com/workshop/5965550F24A0C1524")
	// // fmt.Printf("%v\n", mod)
}
