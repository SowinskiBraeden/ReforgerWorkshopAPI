package api

import (
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/models"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

const REQUESTS_PER_SECOND int = 15
const MAX_BURSTS int = 50

// Simple middleware to handling some basic rate limiting
func Middleware(next http.Handler) http.Handler {
	type client struct {
		limiter  *rate.Limiter
		lastSeen time.Time
	}

	var (
		mu      sync.Mutex
		clients = make(map[string]*client)
	)

	go func() {
		for {
			time.Sleep(time.Minute)
			// Lock the mutex to protect this section from race conditions.
			mu.Lock()
			for ip, client := range clients {
				if time.Since(client.lastSeen) > 5*time.Minute {
					delete(clients, ip)
				}
			}
			mu.Unlock()
		}
	}()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get request IP address
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			config.ErrorStatus("Error splitting port from request IP", http.StatusInternalServerError, w, err)
			return
		}

		// Lock the mutex to protect this section from race conditions.
		mu.Lock()
		if _, found := clients[ip]; !found {
			clients[ip] = &client{limiter: rate.NewLimiter(rate.Limit(REQUESTS_PER_SECOND), MAX_BURSTS)}
		}
		clients[ip].lastSeen = time.Now()
		if !clients[ip].limiter.Allow() {
			mu.Unlock()
			zap.S().Infow("Suppressing incoming requests, too fast!", "from", ip, "to", r.URL)
			w.WriteHeader(http.StatusTooManyRequests)
			b, _ := json.Marshal(models.MessageResponse{Message: "Too fast! The API is at capacity, try again later."})
			w.Write(b)
			return
		}
		mu.Unlock()
		next.ServeHTTP(w, r)
	})
}
