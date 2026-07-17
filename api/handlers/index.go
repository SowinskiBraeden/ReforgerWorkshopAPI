package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/api"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/models"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/util"
	"go.uber.org/zap"
)

func (a *App) persistListPage(ctx context.Context, key string, pageNumber int, search string, sort string, results *models.WebScrapeResults, body []byte, policy api.CacheTTLPolicy) {
	if a.IndexStore == nil || results == nil || !results.Found {
		return
	}
	now := time.Now().UTC()
	modIDs := make([]string, 0, len(results.Mods))
	for _, mod := range results.Mods {
		modIDs = append(modIDs, api.CanonicalModID(mod.ID))
		if err := a.IndexStore.UpsertModPreview(ctx, mod); err != nil {
			a.recordIndexDBError("failed to upsert mod preview", err)
		}
	}
	modIDsJSON, _ := json.Marshal(modIDs)
	if err := a.IndexStore.PutListPage(ctx, api.ListPageRecord{
		CacheKey:        key,
		Page:            pageNumber,
		Sort:            normalizedSortForStorage(sort),
		Search:          strings.ToLower(api.NormalizeSearch(search, 120)),
		ResourceType:    "mods",
		ModIDsJSON:      string(modIDsJSON),
		Body:            body,
		FreshUntil:      now.Add(policy.Fresh),
		StaleUntil:      now.Add(policy.Fresh + policy.Stale),
		LastRefreshedAt: now,
		LastAccessedAt:  now,
		RefreshStatus:   string(api.RefreshJobSucceeded),
	}); err != nil {
		a.recordIndexDBError("failed to persist list page", err)
	}
	a.Hooks.IndexEvent("page_refreshed")
	a.queueDetailHydration(results.Mods)
}

func (a *App) persistModDetail(ctx context.Context, modID string, mod models.Mod, body []byte, policy api.CacheTTLPolicy) {
	if a.IndexStore == nil {
		return
	}
	now := time.Now().UTC()
	if err := a.IndexStore.UpsertModDetail(ctx, mod); err != nil {
		a.recordIndexDBError("failed to upsert mod detail", err)
	}
	deps, _ := json.Marshal(mod.Dependencies)
	scenarios, _ := json.Marshal(mod.Scenarios)
	if err := a.IndexStore.PutModDetail(ctx, api.ModDetailRecord{
		ModID:            strings.TrimSpace(modID),
		Body:             body,
		DependenciesJSON: string(deps),
		ScenariosJSON:    string(scenarios),
		FreshUntil:       now.Add(policy.Fresh),
		StaleUntil:       now.Add(policy.Fresh + policy.Stale),
		LastRefreshedAt:  now,
		LastAccessedAt:   now,
		RefreshStatus:    string(api.RefreshJobSucceeded),
	}); err != nil {
		a.recordIndexDBError("failed to persist mod detail", err)
	}
	a.Hooks.IndexEvent("detail_refreshed")
}

func (a *App) localSearchFallback(key string, pageNumber int, search string, sort string, tags []string, policy api.CacheTTLPolicy) api.LocalFallbackFunc {
	if a.IndexStore == nil || strings.TrimSpace(search) == "" || len(tags) > 0 {
		return nil
	}
	return func(ctx context.Context) (api.CachedResponse, bool) {
		offset := (pageNumber - 1) * 16
		mods, err := a.IndexStore.SearchMods(ctx, search, 16, offset)
		if err != nil {
			a.recordIndexDBError("local search failed", err)
			return api.CachedResponse{}, false
		}
		if len(mods) == 0 {
			a.Hooks.IndexEvent("local_search_empty")
			return api.CachedResponse{}, false
		}
		a.Hooks.IndexEvent("local_search_hit")
		totalPages := pageNumber
		if len(mods) == 16 {
			totalPages = pageNumber + 1
		}
		body, err := json.Marshal(models.ModsPreviewsResponse{
			Status: "success",
			Meta: models.Meta{
				TotalPages:     totalPages,
				CurrentPage:    pageNumber,
				TotalMods:      offset + len(mods),
				ShownMods:      len(mods),
				ModsIndexStart: offset + 1,
				ModsIndexEnd:   offset + len(mods),
			},
			Data:  mods,
			Links: makeLinks(pageNumber, totalPages, parameters{search: search, sort: normalizedSortForStorage(sort), tags: tags}),
		})
		if err != nil {
			return api.CachedResponse{}, false
		}
		a.persistSyntheticListPage(ctx, key, pageNumber, search, sort, mods, body, policy)
		return api.CachedResponse{StatusCode: http.StatusOK, Body: body, TTL: policy.Fresh, Stale: policy.Stale}, true
	}
}

func (a *App) persistSyntheticListPage(ctx context.Context, key string, pageNumber int, search string, sort string, mods []models.ModPreview, body []byte, policy api.CacheTTLPolicy) {
	if a.IndexStore == nil {
		return
	}
	now := time.Now().UTC()
	modIDs := make([]string, 0, len(mods))
	for _, mod := range mods {
		modIDs = append(modIDs, api.CanonicalModID(mod.ID))
	}
	modIDsJSON, _ := json.Marshal(modIDs)
	if err := a.IndexStore.PutListPage(ctx, api.ListPageRecord{
		CacheKey:        key,
		Page:            pageNumber,
		Sort:            normalizedSortForStorage(sort),
		Search:          strings.ToLower(api.NormalizeSearch(search, 120)),
		ResourceType:    "mods",
		ModIDsJSON:      string(modIDsJSON),
		Body:            body,
		FreshUntil:      now.Add(policy.Fresh),
		StaleUntil:      now.Add(policy.Fresh + policy.Stale),
		LastRefreshedAt: now,
		LastAccessedAt:  now,
		RefreshStatus:   "local_search",
	}); err != nil {
		a.recordIndexDBError("failed to persist local search page", err)
	}
}

func (a *App) queueDetailHydration(mods []models.ModPreview) {
	if a.Cache == nil || a.IndexStore == nil {
		return
	}
	limit := 8
	if len(mods) < limit {
		limit = len(mods)
	}
	policy := api.SelectCacheTTL(a.Config, "mod", "", http.StatusOK)
	for i := 0; i < limit; i++ {
		modID := strings.TrimSpace(mods[i].ID)
		if modID == "" {
			continue
		}
		key := api.ModCacheKey(modID)
		_, created, err := a.Cache.EnqueueRefresh(key, "/v1/mod/"+modID, policy.Fresh, policy.Stale, api.RefreshPriorityLow, func(ctx context.Context) api.CachedResponse {
			mod, err := fetchWorkshopModByID(ctx, modID)
			if err != nil {
				return api.CachedResponse{Err: err, ErrorCode: "UPSTREAM_UNAVAILABLE", Message: "Workshop mod data is temporarily unavailable."}
			}
			if mod.Name == "" {
				return api.CachedResponse{StatusCode: http.StatusNotFound, Body: []byte(`{"error":{"code":"NOT_FOUND","message":"No mod found for the provided ID.","requestId":""}}`), TTL: a.Config.NotFoundCacheTTL}
			}
			body, err := json.Marshal(models.ModResponse{Status: "success", Data: *mod})
			if err != nil {
				return api.CachedResponse{Err: err, ErrorCode: "INTERNAL_ERROR", Message: "Failed to encode response."}
			}
			a.persistModDetail(ctx, modID, *mod, body, policy)
			return api.CachedResponse{StatusCode: http.StatusOK, Body: body}
		})
		if err == nil && created {
			a.Hooks.IndexEvent("background_queued")
		}
	}
}

func fetchWorkshopModByID(ctx context.Context, modID string) (*models.Mod, error) {
	var baseURL string = "reforger.armaplatform.com"
	modID = strings.TrimSpace(modID)
	mod, err := util.GetModContext(ctx, fmt.Sprintf("https://%s/workshop/%s", baseURL, modID))
	if err == nil && mod != nil && mod.Name != "" {
		return mod, nil
	}
	upperID := strings.ToUpper(modID)
	if upperID == modID {
		return mod, err
	}
	fallback, fallbackErr := util.GetModContext(ctx, fmt.Sprintf("https://%s/workshop/%s", baseURL, upperID))
	if fallbackErr == nil {
		return fallback, nil
	}
	if err != nil {
		return mod, err
	}
	return fallback, fallbackErr
}

func normalizedSortForStorage(sort string) string {
	sort = strings.ToLower(strings.TrimSpace(sort))
	if sort == "" {
		return SortPopular
	}
	return sort
}

func (a *App) recordIndexDBError(message string, err error) {
	if err == nil {
		return
	}
	a.Hooks.IndexEvent("database_error")
	zap.S().Warnw(message, "error", err)
}
