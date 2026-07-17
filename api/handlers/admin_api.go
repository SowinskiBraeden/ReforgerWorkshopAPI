package handlers

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/telemetry"
	"github.com/gorilla/mux"
)

// parseRange resolves the range/from/to query parameters into a UTC window
// plus the immediately preceding window of equal length for comparisons.
// Live windows end one second in the future so events written in the same
// millisecond as the query are included.
func parseRange(query url.Values, now time.Time) (from time.Time, to time.Time, prevFrom time.Time, prevTo time.Time) {
	now = now.UTC().Add(time.Second)
	day := func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	}
	switch strings.TrimSpace(query.Get("range")) {
	case "yesterday":
		from, to = day(now).AddDate(0, 0, -1), day(now)
	case "7d":
		from, to = day(now).AddDate(0, 0, -6), now
	case "30d", "":
		from, to = day(now).AddDate(0, 0, -29), now
	case "today":
		from, to = day(now), now
	case "this_month":
		from, to = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC), now
	case "last_month":
		first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		from, to = first.AddDate(0, -1, 0), first
	case "this_year":
		from, to = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC), now
	case "custom":
		from, to = parseDay(query.Get("from"), day(now).AddDate(0, 0, -29)), parseDay(query.Get("to"), day(now)).AddDate(0, 0, 1)
		if !to.After(from) {
			to = from.AddDate(0, 0, 1)
		}
	default:
		from, to = day(now).AddDate(0, 0, -29), now
	}
	length := to.Sub(from)
	prevFrom, prevTo = from.Add(-length), from
	return from, to, prevFrom, prevTo
}

func parseDay(value string, fallback time.Time) time.Time {
	parsed, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(value), time.UTC)
	if err != nil {
		return fallback
	}
	return parsed
}

func parsePagination(query url.Values) (int, int) {
	limit, _ := strconv.Atoi(query.Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	offset, _ := strconv.Atoi(query.Get("offset"))
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// requestFilterFromQuery maps explorer query params onto the shared filter.
func requestFilterFromQuery(query url.Values, from time.Time, to time.Time) telemetry.RequestFilter {
	filter := telemetry.RequestFilter{
		From:          from,
		To:            to,
		RouteTemplate: query.Get("route"),
		Path:          query.Get("path"),
		Method:        query.Get("method"),
		EndpointGroup: query.Get("group"),
		Source:        query.Get("source"),
		AuthType:      query.Get("auth"),
		AccountID:     query.Get("user"),
		APIKeyID:      query.Get("key"),
		APIClientID:   query.Get("client_id"),
		ClientName:    query.Get("client"),
		ClientKind:    query.Get("client_kind"),
		CountryCode:   query.Get("country"),
		NetworkID:     query.Get("network"),
		CacheStatus:   query.Get("cache"),
		SearchTerm:    query.Get("term"),
		RequestID:     query.Get("request_id"),
		TraceID:       query.Get("trace_id"),
		ErrorCategory: query.Get("error_category"),
		AppVersion:    query.Get("version"),
		Search:        query.Get("q"),
	}
	filter.Status, _ = strconv.Atoi(query.Get("status"))
	filter.StatusFamily, _ = strconv.Atoi(query.Get("status_family"))
	if minMs, err := strconv.ParseFloat(query.Get("min_ms"), 64); err == nil {
		filter.MinDurationMs = minMs
	}
	if value := query.Get("rate_limited"); value != "" {
		limited := value == "true" || value == "1"
		filter.RateLimited = &limited
	}
	return filter
}

func (a *App) telemetryReady(w http.ResponseWriter, r *http.Request) bool {
	if a.Telemetry == nil {
		config.WriteError(w, r, http.StatusServiceUnavailable, "TELEMETRY_DISABLED", "Telemetry storage is not available.")
		return false
	}
	return true
}

// --- Overview ---

func (a *App) adminOverviewHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	ctx := r.Context()
	from, to, prevFrom, prevTo := parseRange(r.URL.Query(), time.Now())
	current, err := a.Telemetry.Totals(ctx, telemetry.RequestFilter{From: from, To: to})
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "OVERVIEW_FAILED", "Failed to compute overview.")
		return
	}
	previous, _ := a.Telemetry.Totals(ctx, telemetry.RequestFilter{From: prevFrom, To: prevTo})

	topEndpoints, _ := a.Telemetry.TopBy(ctx, telemetry.RequestFilter{From: from, To: to}, "route", 10)
	slowEndpoints, _ := a.Telemetry.SlowestEndpoints(ctx, telemetry.RequestFilter{From: from, To: to}, 10)
	topClients, _ := a.Telemetry.TopBy(ctx, telemetry.RequestFilter{From: from, To: to}, "client", 10)
	topCountries, _ := a.Telemetry.TopBy(ctx, telemetry.RequestFilter{From: from, To: to}, "country", 10)
	topNetworks, _ := a.Telemetry.TopBy(ctx, telemetry.RequestFilter{From: from, To: to}, "network", 10)
	topUsers, _ := a.Telemetry.TopBy(ctx, telemetry.RequestFilter{From: from, To: to}, "account", 10)
	recentErrors, _, _ := a.Telemetry.ListErrors(ctx, telemetry.ErrorFilter{From: from, To: to}, 10, 0)
	series, _ := a.Telemetry.Timeseries(ctx, telemetry.RequestFilter{From: from, To: to}, seriesInterval(from, to), "")

	newUsers, _ := a.Telemetry.ActivitySeries(ctx, "user", from, to.Add(-time.Nanosecond))
	newClients, _ := a.Telemetry.ActivitySeries(ctx, "client", from, to.Add(-time.Nanosecond))
	var newUserTotal, newClientTotal int64
	for _, point := range newUsers {
		newUserTotal += point.New
	}
	for _, point := range newClients {
		newClientTotal += point.New
	}

	writeAdminJSON(w, map[string]any{
		"range":         map[string]any{"from": from, "to": to, "prevFrom": prevFrom, "prevTo": prevTo},
		"totals":        current,
		"previous":      previous,
		"series":        series,
		"topEndpoints":  topEndpoints,
		"slowEndpoints": slowEndpoints,
		"topClients":    topClients,
		"topUsers":      topUsers,
		"topCountries":  topCountries,
		"topNetworks":   topNetworks,
		"recentErrors":  recentErrors,
		"newUsers":      newUserTotal,
		"newClients":    newClientTotal,
		"gauges":        a.Recorder.Gauges(),
		"version":       Version,
		"aggregation":   a.Aggregator.State(r.Context()),
	})
}

func seriesInterval(from time.Time, to time.Time) string {
	span := to.Sub(from)
	switch {
	case span <= 26*time.Hour:
		return "hour"
	case span <= 100*24*time.Hour:
		return "day"
	default:
		return "month"
	}
}

// --- Real-time ---

func (a *App) adminRealtimeHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	ctx := r.Context()
	now := time.Now().UTC()
	last5, _ := a.Telemetry.Totals(ctx, telemetry.RequestFilter{From: now.Add(-5 * time.Minute), To: now})
	last15, _ := a.Telemetry.Totals(ctx, telemetry.RequestFilter{From: now.Add(-15 * time.Minute), To: now})
	last60, _ := a.Telemetry.Totals(ctx, telemetry.RequestFilter{From: now.Add(-time.Hour), To: now})
	perMinute, _ := a.Telemetry.Timeseries(ctx, telemetry.RequestFilter{From: now.Add(-30 * time.Minute), To: now}, "minute", "")
	recent, _, _ := a.Telemetry.ListRequests(ctx, telemetry.RequestFilter{From: now.Add(-15 * time.Minute), To: now}, "", 30, 0)
	failed, _, _ := a.Telemetry.ListRequests(ctx, telemetry.RequestFilter{From: now.Add(-time.Hour), To: now, StatusFamily: 5}, "", 10, 0)
	slowThreshold := a.slowRequestThreshold(r)
	slow, _, _ := a.Telemetry.ListRequests(ctx, telemetry.RequestFilter{From: now.Add(-time.Hour), To: now, MinDurationMs: slowThreshold}, "duration", 10, 0)
	jobStats, _ := a.Telemetry.JobStatsFor(ctx, telemetry.JobFilter{From: now.Add(-time.Hour), To: now})

	writeAdminJSON(w, map[string]any{
		"generatedAt": now,
		"last5m":      last5,
		"last15m":     last15,
		"last60m":     last60,
		"perMinute":   perMinute,
		"recent":      recent,
		"failed":      failed,
		"slow":        slow,
		"slowMs":      slowThreshold,
		"jobs":        jobStats,
		"gauges":      a.Recorder.Gauges(),
		"health":      a.healthSummary(r),
	})
}

// --- Traffic / timeseries ---

func (a *App) adminTimeseriesHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	from, to, _, _ := parseRange(r.URL.Query(), time.Now())
	filter := requestFilterFromQuery(r.URL.Query(), from, to)
	interval := r.URL.Query().Get("interval")
	if interval == "" {
		interval = seriesInterval(from, to)
	}
	series, err := a.Telemetry.Timeseries(r.Context(), filter, interval, r.URL.Query().Get("group_by"))
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "TIMESERIES_FAILED", "Failed to compute series.")
		return
	}
	totals, _ := a.Telemetry.Totals(r.Context(), filter)
	writeAdminJSON(w, map[string]any{"series": series, "totals": totals, "interval": interval})
}

// --- Requests explorer ---

func (a *App) adminRequestsHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	from, to, _, _ := parseRange(r.URL.Query(), time.Now())
	filter := requestFilterFromQuery(r.URL.Query(), from, to)
	limit, offset := parsePagination(r.URL.Query())
	requests, total, err := a.Telemetry.ListRequests(r.Context(), filter, r.URL.Query().Get("order"), limit, offset)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "REQUESTS_FAILED", "Failed to list requests.")
		return
	}
	writeAdminJSON(w, map[string]any{"requests": requests, "total": total, "limit": limit, "offset": offset})
}

func (a *App) adminRequestDetailHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	event, ok, err := a.Telemetry.GetRequest(r.Context(), mux.Vars(r)["id"])
	if err != nil || !ok {
		config.WriteError(w, r, http.StatusNotFound, "REQUEST_NOT_FOUND", "Request was not found.")
		return
	}
	logs, _, _ := a.Telemetry.ListLogs(r.Context(), telemetry.LogFilter{
		From: event.At.Add(-time.Hour), To: event.At.Add(time.Hour), RequestID: event.RequestID,
	}, 100, 0)
	errors, _, _ := a.Telemetry.ListErrors(r.Context(), telemetry.ErrorFilter{
		From: event.At.Add(-time.Hour), To: event.At.Add(time.Hour), RequestID: event.RequestID,
	}, 20, 0)
	jobs, _, _ := a.Telemetry.ListJobs(r.Context(), telemetry.JobFilter{
		From: event.At.Add(-time.Hour), To: event.At.Add(2 * time.Hour), RequestID: event.RequestID,
	}, "", 20, 0)
	writeAdminJSON(w, map[string]any{"request": event, "logs": logs, "errors": errors, "jobs": jobs})
}

// --- Endpoints ---

func (a *App) adminEndpointsHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	from, to, _, _ := parseRange(r.URL.Query(), time.Now())
	filter := requestFilterFromQuery(r.URL.Query(), from, to)
	stats, err := a.Telemetry.EndpointStats(r.Context(), filter)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "ENDPOINTS_FAILED", "Failed to compute endpoint stats.")
		return
	}
	writeAdminJSON(w, map[string]any{"endpoints": stats})
}

// --- Performance ---

func (a *App) slowRequestThreshold(r *http.Request) float64 {
	if a.Telemetry != nil {
		if value := a.Telemetry.Setting(r.Context(), "slow_request_ms", ""); value != "" {
			if parsed, err := strconv.ParseFloat(value, 64); err == nil && parsed > 0 {
				return parsed
			}
		}
	}
	return float64(a.Config.TelemetrySlowRequestMs)
}

func (a *App) adminPerformanceHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	ctx := r.Context()
	from, to, _, _ := parseRange(r.URL.Query(), time.Now())
	filter := requestFilterFromQuery(r.URL.Query(), from, to)
	latency, err := a.Telemetry.Latency(ctx, filter)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "PERFORMANCE_FAILED", "Failed to compute latency stats.")
		return
	}
	threshold := a.slowRequestThreshold(r)
	if custom, err := strconv.ParseFloat(r.URL.Query().Get("threshold_ms"), 64); err == nil && custom > 0 {
		threshold = custom
	}
	slowFilter := filter
	slowFilter.MinDurationMs = threshold
	limit, offset := parsePagination(r.URL.Query())
	slow, slowTotal, _ := a.Telemetry.ListRequests(ctx, slowFilter, "duration", limit, offset)
	series, _ := a.Telemetry.Timeseries(ctx, filter, seriesInterval(from, to), "")
	jobStats, _ := a.Telemetry.JobStatsFor(ctx, telemetry.JobFilter{From: from, To: to})
	bySource, _ := a.Telemetry.Timeseries(ctx, filter, seriesInterval(from, to), "source")

	writeAdminJSON(w, map[string]any{
		"latency":     latency,
		"slow":        slow,
		"slowTotal":   slowTotal,
		"thresholdMs": threshold,
		"series":      series,
		"bySource":    bySource,
		"jobs":        jobStats,
		"gauges":      a.Recorder.Gauges(),
	})
}

// --- Cache ---

func (a *App) adminCacheHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	ctx := r.Context()
	from, to, _, _ := parseRange(r.URL.Query(), time.Now())
	filter := requestFilterFromQuery(r.URL.Query(), from, to)
	totals, err := a.Telemetry.Totals(ctx, filter)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "CACHE_FAILED", "Failed to compute cache stats.")
		return
	}
	series, _ := a.Telemetry.Timeseries(ctx, filter, seriesInterval(from, to), "cache")
	byEndpoint, _ := a.Telemetry.EndpointStats(ctx, filter)
	missFilter := filter
	missFilter.CacheStatus = telemetry.CacheMiss
	topMissed, _ := a.Telemetry.TopBy(ctx, missFilter, "route", 10)
	hitFilter := filter
	hitFilter.CacheStatus = telemetry.CacheHit
	topHit, _ := a.Telemetry.TopBy(ctx, hitFilter, "path", 10)
	topMods, _ := a.Telemetry.ModStats(ctx, from, to, 15)
	jobStats, _ := a.Telemetry.JobStatsFor(ctx, telemetry.JobFilter{From: from, To: to, Kind: telemetry.JobKindCacheRefresh})

	var snapshot any
	if a.Cache != nil {
		snapshot = a.Cache.Snapshot(25)
	}
	writeAdminJSON(w, map[string]any{
		"totals":     totals,
		"series":     series,
		"byEndpoint": byEndpoint,
		"topMissed":  topMissed,
		"topHit":     topHit,
		"topMods":    topMods,
		"refreshes":  jobStats,
		"live":       snapshot,
		"counters":   a.Hooks.Counters(),
		"gauges":     a.Recorder.Gauges(),
	})
}

// --- Errors ---

func errorFilterFromQuery(query url.Values, from time.Time, to time.Time) telemetry.ErrorFilter {
	filter := telemetry.ErrorFilter{
		From:        from,
		To:          to,
		Severity:    query.Get("severity"),
		Category:    query.Get("category"),
		Code:        query.Get("code"),
		Route:       query.Get("route"),
		Resolution:  query.Get("resolution"),
		Fingerprint: query.Get("fingerprint"),
		RequestID:   query.Get("request_id"),
		AccountID:   query.Get("user"),
		ClientName:  query.Get("client"),
		AppVersion:  query.Get("version"),
		Search:      query.Get("q"),
	}
	filter.Status, _ = strconv.Atoi(query.Get("status"))
	return filter
}

func (a *App) adminErrorsHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	from, to, _, _ := parseRange(r.URL.Query(), time.Now())
	filter := errorFilterFromQuery(r.URL.Query(), from, to)
	limit, offset := parsePagination(r.URL.Query())
	errorsList, total, err := a.Telemetry.ListErrors(r.Context(), filter, limit, offset)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "ERRORS_FAILED", "Failed to list errors.")
		return
	}
	patterns, _ := a.Telemetry.ErrorPatterns(r.Context(), from, to, 25)
	writeAdminJSON(w, map[string]any{"errors": errorsList, "total": total, "patterns": patterns})
}

func (a *App) adminErrorDetailHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	event, ok, err := a.Telemetry.GetError(r.Context(), mux.Vars(r)["id"])
	if err != nil || !ok {
		config.WriteError(w, r, http.StatusNotFound, "ERROR_NOT_FOUND", "Error was not found.")
		return
	}
	var request *telemetry.RequestEvent
	if event.RequestID != "" {
		if found, ok, _ := a.Telemetry.GetRequest(r.Context(), event.RequestID); ok {
			request = &found
		}
	}
	related, _, _ := a.Telemetry.ListErrors(r.Context(), telemetry.ErrorFilter{
		From: event.At.AddDate(0, -1, 0), To: event.At.AddDate(0, 0, 1), Fingerprint: event.Fingerprint,
	}, 20, 0)
	writeAdminJSON(w, map[string]any{"error": event, "request": request, "related": related})
}

func (a *App) adminErrorUpdateHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	var input struct {
		Resolution   string `json:"resolution"`
		Notes        string `json:"notes"`
		WholePattern bool   `json:"wholePattern"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input); err != nil {
		config.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON.")
		return
	}
	errorID := mux.Vars(r)["id"]
	if err := a.Telemetry.UpdateErrorResolution(r.Context(), errorID, input.Resolution, input.Notes, input.WholePattern); err != nil {
		config.WriteError(w, r, http.StatusBadRequest, "ERROR_UPDATE_FAILED", "Failed to update error resolution.")
		return
	}
	a.audit(r, "error.resolution", "error", errorID, input)
	writeAdminJSON(w, map[string]bool{"updated": true})
}

// --- Rate limits ---

func (a *App) adminRateLimitsHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	ctx := r.Context()
	from, to, _, _ := parseRange(r.URL.Query(), time.Now())
	filter := requestFilterFromQuery(r.URL.Query(), from, to)
	totals, err := a.Telemetry.Totals(ctx, filter)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "RATELIMITS_FAILED", "Failed to compute rate-limit stats.")
		return
	}
	limited := true
	limitedFilter := filter
	limitedFilter.RateLimited = &limited
	topClients, _ := a.Telemetry.TopBy(ctx, limitedFilter, "client", 15)
	topRoutes, _ := a.Telemetry.TopBy(ctx, limitedFilter, "route", 15)
	topBuckets, _ := a.Telemetry.TopBy(ctx, limitedFilter, "rate_bucket", 15)
	topNetworks, _ := a.Telemetry.TopBy(ctx, limitedFilter, "network", 15)
	series, _ := a.Telemetry.Timeseries(ctx, filter, seriesInterval(from, to), "")
	limit, offset := parsePagination(r.URL.Query())
	recent, total, _ := a.Telemetry.ListRequests(ctx, limitedFilter, "", limit, offset)

	// Upgrade opportunities: anonymous/free traffic that got limited would
	// fit a higher tier.
	anonLimited := int64(0)
	for _, bucket := range topBuckets {
		if bucket.Key == "anonymous" || bucket.Key == "plan:free" {
			anonLimited += bucket.Count
		}
	}
	writeAdminJSON(w, map[string]any{
		"totals":               totals,
		"topClients":           topClients,
		"topRoutes":            topRoutes,
		"topBuckets":           topBuckets,
		"topNetworks":          topNetworks,
		"series":               series,
		"recent":               recent,
		"recentTotal":          total,
		"anonymousRateLimited": anonLimited,
	})
}

// --- Geography / networks / search ---

func (a *App) adminGeographyHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	from, to, _, _ := parseRange(r.URL.Query(), time.Now())
	countries, err := a.Telemetry.CountryStats(r.Context(), from, to.Add(-time.Nanosecond))
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "GEOGRAPHY_FAILED", "Failed to compute geography stats.")
		return
	}
	writeAdminJSON(w, map[string]any{"countries": countries, "note": "Country data is approximate (edge-provided)."})
}

func (a *App) adminNetworksHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	from, to, _, _ := parseRange(r.URL.Query(), time.Now())
	limit, _ := parsePagination(r.URL.Query())
	networks, err := a.Telemetry.NetworkStats(r.Context(), from, to.Add(-time.Nanosecond), limit)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "NETWORKS_FAILED", "Failed to compute network stats.")
		return
	}
	writeAdminJSON(w, map[string]any{
		"networks": networks,
		"note":     "Network identifiers are anonymous, rotating HMAC values; ASN data appears only when the edge provides it.",
	})
}

func (a *App) adminSearchAnalyticsHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	from, to, _, _ := parseRange(r.URL.Query(), time.Now())
	limit, _ := parsePagination(r.URL.Query())
	emptyOnly := r.URL.Query().Get("empty") == "true"
	terms, err := a.Telemetry.SearchStats(r.Context(), from, to.Add(-time.Nanosecond), emptyOnly, limit)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "SEARCH_FAILED", "Failed to compute search stats.")
		return
	}
	mods, _ := a.Telemetry.ModStats(r.Context(), from, to.Add(-time.Nanosecond), limit)
	writeAdminJSON(w, map[string]any{"terms": terms, "mods": mods})
}

// --- Logs ---

func (a *App) adminLogsHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	query := r.URL.Query()
	from, to, _, _ := parseRange(query, time.Now())
	filter := telemetry.LogFilter{
		From:          from,
		To:            to,
		Level:         query.Get("level"),
		Message:       query.Get("message"),
		RequestID:     query.Get("request_id"),
		TraceID:       query.Get("trace_id"),
		JobID:         query.Get("job_id"),
		Route:         query.Get("route"),
		Path:          query.Get("path"),
		ErrorCategory: query.Get("error_category"),
		CountryCode:   query.Get("country"),
		NetworkID:     query.Get("network"),
		AccountID:     query.Get("user"),
		ClientName:    query.Get("client"),
		APIKeyID:      query.Get("key"),
		CacheStatus:   query.Get("cache"),
		InstanceID:    query.Get("instance"),
		AppVersion:    query.Get("version"),
		Search:        query.Get("q"),
	}
	filter.Status, _ = strconv.Atoi(query.Get("status"))
	limit, offset := parsePagination(query)
	logs, total, err := a.Telemetry.ListLogs(r.Context(), filter, limit, offset)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "LOGS_FAILED", "Failed to search logs.")
		return
	}
	writeAdminJSON(w, map[string]any{"logs": logs, "total": total, "limit": limit, "offset": offset})
}

func (a *App) adminLogDetailHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if err != nil {
		config.WriteError(w, r, http.StatusBadRequest, "INVALID_LOG_ID", "Log id must be numeric.")
		return
	}
	logContext, ok, err := a.Telemetry.GetLogContext(r.Context(), id)
	if err != nil || !ok {
		config.WriteError(w, r, http.StatusNotFound, "LOG_NOT_FOUND", "Log entry was not found.")
		return
	}
	writeAdminJSON(w, logContext)
}

// --- Jobs ---

func (a *App) adminJobsHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	query := r.URL.Query()
	from, to, _, _ := parseRange(query, time.Now())
	filter := telemetry.JobFilter{
		From:        from,
		To:          to,
		Kind:        query.Get("kind"),
		Status:      query.Get("status"),
		ResourceKey: query.Get("resource"),
		RequestID:   query.Get("request_id"),
	}
	if minMs, err := strconv.ParseFloat(query.Get("min_ms"), 64); err == nil {
		filter.MinMs = minMs
	}
	limit, offset := parsePagination(query)
	jobs, total, err := a.Telemetry.ListJobs(r.Context(), filter, query.Get("order"), limit, offset)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "JOBS_FAILED", "Failed to list jobs.")
		return
	}
	stats, _ := a.Telemetry.JobStatsFor(r.Context(), filter)
	writeAdminJSON(w, map[string]any{
		"jobs": jobs, "total": total, "stats": stats, "gauges": a.Recorder.Gauges(),
	})
}

func (a *App) adminJobDetailHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	job, ok, err := a.Telemetry.GetJob(r.Context(), mux.Vars(r)["id"])
	if err != nil || !ok {
		config.WriteError(w, r, http.StatusNotFound, "JOB_NOT_FOUND", "Job was not found.")
		return
	}
	logs, _, _ := a.Telemetry.ListLogs(r.Context(), telemetry.LogFilter{
		From: job.EnqueuedAt.Add(-time.Hour), To: job.EnqueuedAt.Add(24 * time.Hour), JobID: job.JobID,
	}, 100, 0)
	var request *telemetry.RequestEvent
	if job.RequestID != "" {
		if found, ok, _ := a.Telemetry.GetRequest(r.Context(), job.RequestID); ok {
			request = &found
		}
	}
	writeAdminJSON(w, map[string]any{"job": job, "logs": logs, "request": request})
}

// --- Retention ---

func (a *App) adminRetentionHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	ctx := r.Context()
	from, to, _, _ := parseRange(r.URL.Query(), time.Now())
	entity := r.URL.Query().Get("entity")
	if entity == "" {
		entity = "client"
	}
	series, err := a.Telemetry.ActivitySeries(ctx, entity, from, to.Add(-time.Nanosecond))
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "RETENTION_FAILED", "Failed to compute retention.")
		return
	}
	summary, _ := a.Telemetry.RetentionSummaryFor(ctx, entity, from, to.Add(-time.Nanosecond))
	weeklyCohorts, _ := a.Telemetry.CohortRetention(ctx, entity, "weekly", 10, 8)
	monthlyCohorts, _ := a.Telemetry.CohortRetention(ctx, entity, "monthly", 6, 6)
	writeAdminJSON(w, map[string]any{
		"entity":         entity,
		"series":         series,
		"summary":        summary,
		"weeklyCohorts":  weeklyCohorts,
		"monthlyCohorts": monthlyCohorts,
		"definitions": map[string]string{
			"active": "≥1 successful request (status <500, not 401/403/429) from a non-crawler, non-monitoring, non-health source in the period.",
			"note":   "Network (anonymous) retention is estimated: identifiers rotate and must not be compared with authenticated retention.",
		},
	})
}

// --- Clients (analytics view) ---

func (a *App) adminClientsHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	from, to, _, _ := parseRange(r.URL.Query(), time.Now())
	limit, _ := parsePagination(r.URL.Query())
	stats, err := a.Telemetry.ClientStats(r.Context(), from, to.Add(-time.Nanosecond), limit)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "CLIENTS_FAILED", "Failed to compute client stats.")
		return
	}
	var registered any
	if a.BillingStore != nil {
		registered, _ = a.BillingStore.ListAPIClients(r.Context(), "", 200)
	}
	writeAdminJSON(w, map[string]any{"clients": stats, "registered": registered})
}

// --- Marketing ---

func (a *App) adminMarketingHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	ctx := r.Context()
	from, to, prevFrom, prevTo := parseRange(r.URL.Query(), time.Now())
	current, err := a.Telemetry.Totals(ctx, telemetry.RequestFilter{From: from, To: to})
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "MARKETING_FAILED", "Failed to compute marketing stats.")
		return
	}
	previous, _ := a.Telemetry.Totals(ctx, telemetry.RequestFilter{From: prevFrom, To: prevTo})
	clientSummary, _ := a.Telemetry.RetentionSummaryFor(ctx, "client", from, to.Add(-time.Nanosecond))
	userSummary, _ := a.Telemetry.RetentionSummaryFor(ctx, "user", from, to.Add(-time.Nanosecond))
	countries, _ := a.Telemetry.CountryStats(ctx, from, to.Add(-time.Nanosecond))
	topMods, _ := a.Telemetry.ModStats(ctx, from, to.Add(-time.Nanosecond), 15)
	topSearches, _ := a.Telemetry.SearchStats(ctx, from, to.Add(-time.Nanosecond), false, 15)
	crawlers, _ := a.Telemetry.TopBy(ctx, telemetry.RequestFilter{From: from, To: to, Source: telemetry.SourceCrawler}, "client", 10)
	aiCrawlers, _ := a.Telemetry.TopBy(ctx, telemetry.RequestFilter{From: from, To: to, Source: telemetry.SourceAICrawler}, "client", 10)

	var publicClients any
	if a.BillingStore != nil {
		all, _ := a.BillingStore.ListAPIClients(ctx, "", 500)
		nameable := all[:0]
		for _, client := range all {
			if client.PubliclyNamable {
				nameable = append(nameable, client)
			}
		}
		publicClients = nameable
	}
	writeAdminJSON(w, map[string]any{
		"range":              map[string]any{"from": from, "to": to},
		"totals":             current,
		"previous":           previous,
		"clientRetention":    clientSummary,
		"userRetention":      userSummary,
		"countriesReached":   len(countries),
		"countries":          countries,
		"topMods":            topMods,
		"topSearches":        topSearches,
		"crawlerDiscovery":   crawlers,
		"aiCrawlerDiscovery": aiCrawlers,
		"publiclyNameable":   publicClients,
		"estimateDisclaimer": "Anonymous figures use rotating network identifiers and are estimates.",
	})
}

// --- Export ---

// adminExportHandler streams aggregate datasets as CSV or JSON. Only
// aggregate, marketing-safe tables are exportable.
func (a *App) adminExportHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	ctx := r.Context()
	from, to, _, _ := parseRange(r.URL.Query(), time.Now())
	dataset := r.URL.Query().Get("dataset")
	format := r.URL.Query().Get("format")

	var header []string
	var rows [][]string
	switch dataset {
	case "usage":
		series, err := a.Telemetry.Timeseries(ctx, telemetry.RequestFilter{From: from, To: to}, "day", "source")
		if err != nil {
			config.WriteError(w, r, http.StatusInternalServerError, "EXPORT_FAILED", "Failed to export dataset.")
			return
		}
		header = []string{"day", "source", "requests", "errors", "rate_limited", "avg_ms"}
		for _, point := range series {
			rows = append(rows, []string{point.Bucket, point.Group,
				strconv.FormatInt(point.Requests, 10), strconv.FormatInt(point.Errors, 10),
				strconv.FormatInt(point.RateLimited, 10), fmt.Sprintf("%.2f", point.AvgMs)})
		}
	case "countries":
		countries, err := a.Telemetry.CountryStats(ctx, from, to.Add(-time.Nanosecond))
		if err != nil {
			config.WriteError(w, r, http.StatusInternalServerError, "EXPORT_FAILED", "Failed to export dataset.")
			return
		}
		header = []string{"country", "requests", "errors", "rate_limited"}
		for _, country := range countries {
			rows = append(rows, []string{country.CountryCode,
				strconv.FormatInt(country.Requests, 10), strconv.FormatInt(country.Errors, 10),
				strconv.FormatInt(country.RateLimited, 10)})
		}
	case "mods":
		mods, err := a.Telemetry.ModStats(ctx, from, to.Add(-time.Nanosecond), 500)
		if err != nil {
			config.WriteError(w, r, http.StatusInternalServerError, "EXPORT_FAILED", "Failed to export dataset.")
			return
		}
		header = []string{"mod_id", "requests"}
		for _, mod := range mods {
			rows = append(rows, []string{mod.ModID, strconv.FormatInt(mod.Requests, 10)})
		}
	case "searches":
		terms, err := a.Telemetry.SearchStats(ctx, from, to.Add(-time.Nanosecond), false, 500)
		if err != nil {
			config.WriteError(w, r, http.StatusInternalServerError, "EXPORT_FAILED", "Failed to export dataset.")
			return
		}
		header = []string{"term", "searches", "empty_results"}
		for _, term := range terms {
			rows = append(rows, []string{term.Term, strconv.FormatInt(term.Searches, 10), strconv.FormatInt(term.EmptyResults, 10)})
		}
	default:
		config.WriteError(w, r, http.StatusBadRequest, "UNKNOWN_DATASET", "dataset must be one of: usage, countries, mods, searches.")
		return
	}
	a.audit(r, "export."+dataset, "export", dataset, map[string]any{"from": from, "to": to, "format": format})

	filename := fmt.Sprintf("reforgermods-%s-%s", dataset, time.Now().UTC().Format("20060102"))
	if format == "json" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`.json"`)
		out := make([]map[string]string, 0, len(rows))
		for _, row := range rows {
			item := map[string]string{}
			for i, column := range header {
				if i < len(row) {
					item[column] = row[i]
				}
			}
			out = append(out, item)
		}
		writeAdminJSON(w, out)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`.csv"`)
	writer := csv.NewWriter(w)
	_ = writer.Write(header)
	for _, row := range rows {
		_ = writer.Write(row)
	}
	writer.Flush()
}

// --- Health ---

func (a *App) healthSummary(r *http.Request) map[string]any {
	ctx := r.Context()
	out := map[string]any{
		"version":   Version,
		"instance":  a.Config.InstanceID,
		"billing":   a.BillingStore != nil,
		"index":     a.IndexStore != nil,
		"telemetry": a.Telemetry != nil,
		"gauges":    a.Recorder.Gauges(),
		"counters":  a.Hooks.Counters(),
	}
	if a.Telemetry != nil {
		written, dropped, writeErrors, lastError := a.Recorder.Stats()
		out["recorder"] = map[string]any{
			"written": written, "dropped": dropped, "writeErrors": writeErrors, "lastError": lastError,
		}
		out["storage"] = a.Telemetry.Storage(ctx)
		out["aggregation"] = a.Aggregator.State(ctx)
		restarts, _ := a.Telemetry.RecentRestarts(ctx, time.Now().AddDate(0, 0, -7))
		out["recentRestarts"] = restarts
	}
	return out
}

func (a *App) adminHealthHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	health := a.healthSummary(r)

	// Live DB checks with latency.
	if a.Telemetry != nil {
		start := time.Now()
		var one int
		err := a.Telemetry.DB().QueryRowContext(ctx, `SELECT 1`).Scan(&one)
		health["telemetryDb"] = map[string]any{"ok": err == nil, "latencyMs": float64(time.Since(start).Microseconds()) / 1000}
	}
	if a.BillingStore != nil {
		// Billing store has no exported DB; account count doubles as a check.
		_, total, err := a.BillingStore.SearchAccounts(ctx, "", "", 1, 0)
		health["billingDb"] = map[string]any{"ok": err == nil, "accounts": total}
	}
	if a.Telemetry != nil {
		now := time.Now().UTC()
		hour, _ := a.Telemetry.Totals(ctx, telemetry.RequestFilter{From: now.Add(-time.Hour), To: now})
		health["lastHour"] = hour
	}
	writeAdminJSON(w, health)
}

// --- Audit ---

func (a *App) adminAuditHandler(w http.ResponseWriter, r *http.Request) {
	if !a.telemetryReady(w, r) {
		return
	}
	from, to, _, _ := parseRange(r.URL.Query(), time.Now())
	limit, offset := parsePagination(r.URL.Query())
	events, total, err := a.Telemetry.ListAuditEvents(r.Context(), from, to,
		r.URL.Query().Get("actor"), r.URL.Query().Get("action"), limit, offset)
	if err != nil {
		config.WriteError(w, r, http.StatusInternalServerError, "AUDIT_FAILED", "Failed to list audit events.")
		return
	}
	writeAdminJSON(w, map[string]any{"events": events, "total": total})
}
