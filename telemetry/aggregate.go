package telemetry

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Aggregator turns raw request events into the hourly/daily rollup tables.
// Every pass re-aggregates all non-final buckets by DELETE+INSERT inside one
// transaction per bucket, so runs are idempotent and safe to repeat. An hour
// becomes final 5 minutes after it ends, a day 15 minutes after UTC midnight;
// the watermarks in aggregation_state track the last final bucket. Historical
// imports call RebuildRange for the imported window.
type Aggregator struct {
	store *Store
	now   func() time.Time
}

const (
	stateHourWatermark = "hourly_watermark"
	stateDayWatermark  = "daily_watermark"
	stateLastRun       = "last_aggregation_run"
	stateLastError     = "last_aggregation_error"
	stateLastPrune     = "last_prune_run"

	hourFinalDelay = 5 * time.Minute
	dayFinalDelay  = 15 * time.Minute
)

func NewAggregator(store *Store) *Aggregator {
	return &Aggregator{store: store, now: time.Now}
}

// Run performs one aggregation pass. It is called on a ticker and after
// imports; concurrent runs are prevented by the caller.
func (a *Aggregator) Run(ctx context.Context) error {
	now := a.now().UTC()
	if err := a.runHourly(ctx, now); err != nil {
		a.setState(ctx, stateLastError, fmt.Sprintf("hourly: %v", err))
		return err
	}
	if err := a.runDaily(ctx, now); err != nil {
		a.setState(ctx, stateLastError, fmt.Sprintf("daily: %v", err))
		return err
	}
	a.setState(ctx, stateLastRun, now.Format(time.RFC3339))
	a.setState(ctx, stateLastError, "")
	return nil
}

func (a *Aggregator) runHourly(ctx context.Context, now time.Time) error {
	current := now.Truncate(time.Hour)
	start, err := a.resumePoint(ctx, stateHourWatermark, "2006-01-02T15", current, func(t time.Time) time.Time {
		return t.Add(time.Hour)
	})
	if err != nil || start.IsZero() {
		return err
	}
	for hour := start; !hour.After(current); hour = hour.Add(time.Hour) {
		if err := a.aggregateUsageBucket(ctx, "usage_hourly", HourKey(hour), hour, hour.Add(time.Hour)); err != nil {
			return err
		}
	}
	final := now.Add(-hourFinalDelay).Truncate(time.Hour).Add(-time.Hour)
	a.setState(ctx, stateHourWatermark, final.Format("2006-01-02T15"))
	return nil
}

func (a *Aggregator) runDaily(ctx context.Context, now time.Time) error {
	current := dayStart(now)
	start, err := a.resumePoint(ctx, stateDayWatermark, "2006-01-02", current, func(t time.Time) time.Time {
		return t.AddDate(0, 0, 1)
	})
	if err != nil || start.IsZero() {
		return err
	}
	for day := start; !day.After(current); day = day.AddDate(0, 0, 1) {
		if err := a.aggregateDay(ctx, day); err != nil {
			return err
		}
	}
	final := dayStart(now.Add(-dayFinalDelay)).AddDate(0, 0, -1)
	a.setState(ctx, stateDayWatermark, final.Format("2006-01-02"))
	return nil
}

// resumePoint returns the first bucket to (re)aggregate: the bucket after the
// stored watermark, or the earliest raw event when no watermark exists.
// The zero time means there is nothing to do.
func (a *Aggregator) resumePoint(ctx context.Context, stateKey string, layout string, current time.Time, next func(time.Time) time.Time) (time.Time, error) {
	if value := a.getState(ctx, stateKey); value != "" {
		parsed, err := time.ParseInLocation(layout, value, time.UTC)
		if err == nil {
			start := next(parsed)
			if start.After(current) {
				return current, nil // still re-aggregate the current bucket
			}
			return start, nil
		}
	}
	var earliest sql.NullInt64
	if err := a.store.db.QueryRowContext(ctx, `SELECT MIN(at) FROM request_events`).Scan(&earliest); err != nil {
		return time.Time{}, err
	}
	if !earliest.Valid {
		return time.Time{}, nil
	}
	first, err := time.ParseInLocation(layout, fromMs(earliest.Int64).Format(layout), time.UTC)
	if err != nil {
		return time.Time{}, err
	}
	return first, nil
}

// RebuildRange re-aggregates every bucket intersecting [from, to] and rolls
// the watermarks back so the ticker continues from the rebuilt range.
func (a *Aggregator) RebuildRange(ctx context.Context, from time.Time, to time.Time) error {
	from, to = from.UTC(), to.UTC()
	for hour := from.Truncate(time.Hour); !hour.After(to); hour = hour.Add(time.Hour) {
		if err := a.aggregateUsageBucket(ctx, "usage_hourly", HourKey(hour), hour, hour.Add(time.Hour)); err != nil {
			return err
		}
	}
	for day := dayStart(from); !day.After(to); day = day.AddDate(0, 0, 1) {
		if err := a.aggregateDay(ctx, day); err != nil {
			return err
		}
	}
	return nil
}

// RebuildAll clears every aggregate table and rebuilds from raw events.
func (a *Aggregator) RebuildAll(ctx context.Context) error {
	tables := []string{
		"usage_hourly", "usage_daily", "endpoint_daily", "client_daily",
		"country_daily", "network_daily", "search_daily", "mod_daily",
		"entity_activity", "entity_profiles",
	}
	for _, table := range tables {
		if _, err := a.store.db.ExecContext(ctx, `DELETE FROM `+table); err != nil {
			return err
		}
	}
	for _, key := range []string{stateHourWatermark, stateDayWatermark} {
		a.setState(ctx, key, "")
	}
	return a.Run(ctx)
}

// usageRow accumulates one (source × endpoint_group) dimension combination.
type usageRow struct {
	requests, served, accepted, errors, rateLimited int64
	cacheHit, cacheStale, cacheMiss, cacheBypass    int64
	bytesOut                                        int64
	networks, accounts, keys                        map[string]struct{}
	durations                                       []float64
}

func newUsageRow() *usageRow {
	return &usageRow{
		networks: map[string]struct{}{},
		accounts: map[string]struct{}{},
		keys:     map[string]struct{}{},
	}
}

func (u *usageRow) add(e *rawEvent) {
	u.requests++
	if e.status < 400 {
		u.served++
	}
	if e.status == http.StatusAccepted {
		u.accepted++
	}
	if e.status >= 400 && e.status != http.StatusTooManyRequests {
		u.errors++
	}
	if e.rateLimited || e.status == http.StatusTooManyRequests {
		u.rateLimited++
	}
	switch e.cacheStatus {
	case CacheHit:
		u.cacheHit++
	case CacheStale:
		u.cacheStale++
	case CacheMiss:
		u.cacheMiss++
	case CacheBypass:
		u.cacheBypass++
	}
	u.bytesOut += e.responseBytes
	if e.networkID != "" {
		u.networks[e.networkID] = struct{}{}
	}
	if e.accountID != "" {
		u.accounts[e.accountID] = struct{}{}
	}
	if e.apiKeyID != "" {
		u.keys[e.apiKeyID] = struct{}{}
	}
	u.durations = append(u.durations, e.durationMs)
}

type rawEvent struct {
	at            int64
	method        string
	routeTemplate string
	endpointGroup string
	status        int
	durationMs    float64
	responseBytes int64
	source        string
	accountID     string
	apiKeyID      string
	apiClientID   string
	clientName    string
	clientVersion string
	verified      bool
	countryCode   string
	asn           string
	networkName   string
	networkID     string
	isHosting     string
	cacheStatus   string
	rateLimited   bool
	searchTerm    string
	resultCount   int
	modID         string
}

func (a *Aggregator) loadEvents(ctx context.Context, from time.Time, to time.Time) ([]*rawEvent, error) {
	rows, err := a.store.db.QueryContext(ctx, `SELECT at, method, route_template,
		endpoint_group, status, duration_ms, response_bytes, source, account_id,
		api_key_id, api_client_id, client_name, client_version, client_verified,
		country_code, asn, network_name, network_id, is_hosting, cache_status,
		rate_limited, search_term, result_count, mod_id
		FROM request_events WHERE at >= ? AND at < ?`, ms(from), ms(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*rawEvent
	for rows.Next() {
		e := &rawEvent{}
		var verified, rateLimited int
		if err := rows.Scan(&e.at, &e.method, &e.routeTemplate, &e.endpointGroup,
			&e.status, &e.durationMs, &e.responseBytes, &e.source, &e.accountID,
			&e.apiKeyID, &e.apiClientID, &e.clientName, &e.clientVersion, &verified,
			&e.countryCode, &e.asn, &e.networkName, &e.networkID, &e.isHosting,
			&e.cacheStatus, &rateLimited, &e.searchTerm, &e.resultCount, &e.modID); err != nil {
			return nil, err
		}
		e.verified = verified == 1
		e.rateLimited = rateLimited == 1
		out = append(out, e)
	}
	return out, rows.Err()
}

func (a *Aggregator) aggregateUsageBucket(ctx context.Context, table string, bucket string, from time.Time, to time.Time) error {
	events, err := a.loadEvents(ctx, from, to)
	if err != nil {
		return err
	}
	usage := map[[2]string]*usageRow{}
	for _, e := range events {
		key := [2]string{e.source, e.endpointGroup}
		row := usage[key]
		if row == nil {
			row = newUsageRow()
			usage[key] = row
		}
		row.add(e)
	}
	tx, err := a.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE bucket = ?`, bucket); err != nil {
		return err
	}
	for key, row := range usage {
		stats := durationStats(row.durations)
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+table+` (
			bucket, source, endpoint_group, requests, served, accepted, errors,
			rate_limited, cache_hit, cache_stale, cache_miss, cache_bypass,
			unique_networks, unique_accounts, unique_keys, bytes_out,
			dur_count, dur_sum_ms, dur_min_ms, dur_max_ms,
			p50_ms, p75_ms, p90_ms, p95_ms, p99_ms
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			bucket, key[0], key[1], row.requests, row.served, row.accepted, row.errors,
			row.rateLimited, row.cacheHit, row.cacheStale, row.cacheMiss, row.cacheBypass,
			len(row.networks), len(row.accounts), len(row.keys), row.bytesOut,
			stats.count, stats.sum, stats.min, stats.max,
			stats.p50, stats.p75, stats.p90, stats.p95, stats.p99); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// aggregateDay rebuilds every daily table for one UTC day.
func (a *Aggregator) aggregateDay(ctx context.Context, day time.Time) error {
	from, to := day, day.AddDate(0, 0, 1)
	dayKey := DayKey(day)
	events, err := a.loadEvents(ctx, from, to)
	if err != nil {
		return err
	}

	usage := map[[2]string]*usageRow{}
	type endpointAgg struct {
		usageRow
		firstAt, lastAt int64
	}
	endpoints := map[[2]string]*endpointAgg{}
	type clientAgg struct {
		apiClientID, clientName, lastVersion string
		verified                             bool
		requests, errors, rateLimited        int64
		accepted                             int64
		cacheHit, cacheStale, cacheMiss      int64
		countries                            map[string]struct{}
	}
	clients := map[string]*clientAgg{}
	type countryAgg struct {
		requests, errors, rateLimited int64
		networks                      map[string]struct{}
		durCount                      int64
		durSum                        float64
	}
	countries := map[string]*countryAgg{}
	type networkAgg struct {
		country, asn, name, hosting   string
		requests, errors, rateLimited int64
	}
	networks := map[string]*networkAgg{}
	type searchAgg struct {
		searches, empty, errors, cacheHit int64
		durCount                          int64
		durSum                            float64
	}
	searches := map[string]*searchAgg{}
	type modAgg struct {
		requests int64
		networks map[string]struct{}
	}
	mods := map[string]*modAgg{}
	type activityAgg struct{ requests, errors int64 }
	activity := map[[2]string]*activityAgg{}
	type profileMeta struct {
		at                           int64
		country, clientName, version string
	}
	profiles := map[[2]string]*profileMeta{}

	isError := func(e *rawEvent) bool {
		return e.status >= 400 && e.status != http.StatusTooManyRequests
	}
	recordActivity := func(entityType string, entityID string, e *rawEvent) {
		if entityID == "" {
			return
		}
		key := [2]string{entityType, entityID}
		row := activity[key]
		if row == nil {
			row = &activityAgg{}
			activity[key] = row
		}
		row.requests++
		if isError(e) {
			row.errors++
		}
		meta := profiles[key]
		if meta == nil || e.at > meta.at {
			profiles[key] = &profileMeta{at: e.at, country: e.countryCode, clientName: e.clientName, version: e.clientVersion}
		}
	}

	for _, e := range events {
		key := [2]string{e.source, e.endpointGroup}
		row := usage[key]
		if row == nil {
			row = newUsageRow()
			usage[key] = row
		}
		row.add(e)

		endpointKey := [2]string{e.routeTemplate, e.method}
		endpoint := endpoints[endpointKey]
		if endpoint == nil {
			endpoint = &endpointAgg{usageRow: *newUsageRow()}
			endpoints[endpointKey] = endpoint
		}
		endpoint.add(e)
		if endpoint.firstAt == 0 || e.at < endpoint.firstAt {
			endpoint.firstAt = e.at
		}
		if e.at > endpoint.lastAt {
			endpoint.lastAt = e.at
		}

		clientKey := clientKeyFor(e)
		if clientKey != "" {
			client := clients[clientKey]
			if client == nil {
				client = &clientAgg{countries: map[string]struct{}{}}
				clients[clientKey] = client
			}
			client.apiClientID = e.apiClientID
			client.clientName = e.clientName
			client.verified = client.verified || e.verified
			if e.clientVersion != "" {
				client.lastVersion = e.clientVersion
			}
			client.requests++
			if isError(e) {
				client.errors++
			}
			if e.rateLimited || e.status == http.StatusTooManyRequests {
				client.rateLimited++
			}
			if e.status == http.StatusAccepted {
				client.accepted++
			}
			switch e.cacheStatus {
			case CacheHit:
				client.cacheHit++
			case CacheStale:
				client.cacheStale++
			case CacheMiss:
				client.cacheMiss++
			}
			if e.countryCode != "" {
				client.countries[e.countryCode] = struct{}{}
			}
		}

		country := countries[e.countryCode]
		if country == nil {
			country = &countryAgg{networks: map[string]struct{}{}}
			countries[e.countryCode] = country
		}
		country.requests++
		if isError(e) {
			country.errors++
		}
		if e.rateLimited || e.status == http.StatusTooManyRequests {
			country.rateLimited++
		}
		if e.networkID != "" {
			country.networks[e.networkID] = struct{}{}
		}
		country.durCount++
		country.durSum += e.durationMs

		if e.networkID != "" {
			network := networks[e.networkID]
			if network == nil {
				network = &networkAgg{country: e.countryCode, asn: e.asn, name: e.networkName, hosting: e.isHosting}
				networks[e.networkID] = network
			}
			network.requests++
			if isError(e) {
				network.errors++
			}
			if e.rateLimited || e.status == http.StatusTooManyRequests {
				network.rateLimited++
			}
		}

		if e.searchTerm != "" && e.endpointGroup == "search" {
			search := searches[e.searchTerm]
			if search == nil {
				search = &searchAgg{}
				searches[e.searchTerm] = search
			}
			search.searches++
			if e.resultCount == 0 {
				search.empty++
			}
			if isError(e) {
				search.errors++
			}
			if e.cacheStatus == CacheHit || e.cacheStatus == CacheStale {
				search.cacheHit++
			}
			search.durCount++
			search.durSum += e.durationMs
		}

		if e.modID != "" {
			mod := mods[e.modID]
			if mod == nil {
				mod = &modAgg{networks: map[string]struct{}{}}
				mods[e.modID] = mod
			}
			mod.requests++
			if e.networkID != "" {
				mod.networks[e.networkID] = struct{}{}
			}
		}

		// Activity and retention only count real usage per the documented
		// "active" definition.
		if CountsAsActivity(e.source, e.status, e.rateLimited) {
			recordActivity("user", e.accountID, e)
			recordActivity("key", e.apiKeyID, e)
			if clientKey != "" && (e.apiClientID != "" || e.verified || e.apiKeyID != "") {
				recordActivity("client", clientKey, e)
			}
			recordActivity("network", e.networkID, e)
		}
	}

	tx, err := a.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM usage_daily WHERE bucket = ?`, dayKey); err != nil {
		return err
	}
	for key, row := range usage {
		stats := durationStats(row.durations)
		if _, err := tx.ExecContext(ctx, `INSERT INTO usage_daily (
			bucket, source, endpoint_group, requests, served, accepted, errors,
			rate_limited, cache_hit, cache_stale, cache_miss, cache_bypass,
			unique_networks, unique_accounts, unique_keys, bytes_out,
			dur_count, dur_sum_ms, dur_min_ms, dur_max_ms,
			p50_ms, p75_ms, p90_ms, p95_ms, p99_ms
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			dayKey, key[0], key[1], row.requests, row.served, row.accepted, row.errors,
			row.rateLimited, row.cacheHit, row.cacheStale, row.cacheMiss, row.cacheBypass,
			len(row.networks), len(row.accounts), len(row.keys), row.bytesOut,
			stats.count, stats.sum, stats.min, stats.max,
			stats.p50, stats.p75, stats.p90, stats.p95, stats.p99); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM endpoint_daily WHERE day = ?`, dayKey); err != nil {
		return err
	}
	for key, endpoint := range endpoints {
		stats := durationStats(endpoint.durations)
		if _, err := tx.ExecContext(ctx, `INSERT INTO endpoint_daily (
			day, route_template, method, requests, served, errors, rate_limited,
			unique_accounts, unique_keys, unique_networks,
			cache_hit, cache_stale, cache_miss, bytes_out,
			dur_count, dur_sum_ms, dur_min_ms, dur_max_ms,
			p50_ms, p90_ms, p95_ms, p99_ms, first_at, last_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			dayKey, key[0], key[1], endpoint.requests, endpoint.served, endpoint.errors, endpoint.rateLimited,
			len(endpoint.accounts), len(endpoint.keys), len(endpoint.networks),
			endpoint.cacheHit, endpoint.cacheStale, endpoint.cacheMiss, endpoint.bytesOut,
			stats.count, stats.sum, stats.min, stats.max,
			stats.p50, stats.p90, stats.p95, stats.p99, endpoint.firstAt, endpoint.lastAt); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM client_daily WHERE day = ?`, dayKey); err != nil {
		return err
	}
	for clientKey, client := range clients {
		if _, err := tx.ExecContext(ctx, `INSERT INTO client_daily (
			day, client_key, api_client_id, client_name, verified, requests,
			errors, rate_limited, accepted, cache_hit, cache_stale, cache_miss,
			countries, last_version
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			dayKey, clientKey, client.apiClientID, client.clientName, boolToInt(client.verified),
			client.requests, client.errors, client.rateLimited, client.accepted,
			client.cacheHit, client.cacheStale, client.cacheMiss,
			joinKeys(client.countries), client.lastVersion); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM country_daily WHERE day = ?`, dayKey); err != nil {
		return err
	}
	for code, country := range countries {
		if _, err := tx.ExecContext(ctx, `INSERT INTO country_daily (
			day, country_code, requests, errors, rate_limited, unique_networks,
			dur_count, dur_sum_ms
		) VALUES (?,?,?,?,?,?,?,?)`,
			dayKey, code, country.requests, country.errors, country.rateLimited,
			len(country.networks), country.durCount, country.durSum); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM network_daily WHERE day = ?`, dayKey); err != nil {
		return err
	}
	for id, network := range networks {
		if _, err := tx.ExecContext(ctx, `INSERT INTO network_daily (
			day, network_id, country_code, asn, network_name, is_hosting,
			requests, errors, rate_limited
		) VALUES (?,?,?,?,?,?,?,?,?)`,
			dayKey, id, network.country, network.asn, network.name, network.hosting,
			network.requests, network.errors, network.rateLimited); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM search_daily WHERE day = ?`, dayKey); err != nil {
		return err
	}
	for term, search := range searches {
		if _, err := tx.ExecContext(ctx, `INSERT INTO search_daily (
			day, term, searches, empty_results, errors, dur_count, dur_sum_ms, cache_hit
		) VALUES (?,?,?,?,?,?,?,?)`,
			dayKey, term, search.searches, search.empty, search.errors,
			search.durCount, search.durSum, search.cacheHit); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM mod_daily WHERE day = ?`, dayKey); err != nil {
		return err
	}
	for id, mod := range mods {
		if _, err := tx.ExecContext(ctx, `INSERT INTO mod_daily (day, mod_id, requests, unique_networks)
			VALUES (?,?,?,?)`, dayKey, id, mod.requests, len(mod.networks)); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM entity_activity WHERE day = ?`, dayKey); err != nil {
		return err
	}
	for key, row := range activity {
		if _, err := tx.ExecContext(ctx, `INSERT INTO entity_activity (
			entity_type, entity_id, day, requests, errors
		) VALUES (?,?,?,?,?)`, key[0], key[1], dayKey, row.requests, row.errors); err != nil {
			return err
		}
	}
	// Profiles are derived from entity_activity plus day metadata, so a
	// rebuild reproduces them exactly.
	for key, meta := range profiles {
		if _, err := tx.ExecContext(ctx, `INSERT INTO entity_profiles (
			entity_type, entity_id, first_seen_at, last_seen_at, days_active,
			last_country, last_client_name, last_version
		)
		SELECT entity_type, entity_id,
			MIN(CAST(strftime('%s', day || 'T00:00:00Z') AS INTEGER) * 1000),
			MAX(CAST(strftime('%s', day || 'T00:00:00Z') AS INTEGER) * 1000),
			COUNT(*), ?, ?, ?
		FROM entity_activity WHERE entity_type = ? AND entity_id = ?
		GROUP BY entity_type, entity_id
		ON CONFLICT(entity_type, entity_id) DO UPDATE SET
			first_seen_at=excluded.first_seen_at,
			last_seen_at=excluded.last_seen_at,
			days_active=excluded.days_active,
			last_country=excluded.last_country,
			last_client_name=excluded.last_client_name,
			last_version=excluded.last_version`,
			meta.country, meta.clientName, meta.version, key[0], key[1]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// clientKeyFor identifies a client for analytics: registered API clients by
// ID, API-key traffic without a client by account, otherwise the self-reported
// or UA-derived client name (prefixed so the namespaces cannot collide).
func clientKeyFor(e *rawEvent) string {
	switch {
	case e.apiClientID != "":
		return "client:" + e.apiClientID
	case e.apiKeyID != "":
		return "account:" + e.accountID
	case e.clientName != "" && e.clientName != "browser" && e.clientName != "unknown":
		return "ua:" + e.clientName
	default:
		return ""
	}
}

type durStats struct {
	count                   int64
	sum, min, max           float64
	p50, p75, p90, p95, p99 float64
}

// durationStats computes exact percentiles (nearest-rank) for one bucket.
func durationStats(durations []float64) durStats {
	out := durStats{count: int64(len(durations))}
	if len(durations) == 0 {
		return out
	}
	sort.Float64s(durations)
	out.min = durations[0]
	out.max = durations[len(durations)-1]
	for _, d := range durations {
		out.sum += d
	}
	out.p50 = percentileFloat(durations, 50)
	out.p75 = percentileFloat(durations, 75)
	out.p90 = percentileFloat(durations, 90)
	out.p95 = percentileFloat(durations, 95)
	out.p99 = percentileFloat(durations, 99)
	return out
}

// percentileFloat returns the nearest-rank percentile of a sorted slice.
func percentileFloat(sorted []float64, percentile int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := (len(sorted)*percentile + 99) / 100
	if index <= 0 {
		index = 1
	}
	if index > len(sorted) {
		index = len(sorted)
	}
	return sorted[index-1]
}

// Prune enforces the configured retention windows.
func (a *Aggregator) Prune(ctx context.Context, rawDays int, hourlyDays int, logDays int, errorDays int) error {
	now := a.now().UTC()
	prune := []struct {
		query string
		arg   any
		on    bool
	}{
		{`DELETE FROM request_events WHERE at < ?`, ms(now.AddDate(0, 0, -rawDays)), rawDays > 0},
		{`DELETE FROM structured_logs WHERE at < ?`, ms(now.AddDate(0, 0, -logDays)), logDays > 0},
		{`DELETE FROM request_errors WHERE at < ?`, ms(now.AddDate(0, 0, -errorDays)), errorDays > 0},
		{`DELETE FROM usage_hourly WHERE bucket < ?`, HourKey(now.AddDate(0, 0, -hourlyDays)), hourlyDays > 0},
		{`DELETE FROM background_jobs WHERE enqueued_at < ?`, ms(now.AddDate(0, 0, -rawDays)), rawDays > 0},
	}
	for _, p := range prune {
		if !p.on {
			continue
		}
		if _, err := a.store.db.ExecContext(ctx, p.query, p.arg); err != nil {
			return err
		}
	}
	a.setState(ctx, stateLastPrune, now.Format(time.RFC3339))
	return nil
}

func (a *Aggregator) getState(ctx context.Context, key string) string {
	var value string
	_ = a.store.db.QueryRowContext(ctx, `SELECT value FROM aggregation_state WHERE key = ?`, key).Scan(&value)
	return strings.TrimSpace(value)
}

func (a *Aggregator) setState(ctx context.Context, key string, value string) {
	_, _ = a.store.db.ExecContext(ctx, `INSERT INTO aggregation_state (key, value, updated_at)
		VALUES (?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value, ms(a.now()))
}

// State returns aggregation health for the admin panel.
func (a *Aggregator) State(ctx context.Context) map[string]string {
	out := map[string]string{}
	rows, err := a.store.db.QueryContext(ctx, `SELECT key, value FROM aggregation_state`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if rows.Scan(&key, &value) == nil {
			out[key] = value
		}
	}
	return out
}

func dayStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// joinKeys renders a small set as a stable comma-separated list.
func joinKeys(set map[string]struct{}) string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}
