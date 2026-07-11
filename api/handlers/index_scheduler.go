package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/api"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/models"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/util"
	"go.uber.org/zap"
)

type IndexScheduler struct {
	app    *App
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewIndexScheduler(app *App) *IndexScheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &IndexScheduler{app: app, ctx: ctx, cancel: cancel}
}

func (s *IndexScheduler) Start() {
	if s == nil || s.app == nil || s.app.Cache == nil {
		return
	}
	s.wg.Add(1)
	go s.loop()
}

func (s *IndexScheduler) Stop() {
	if s == nil {
		return
	}
	s.cancel()
	s.wg.Wait()
}

func (s *IndexScheduler) loop() {
	defer s.wg.Done()
	s.enqueueRound()
	interval := s.app.Config.IndexRefreshInterval
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.enqueueRound()
		}
	}
}

func (s *IndexScheduler) enqueueRound() {
	popularPages := s.app.Config.IndexPopularPages
	if popularPages < 0 {
		popularPages = 0
	}
	recentPages := s.app.Config.IndexRecentPages
	if recentPages < 0 {
		recentPages = 0
	}
	for page := 1; page <= popularPages; page++ {
		s.enqueueListRefresh(page, SortPopular)
	}
	for page := 1; page <= recentPages; page++ {
		s.enqueueListRefresh(page, SortNewest)
	}
}

func (s *IndexScheduler) enqueueListRefresh(page int, sort string) {
	policy := api.SelectCacheTTL(s.app.Config, "mods", "", http.StatusOK)
	key := api.ModsCacheKey(page, "", sort, nil)
	resourceURL := "/v1/mods/" + strconvItoa(page) + "?sort=" + sort
	_, created, err := s.app.Cache.EnqueueRefresh(key, resourceURL, policy.Fresh, policy.Stale, api.RefreshPriorityLow, func(ctx context.Context) api.CachedResponse {
		start := time.Now()
		if s.app.Metrics != nil {
			s.app.Metrics.RecordIndexEvent("background_running", 0)
		}
		results, err := util.ScrapeModsContext(ctx, page, "", sort, nil)
		if err != nil {
			if s.app.Metrics != nil {
				s.app.Metrics.RecordIndexEvent("refresh_failed", time.Since(start))
			}
			return api.CachedResponse{Err: err, ErrorCode: "UPSTREAM_UNAVAILABLE", Message: "Workshop list data is temporarily unavailable."}
		}
		if !results.Found {
			body, _ := json.Marshal(models.ErrorResponse{Error: models.Error{Code: "NOT_FOUND", Message: "No mods found.", RequestID: ""}})
			return api.CachedResponse{StatusCode: http.StatusNotFound, Body: body, TTL: s.app.Config.NotFoundCacheTTL}
		}
		links := makeLinks(results.CurrentPage, results.TotalPages, parameters{sort: sort})
		body, err := json.Marshal(models.ModsPreviewsResponse{
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
		s.app.persistListPage(ctx, key, page, "", sort, results, body, policy)
		if s.app.Metrics != nil {
			s.app.Metrics.RecordIndexEvent("background_succeeded", time.Since(start))
		}
		return api.CachedResponse{StatusCode: http.StatusOK, Body: body}
	})
	if err != nil {
		if s.app.Metrics != nil {
			s.app.Metrics.RecordIndexEvent("background_failed", 0)
		}
		zap.S().Warnw("background index job was not queued", "key", key, "error", err)
		return
	}
	if created && s.app.Metrics != nil {
		s.app.Metrics.RecordIndexEvent("background_queued", 0)
	}
}

func strconvItoa(value int) string {
	if value == 1 {
		return "1"
	}
	return strconv.FormatInt(int64(value), 10)
}
