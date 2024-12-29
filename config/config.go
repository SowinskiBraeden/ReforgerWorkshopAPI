package config

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"go.uber.org/zap"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/models"
	"github.com/joho/godotenv"
)

// Config holds the project config values
type Config struct {
	BaseURL string
	Port    string
}

// New sets up all config related services
func New() *Config {

	_ = godotenv.Load()

	//setup zap logger and replace default Logger
	logger, err := setLogger(os.Getenv("ENV"))
	if err != nil {
		// if we get an error, we will just set the default to debug and move on
		zap.S().With(err).Warn("issue setting logger")
	}
	defer logger.Sync()
	_ = zap.ReplaceGlobals(logger)

	return &Config{
		BaseURL: os.Getenv("BASE_URL"),
		Port:    os.Getenv("PORT"),
	}
}

func GetFullURL() string {
	_ = godotenv.Load()
	return os.Getenv("FULL_URL")
}

// ErrorStatus is a useful function that will log, write http headers and body for a
// given message, status code and error
func ErrorStatus(message string, httpStatusCode int, w http.ResponseWriter, err error) {
	zap.S().With(err).Error(message)
	w.WriteHeader(httpStatusCode)
	b, _ := json.Marshal(models.ErrorResponse{Status: "error", Error: models.Error{Title: message, Detail: err.Error()}})
	w.Write(b)
}

// setLogger is a helper function to set the Logger based on the environment
func setLogger(env string) (*zap.Logger, error) {
	switch env {
	case "production":
		return zap.NewProduction()
	case "development":
		return zap.NewDevelopment()
	case "local":
		return zap.NewExample(), nil
	default:
		return zap.NewExample(), fmt.Errorf("cannon find ENV car so defaulting to debug logging")
	}
}
