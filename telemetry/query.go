package telemetry

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// RequestFilter is the shared filter set for raw request-event queries
// (request explorer, timeseries, performance, rate limits, drilldowns).
type RequestFilter struct {
	From          time.Time
	To            time.Time
	RouteTemplate string
	Path          string
	Method        string
	Status        int
	StatusFamily  int // 2, 3, 4, 5
	EndpointGroup string
	Source        string
	AuthType      string
	AccountID     string
	APIKeyID      string
	APIClientID   string
	ClientName    string
	ClientKind    string
	CountryCode   string
	NetworkID     string
	CacheStatus   string
	RateLimited   *bool
	MinDurationMs float64
	SearchTerm    string
	RequestID     string
	TraceID       string
	ErrorCategory string
	AppVersion    string
	Search        string // free text over path+query+user agent+client
}

func (f RequestFilter) where() (string, []any) {
	clauses := []string{"at >= ?", "at < ?"}
	args := []any{ms(f.From), ms(f.To)}
	add := func(clause string, value any) {
		clauses = append(clauses, clause)
		args = append(args, value)
	}
	if f.RouteTemplate != "" {
		add("route_template = ?", f.RouteTemplate)
	}
	if f.Path != "" {
		add("request_path LIKE ?", f.Path+"%")
	}
	if f.Method != "" {
		add("method = ?", strings.ToUpper(f.Method))
	}
	if f.Status > 0 {
		add("status = ?", f.Status)
	}
	if f.StatusFamily > 0 {
		add("status >= ?", f.StatusFamily*100)
		add("status < ?", f.StatusFamily*100+100)
	}
	if f.EndpointGroup != "" {
		add("endpoint_group = ?", f.EndpointGroup)
	}
	if f.Source != "" {
		add("source = ?", f.Source)
	}
	if f.AuthType != "" {
		add("auth_type = ?", f.AuthType)
	}
	if f.AccountID != "" {
		add("account_id = ?", f.AccountID)
	}
	if f.APIKeyID != "" {
		add("api_key_id = ?", f.APIKeyID)
	}
	if f.APIClientID != "" {
		add("api_client_id = ?", f.APIClientID)
	}
	if f.ClientName != "" {
		add("client_name = ?", f.ClientName)
	}
	if f.ClientKind != "" {
		add("client_kind = ?", f.ClientKind)
	}
	if f.CountryCode != "" {
		add("country_code = ?", strings.ToUpper(f.CountryCode))
	}
	if f.NetworkID != "" {
		add("network_id = ?", f.NetworkID)
	}
	if f.CacheStatus != "" {
		add("cache_status = ?", strings.ToUpper(f.CacheStatus))
	}
	if f.RateLimited != nil {
		add("rate_limited = ?", boolToInt(*f.RateLimited))
	}
	if f.MinDurationMs > 0 {
		add("duration_ms >= ?", f.MinDurationMs)
	}
	if f.SearchTerm != "" {
		add("search_term = ?", f.SearchTerm)
	}
	if f.RequestID != "" {
		add("request_id = ?", f.RequestID)
	}
	if f.TraceID != "" {
		add("trace_id = ?", f.TraceID)
	}
	if f.ErrorCategory != "" {
		add("error_category = ?", f.ErrorCategory)
	}
	if f.AppVersion != "" {
		add("app_version = ?", f.AppVersion)
	}
	if f.Search != "" {
		like := "%" + escapeLike(f.Search) + "%"
		clauses = append(clauses, "(request_path LIKE ? ESCAPE '\\' OR query LIKE ? ESCAPE '\\' OR user_agent LIKE ? ESCAPE '\\' OR client_name LIKE ? ESCAPE '\\')")
		args = append(args, like, like, like, like)
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

var requestEventColumns = `id, request_id, trace_id, at, method, route_template,
	request_path, query, endpoint_group, status, duration_ms, response_bytes,
	request_bytes, api_version, source, client_kind, auth_type, account_id,
	api_key_id, api_client_id, client_name, client_version, client_verified,
	user_agent, country_code, asn, network_name, network_id, is_hosting,
	via_proxy, cache_status, refresh_result, rate_limited, rate_limit_limit,
	rate_bucket, error_category, error_code, search_term, result_count, mod_id,
	app_version, instance_id`

func scanRequestEvent(rows interface{ Scan(...any) error }) (RequestEvent, error) {
	var e RequestEvent
	var at int64
	var verified, viaProxy, rateLimited int
	err := rows.Scan(&e.ID, &e.RequestID, &e.TraceID, &at, &e.Method, &e.RouteTemplate,
		&e.RequestPath, &e.Query, &e.EndpointGroup, &e.Status, &e.DurationMs, &e.ResponseBytes,
		&e.RequestBytes, &e.APIVersion, &e.Source, &e.ClientKind, &e.AuthType, &e.AccountID,
		&e.APIKeyID, &e.APIClientID, &e.ClientName, &e.ClientVersion, &verified,
		&e.UserAgent, &e.CountryCode, &e.ASN, &e.NetworkName, &e.NetworkID, &e.IsHosting,
		&viaProxy, &e.CacheStatus, &e.RefreshResult, &rateLimited, &e.RateLimitLimit,
		&e.RateBucket, &e.ErrorCategory, &e.ErrorCode, &e.SearchTerm, &e.ResultCount, &e.ModID,
		&e.AppVersion, &e.InstanceID)
	if err != nil {
		return e, err
	}
	e.At = fromMs(at)
	e.ClientVerified = verified == 1
	e.ViaProxy = viaProxy == 1
	e.RateLimited = rateLimited == 1
	return e, nil
}

// ListRequests returns a page of matching request events plus the total count.
func (s *Store) ListRequests(ctx context.Context, filter RequestFilter, orderBy string, limit int, offset int) ([]RequestEvent, int, error) {
	where, args := filter.where()
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM request_events `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "at DESC"
	switch orderBy {
	case "duration":
		order = "duration_ms DESC"
	case "oldest":
		order = "at ASC"
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+requestEventColumns+` FROM request_events `+where+
		` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(args, clampLimit(limit), maxOffset(offset))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []RequestEvent
	for rows.Next() {
		event, err := scanRequestEvent(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, event)
	}
	return out, total, rows.Err()
}

// GetRequest fetches one request event by internal ID or request ID.
func (s *Store) GetRequest(ctx context.Context, id string) (RequestEvent, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+requestEventColumns+` FROM request_events
		WHERE id = CAST(? AS INTEGER) OR request_id = ? ORDER BY at DESC LIMIT 1`, id, id)
	event, err := scanRequestEvent(row)
	if err == sql.ErrNoRows {
		return event, false, nil
	}
	if err != nil {
		return event, false, err
	}
	return event, true, nil
}

// OverviewTotals are the headline numbers for a period.
type OverviewTotals struct {
	Requests       int64            `json:"requests"`
	Served         int64            `json:"served"`
	Accepted       int64            `json:"accepted"`
	Errors         int64            `json:"errors"`
	RateLimited    int64            `json:"rateLimited"`
	UniqueAccounts int64            `json:"uniqueAccounts"`
	UniqueKeys     int64            `json:"uniqueKeys"`
	UniqueClients  int64            `json:"uniqueClients"`
	UniqueNetworks int64            `json:"uniqueNetworks"`
	DistinctMods   int64            `json:"distinctMods"`
	DistinctSearch int64            `json:"distinctSearchTerms"`
	CacheHit       int64            `json:"cacheHit"`
	CacheStale     int64            `json:"cacheStale"`
	CacheMiss      int64            `json:"cacheMiss"`
	CacheBypass    int64            `json:"cacheBypass"`
	BytesOut       int64            `json:"bytesOut"`
	BySource       map[string]int64 `json:"bySource"`
	Latency        LatencySummary   `json:"latency"`
	PeakMinute     int64            `json:"peakRequestsPerMinute"`
	PeakHour       int64            `json:"peakRequestsPerHour"`
}

type LatencySummary struct {
	Count int64   `json:"count"`
	MinMs float64 `json:"minMs"`
	MaxMs float64 `json:"maxMs"`
	AvgMs float64 `json:"avgMs"`
	P50Ms float64 `json:"p50Ms"`
	P75Ms float64 `json:"p75Ms"`
	P90Ms float64 `json:"p90Ms"`
	P95Ms float64 `json:"p95Ms"`
	P99Ms float64 `json:"p99Ms"`
}

// Totals computes the overview numbers for a period from raw events.
func (s *Store) Totals(ctx context.Context, filter RequestFilter) (OverviewTotals, error) {
	where, args := filter.where()
	out := OverviewTotals{BySource: map[string]int64{}}
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(1),
		COALESCE(SUM(CASE WHEN status < 400 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = 202 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status >= 400 AND status <> 429 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN rate_limited = 1 OR status = 429 THEN 1 ELSE 0 END), 0),
		COUNT(DISTINCT CASE WHEN account_id <> '' THEN account_id END),
		COUNT(DISTINCT CASE WHEN api_key_id <> '' THEN api_key_id END),
		COUNT(DISTINCT CASE WHEN network_id <> '' THEN network_id END),
		COUNT(DISTINCT CASE WHEN mod_id <> '' THEN mod_id END),
		COUNT(DISTINCT CASE WHEN search_term <> '' THEN search_term END),
		COALESCE(SUM(CASE WHEN cache_status = 'HIT' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN cache_status = 'STALE' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN cache_status = 'MISS' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN cache_status = 'BYPASS' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(response_bytes), 0)
		FROM request_events `+where, args...)
	if err := row.Scan(&out.Requests, &out.Served, &out.Accepted, &out.Errors, &out.RateLimited,
		&out.UniqueAccounts, &out.UniqueKeys, &out.UniqueNetworks, &out.DistinctMods, &out.DistinctSearch,
		&out.CacheHit, &out.CacheStale, &out.CacheMiss, &out.CacheBypass, &out.BytesOut); err != nil {
		return out, err
	}

	uniqueClients := 0
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT CASE
			WHEN api_client_id <> '' THEN 'client:' || api_client_id
			WHEN api_key_id <> '' THEN 'account:' || account_id
			WHEN client_name NOT IN ('', 'browser', 'unknown') THEN 'ua:' || client_name
		END) FROM request_events `+where, args...).Scan(&uniqueClients); err != nil {
		return out, err
	}
	out.UniqueClients = int64(uniqueClients)

	sourceRows, err := s.db.QueryContext(ctx, `SELECT source, COUNT(1) FROM request_events `+where+` GROUP BY source`, args...)
	if err != nil {
		return out, err
	}
	defer sourceRows.Close()
	for sourceRows.Next() {
		var source string
		var count int64
		if err := sourceRows.Scan(&source, &count); err != nil {
			return out, err
		}
		out.BySource[source] = count
	}

	latency, err := s.Latency(ctx, filter)
	if err != nil {
		return out, err
	}
	out.Latency = latency

	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(n), 0) FROM (
		SELECT COUNT(1) AS n FROM request_events `+where+` GROUP BY at / 60000)`, args...).Scan(&out.PeakMinute); err != nil {
		return out, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(n), 0) FROM (
		SELECT COUNT(1) AS n FROM request_events `+where+` GROUP BY at / 3600000)`, args...).Scan(&out.PeakHour); err != nil {
		return out, err
	}
	return out, nil
}

// Latency computes exact latency percentiles for the filtered events.
func (s *Store) Latency(ctx context.Context, filter RequestFilter) (LatencySummary, error) {
	where, args := filter.where()
	rows, err := s.db.QueryContext(ctx, `SELECT duration_ms FROM request_events `+where, args...)
	if err != nil {
		return LatencySummary{}, err
	}
	defer rows.Close()
	var durations []float64
	for rows.Next() {
		var d float64
		if err := rows.Scan(&d); err != nil {
			return LatencySummary{}, err
		}
		durations = append(durations, d)
	}
	if err := rows.Err(); err != nil {
		return LatencySummary{}, err
	}
	stats := durationStats(durations)
	out := LatencySummary{
		Count: stats.count, MinMs: stats.min, MaxMs: stats.max,
		P50Ms: stats.p50, P75Ms: stats.p75, P90Ms: stats.p90,
		P95Ms: stats.p95, P99Ms: stats.p99,
	}
	if stats.count > 0 {
		out.AvgMs = stats.sum / float64(stats.count)
	}
	return out, nil
}

// TimeseriesPoint is one interval of a grouped series.
type TimeseriesPoint struct {
	Bucket      string  `json:"bucket"`
	Group       string  `json:"group,omitempty"`
	Requests    int64   `json:"requests"`
	Errors      int64   `json:"errors"`
	RateLimited int64   `json:"rateLimited"`
	AvgMs       float64 `json:"avgMs"`
	P95Ms       float64 `json:"p95Ms"`
	CacheHit    int64   `json:"cacheHit"`
	CacheStale  int64   `json:"cacheStale"`
	CacheMiss   int64   `json:"cacheMiss"`
	Uniques     int64   `json:"uniques"`
}

var intervalFormats = map[string]string{
	"minute": "%Y-%m-%dT%H:%M",
	"hour":   "%Y-%m-%dT%H",
	"day":    "%Y-%m-%d",
	"week":   "%Y-W%W",
	"month":  "%Y-%m",
	"year":   "%Y",
}

var groupColumns = map[string]string{
	"source":         "source",
	"endpoint_group": "endpoint_group",
	"route":          "route_template",
	"method":         "method",
	"status_family":  "CAST(status / 100 AS TEXT) || 'xx'",
	"status":         "CAST(status AS TEXT)",
	"country":        "country_code",
	"cache":          "cache_status",
	"auth":           "auth_type",
	"client_kind":    "client_kind",
	"client":         "client_name",
	"version":        "app_version",
}

// Timeseries buckets filtered raw events by interval and optional dimension.
// P95 per point is computed exactly in Go from per-point durations.
func (s *Store) Timeseries(ctx context.Context, filter RequestFilter, interval string, groupBy string) ([]TimeseriesPoint, error) {
	format, ok := intervalFormats[interval]
	if !ok {
		format = intervalFormats["day"]
	}
	groupExpr := "''"
	if column, ok := groupColumns[groupBy]; ok {
		groupExpr = column
	}
	bucketExpr := fmt.Sprintf("strftime('%s', at / 1000, 'unixepoch')", format)
	rows, err := func() (*sql.Rows, error) {
		where, args := filter.where()
		return s.db.QueryContext(ctx, `SELECT `+bucketExpr+` AS bucket, `+groupExpr+` AS grp,
			COUNT(1),
			COALESCE(SUM(CASE WHEN status >= 400 AND status <> 429 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN rate_limited = 1 OR status = 429 THEN 1 ELSE 0 END), 0),
			COALESCE(AVG(duration_ms), 0),
			COALESCE(SUM(CASE WHEN cache_status = 'HIT' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN cache_status = 'STALE' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN cache_status = 'MISS' THEN 1 ELSE 0 END), 0),
			COUNT(DISTINCT CASE WHEN network_id <> '' THEN network_id END)
			FROM request_events `+where+` GROUP BY bucket, grp ORDER BY bucket`, args...)
	}()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TimeseriesPoint
	for rows.Next() {
		var p TimeseriesPoint
		if err := rows.Scan(&p.Bucket, &p.Group, &p.Requests, &p.Errors, &p.RateLimited,
			&p.AvgMs, &p.CacheHit, &p.CacheStale, &p.CacheMiss, &p.Uniques); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// TopItem is a generic named count for top-N widgets, with drilldown key.
type TopItem struct {
	Key      string  `json:"key"`
	Label    string  `json:"label"`
	Count    int64   `json:"count"`
	Extra    float64 `json:"extra,omitempty"`
	ExtraStr string  `json:"extraStr,omitempty"`
}

// TopBy groups filtered events by a dimension and returns the top N.
func (s *Store) TopBy(ctx context.Context, filter RequestFilter, dimension string, limit int) ([]TopItem, error) {
	column, ok := groupColumns[dimension]
	if !ok {
		switch dimension {
		case "path":
			column = "request_path"
		case "user_agent":
			column = "user_agent"
		case "network":
			column = "network_id"
		case "account":
			column = "account_id"
		case "api_key":
			column = "api_key_id"
		case "mod":
			column = "mod_id"
		case "search_term":
			column = "search_term"
		case "error_code":
			column = "error_code"
		case "rate_bucket":
			column = "rate_bucket"
		default:
			return nil, fmt.Errorf("unknown dimension %q", dimension)
		}
	}
	where, args := filter.where()
	rows, err := s.db.QueryContext(ctx, `SELECT `+column+` AS k, COUNT(1) AS n, COALESCE(AVG(duration_ms), 0)
		FROM request_events `+where+` AND `+column+` <> '' GROUP BY k ORDER BY n DESC LIMIT ?`,
		append(args, clampLimit(limit))...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TopItem
	for rows.Next() {
		var item TopItem
		if err := rows.Scan(&item.Key, &item.Count, &item.Extra); err != nil {
			return nil, err
		}
		item.Label = item.Key
		out = append(out, item)
	}
	return out, rows.Err()
}

// SlowestEndpoints ranks route templates by average duration for a period.
func (s *Store) SlowestEndpoints(ctx context.Context, filter RequestFilter, limit int) ([]TopItem, error) {
	where, args := filter.where()
	rows, err := s.db.QueryContext(ctx, `SELECT method || ' ' || route_template AS k,
		COUNT(1) AS n, AVG(duration_ms) AS avg_ms
		FROM request_events `+where+` AND route_template <> ''
		GROUP BY k HAVING n >= 3 ORDER BY avg_ms DESC LIMIT ?`,
		append(args, clampLimit(limit))...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TopItem
	for rows.Next() {
		var item TopItem
		if err := rows.Scan(&item.Key, &item.Count, &item.Extra); err != nil {
			return nil, err
		}
		item.Label = item.Key
		out = append(out, item)
	}
	return out, rows.Err()
}

// EndpointStat is one route's aggregate row for the endpoints page.
type EndpointStat struct {
	RouteTemplate  string    `json:"routeTemplate"`
	Method         string    `json:"method"`
	Requests       int64     `json:"requests"`
	Served         int64     `json:"served"`
	Errors         int64     `json:"errors"`
	RateLimited    int64     `json:"rateLimited"`
	UniqueAccounts int64     `json:"uniqueAccounts"`
	UniqueKeys     int64     `json:"uniqueKeys"`
	UniqueNetworks int64     `json:"uniqueNetworks"`
	CacheHit       int64     `json:"cacheHit"`
	CacheStale     int64     `json:"cacheStale"`
	CacheMiss      int64     `json:"cacheMiss"`
	BytesOut       int64     `json:"bytesOut"`
	AvgMs          float64   `json:"avgMs"`
	MinMs          float64   `json:"minMs"`
	MaxMs          float64   `json:"maxMs"`
	P50Ms          float64   `json:"p50Ms"`
	P90Ms          float64   `json:"p90Ms"`
	P95Ms          float64   `json:"p95Ms"`
	P99Ms          float64   `json:"p99Ms"`
	FirstAt        time.Time `json:"firstAt"`
	LastAt         time.Time `json:"lastAt"`
}

// EndpointStats computes per-endpoint metrics from raw events for the range.
// Percentiles are exact (in-memory per endpoint).
func (s *Store) EndpointStats(ctx context.Context, filter RequestFilter) ([]EndpointStat, error) {
	where, args := filter.where()
	rows, err := s.db.QueryContext(ctx, `SELECT route_template, method, status,
		duration_ms, response_bytes, account_id, api_key_id, network_id,
		cache_status, rate_limited, at
		FROM request_events `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type acc struct {
		stat      EndpointStat
		durations []float64
		accounts  map[string]struct{}
		keys      map[string]struct{}
		networks  map[string]struct{}
		firstAt   int64
		lastAt    int64
	}
	byKey := map[[2]string]*acc{}
	for rows.Next() {
		var route, method, accountID, keyID, networkID, cacheStatus string
		var status int
		var rateLimited int
		var duration float64
		var bytesOut, at int64
		if err := rows.Scan(&route, &method, &status, &duration, &bytesOut,
			&accountID, &keyID, &networkID, &cacheStatus, &rateLimited, &at); err != nil {
			return nil, err
		}
		key := [2]string{route, method}
		a := byKey[key]
		if a == nil {
			a = &acc{
				stat:     EndpointStat{RouteTemplate: route, Method: method},
				accounts: map[string]struct{}{}, keys: map[string]struct{}{}, networks: map[string]struct{}{},
			}
			byKey[key] = a
		}
		a.stat.Requests++
		if status < 400 {
			a.stat.Served++
		}
		if status >= 400 && status != 429 {
			a.stat.Errors++
		}
		if rateLimited == 1 || status == 429 {
			a.stat.RateLimited++
		}
		switch cacheStatus {
		case CacheHit:
			a.stat.CacheHit++
		case CacheStale:
			a.stat.CacheStale++
		case CacheMiss:
			a.stat.CacheMiss++
		}
		a.stat.BytesOut += bytesOut
		if accountID != "" {
			a.accounts[accountID] = struct{}{}
		}
		if keyID != "" {
			a.keys[keyID] = struct{}{}
		}
		if networkID != "" {
			a.networks[networkID] = struct{}{}
		}
		a.durations = append(a.durations, duration)
		if a.firstAt == 0 || at < a.firstAt {
			a.firstAt = at
		}
		if at > a.lastAt {
			a.lastAt = at
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]EndpointStat, 0, len(byKey))
	for _, a := range byKey {
		stats := durationStats(a.durations)
		a.stat.UniqueAccounts = int64(len(a.accounts))
		a.stat.UniqueKeys = int64(len(a.keys))
		a.stat.UniqueNetworks = int64(len(a.networks))
		a.stat.MinMs, a.stat.MaxMs = stats.min, stats.max
		a.stat.P50Ms, a.stat.P90Ms, a.stat.P95Ms, a.stat.P99Ms = stats.p50, stats.p90, stats.p95, stats.p99
		if stats.count > 0 {
			a.stat.AvgMs = stats.sum / float64(stats.count)
		}
		a.stat.FirstAt, a.stat.LastAt = fromMs(a.firstAt), fromMs(a.lastAt)
		out = append(out, a.stat)
	}
	sort.Slice(out, func(i int, j int) bool { return out[i].Requests > out[j].Requests })
	return out, nil
}
