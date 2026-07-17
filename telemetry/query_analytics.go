package telemetry

import (
	"context"
	"fmt"
	"os"
	"time"
)

// ActivitySeriesPoint is one day of active-entity counts.
type ActivitySeriesPoint struct {
	Day     string `json:"day"`
	Daily   int64  `json:"daily"`
	Weekly  int64  `json:"weekly"`  // distinct actives in the 7 days ending here
	Monthly int64  `json:"monthly"` // distinct actives in the 30 days ending here
	New     int64  `json:"new"`     // first ever seen this day
}

// ActivitySeries computes DAU/WAU/MAU and new counts per day for one entity
// type ("user" | "client" | "key" | "network").
func (s *Store) ActivitySeries(ctx context.Context, entityType string, from time.Time, to time.Time) ([]ActivitySeriesPoint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT day, entity_id FROM entity_activity
		WHERE entity_type = ? AND day >= ? AND day <= ? ORDER BY day`,
		entityType, DayKey(from.AddDate(0, 0, -30)), DayKey(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byDay := map[string][]string{}
	for rows.Next() {
		var day, id string
		if err := rows.Scan(&day, &id); err != nil {
			return nil, err
		}
		byDay[day] = append(byDay[day], id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	firstSeen := map[string]string{} // entity -> first day (within loaded horizon + profiles)
	profileRows, err := s.db.QueryContext(ctx, `SELECT entity_id, first_seen_at FROM entity_profiles WHERE entity_type = ?`, entityType)
	if err != nil {
		return nil, err
	}
	defer profileRows.Close()
	for profileRows.Next() {
		var id string
		var seenAt int64
		if err := profileRows.Scan(&id, &seenAt); err != nil {
			return nil, err
		}
		firstSeen[id] = DayKey(fromMs(seenAt))
	}
	if err := profileRows.Err(); err != nil {
		return nil, err
	}

	var out []ActivitySeriesPoint
	for day := dayStart(from); !day.After(dayStart(to)); day = day.AddDate(0, 0, 1) {
		key := DayKey(day)
		point := ActivitySeriesPoint{Day: key, Daily: int64(len(byDay[key]))}
		weekly := map[string]struct{}{}
		monthly := map[string]struct{}{}
		for back := 0; back < 30; back++ {
			backKey := DayKey(day.AddDate(0, 0, -back))
			for _, id := range byDay[backKey] {
				monthly[id] = struct{}{}
				if back < 7 {
					weekly[id] = struct{}{}
				}
			}
		}
		point.Weekly, point.Monthly = int64(len(weekly)), int64(len(monthly))
		for _, id := range byDay[key] {
			if firstSeen[id] == key {
				point.New++
			}
		}
		out = append(out, point)
	}
	return out, nil
}

// RetentionSummary reports the headline retention rates for one entity type.
type RetentionSummary struct {
	EntityType  string  `json:"entityType"`
	Cohort      int64   `json:"cohortSize"` // entities first seen in the base window
	Day1        float64 `json:"day1"`       // share active on day D+1
	Day7        float64 `json:"day7"`
	Day14       float64 `json:"day14"`
	Day30       float64 `json:"day30"`
	Churned     int64   `json:"churned"`     // active previous period, not current
	Reactivated int64   `json:"reactivated"` // active now after a full inactive period
	Returning   int64   `json:"returning"`   // active now, first seen before the period
	New         int64   `json:"new"`         // first seen inside the period
}

// RetentionSummaryFor computes N-day retention over entities first seen in
// [from, to-30d... window], plus churn/reactivation for the period vs the
// preceding period of equal length.
func (s *Store) RetentionSummaryFor(ctx context.Context, entityType string, from time.Time, to time.Time) (RetentionSummary, error) {
	out := RetentionSummary{EntityType: entityType}
	activeDays := map[string]map[string]struct{}{} // entity -> set of days
	rows, err := s.db.QueryContext(ctx, `SELECT entity_id, day FROM entity_activity
		WHERE entity_type = ? AND day >= ?`, entityType, DayKey(from.AddDate(0, 0, -60)))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, day string
		if err := rows.Scan(&id, &day); err != nil {
			return out, err
		}
		if activeDays[id] == nil {
			activeDays[id] = map[string]struct{}{}
		}
		activeDays[id][day] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}

	profiles := map[string]time.Time{}
	profileRows, err := s.db.QueryContext(ctx, `SELECT entity_id, first_seen_at FROM entity_profiles WHERE entity_type = ?`, entityType)
	if err != nil {
		return out, err
	}
	defer profileRows.Close()
	for profileRows.Next() {
		var id string
		var seenAt int64
		if err := profileRows.Scan(&id, &seenAt); err != nil {
			return out, err
		}
		profiles[id] = fromMs(seenAt)
	}
	if err := profileRows.Err(); err != nil {
		return out, err
	}

	var day1Base, day1Kept, day7Base, day7Kept, day14Base, day14Kept, day30Base, day30Kept int64
	now := dayStart(s.now())
	for id, first := range profiles {
		firstDay := dayStart(first)
		if firstDay.Before(dayStart(from)) || firstDay.After(dayStart(to)) {
			continue
		}
		out.Cohort++
		days := activeDays[id]
		check := func(n int, base *int64, kept *int64) {
			target := firstDay.AddDate(0, 0, n)
			if target.After(now) {
				return // window not elapsed yet; excluded from the base
			}
			*base++
			if _, ok := days[DayKey(target)]; ok {
				*kept++
			}
		}
		check(1, &day1Base, &day1Kept)
		check(7, &day7Base, &day7Kept)
		check(14, &day14Base, &day14Kept)
		check(30, &day30Base, &day30Kept)
	}
	rate := func(kept int64, base int64) float64 {
		if base == 0 {
			return 0
		}
		return float64(kept) / float64(base)
	}
	out.Day1, out.Day7 = rate(day1Kept, day1Base), rate(day7Kept, day7Base)
	out.Day14, out.Day30 = rate(day14Kept, day14Base), rate(day30Kept, day30Base)

	periodLen := int(dayStart(to).Sub(dayStart(from)).Hours()/24) + 1
	prevFrom, prevTo := dayStart(from).AddDate(0, 0, -periodLen), dayStart(from).AddDate(0, 0, -1)
	activeIn := func(id string, a time.Time, b time.Time) bool {
		for day := a; !day.After(b); day = day.AddDate(0, 0, 1) {
			if _, ok := activeDays[id][DayKey(day)]; ok {
				return true
			}
		}
		return false
	}
	for id, first := range profiles {
		current := activeIn(id, dayStart(from), dayStart(to))
		previous := activeIn(id, prevFrom, prevTo)
		switch {
		case previous && !current:
			out.Churned++
		case current && !previous && dayStart(first).Before(prevFrom):
			out.Reactivated++
		}
		if current {
			if dayStart(first).Before(dayStart(from)) {
				out.Returning++
			} else {
				out.New++
			}
		}
	}
	return out, nil
}

// CohortRow is one first-seen cohort with per-period retained shares.
type CohortRow struct {
	Cohort  string    `json:"cohort"` // e.g. 2026-W28 or 2026-07
	Size    int64     `json:"size"`
	Periods []float64 `json:"periods"` // retained share for period 0..N
}

// CohortRetention builds weekly or monthly cohort retention heatmap data.
func (s *Store) CohortRetention(ctx context.Context, entityType string, granularity string, cohorts int, periods int) ([]CohortRow, error) {
	if cohorts <= 0 || cohorts > 26 {
		cohorts = 8
	}
	if periods <= 0 || periods > 12 {
		periods = 8
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.entity_id, a.day, p.first_seen_at
		FROM entity_activity a JOIN entity_profiles p
		ON p.entity_type = a.entity_type AND p.entity_id = a.entity_id
		WHERE a.entity_type = ?`, entityType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	periodKey := func(t time.Time) string {
		if granularity == "monthly" {
			return t.UTC().Format("2006-01")
		}
		year, week := t.UTC().ISOWeek()
		return fmt.Sprintf("%04d-W%02d", year, week)
	}
	periodIndex := func(first time.Time, day time.Time) int {
		if granularity == "monthly" {
			return (day.Year()-first.Year())*12 + int(day.Month()) - int(first.Month())
		}
		firstMonday := mondayOf(first)
		return int(mondayOf(day).Sub(firstMonday).Hours() / 24 / 7)
	}

	type cohortAgg struct {
		start    time.Time
		members  map[string]struct{}
		retained []map[string]struct{}
	}
	byCohort := map[string]*cohortAgg{}
	for rows.Next() {
		var id, dayKey string
		var firstAt int64
		if err := rows.Scan(&id, &dayKey, &firstAt); err != nil {
			return nil, err
		}
		day, err := time.ParseInLocation("2006-01-02", dayKey, time.UTC)
		if err != nil {
			continue
		}
		first := fromMs(firstAt)
		cohortKey := periodKey(first)
		agg := byCohort[cohortKey]
		if agg == nil {
			agg = &cohortAgg{start: first, members: map[string]struct{}{}, retained: make([]map[string]struct{}, periods)}
			for i := range agg.retained {
				agg.retained[i] = map[string]struct{}{}
			}
			byCohort[cohortKey] = agg
		}
		agg.members[id] = struct{}{}
		index := periodIndex(first, day)
		if index >= 0 && index < periods {
			agg.retained[index][id] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(byCohort))
	for key := range byCohort {
		keys = append(keys, key)
	}
	sortStringsDesc(keys)
	if len(keys) > cohorts {
		keys = keys[:cohorts]
	}
	var out []CohortRow
	for _, key := range keys {
		agg := byCohort[key]
		row := CohortRow{Cohort: key, Size: int64(len(agg.members))}
		for i := 0; i < periods; i++ {
			share := 0.0
			if len(agg.members) > 0 {
				share = float64(len(agg.retained[i])) / float64(len(agg.members))
			}
			row.Periods = append(row.Periods, share)
		}
		out = append(out, row)
	}
	return out, nil
}

func mondayOf(t time.Time) time.Time {
	t = dayStart(t)
	weekday := (int(t.Weekday()) + 6) % 7
	return t.AddDate(0, 0, -weekday)
}

func sortStringsDesc(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] > values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// EntityProfile is first/last-seen metadata for one entity.
type EntityProfile struct {
	EntityType     string    `json:"entityType"`
	EntityID       string    `json:"entityId"`
	FirstSeenAt    time.Time `json:"firstSeenAt"`
	LastSeenAt     time.Time `json:"lastSeenAt"`
	DaysActive     int64     `json:"daysActive"`
	LastCountry    string    `json:"lastCountry,omitempty"`
	LastClientName string    `json:"lastClientName,omitempty"`
	LastVersion    string    `json:"lastVersion,omitempty"`
}

func (s *Store) GetEntityProfile(ctx context.Context, entityType string, entityID string) (EntityProfile, bool) {
	row := s.db.QueryRowContext(ctx, `SELECT first_seen_at, last_seen_at, days_active,
		last_country, last_client_name, last_version FROM entity_profiles
		WHERE entity_type = ? AND entity_id = ?`, entityType, entityID)
	out := EntityProfile{EntityType: entityType, EntityID: entityID}
	var first, last int64
	if err := row.Scan(&first, &last, &out.DaysActive, &out.LastCountry, &out.LastClientName, &out.LastVersion); err != nil {
		return out, false
	}
	out.FirstSeenAt, out.LastSeenAt = fromMs(first), fromMs(last)
	return out, true
}

// ClientStat merges client_daily rows over a range for the clients page.
type ClientStat struct {
	ClientKey   string    `json:"clientKey"`
	APIClientID string    `json:"apiClientId,omitempty"`
	ClientName  string    `json:"clientName"`
	Verified    bool      `json:"verified"`
	Requests    int64     `json:"requests"`
	Errors      int64     `json:"errors"`
	RateLimited int64     `json:"rateLimited"`
	Accepted    int64     `json:"accepted"`
	CacheHit    int64     `json:"cacheHit"`
	CacheStale  int64     `json:"cacheStale"`
	CacheMiss   int64     `json:"cacheMiss"`
	Countries   string    `json:"countries,omitempty"`
	LastVersion string    `json:"lastVersion,omitempty"`
	DaysActive  int64     `json:"daysActive"`
	FirstSeenAt time.Time `json:"firstSeenAt,omitempty"`
	LastSeenAt  time.Time `json:"lastSeenAt,omitempty"`
}

func (s *Store) ClientStats(ctx context.Context, from time.Time, to time.Time, limit int) ([]ClientStat, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.client_key,
		MAX(c.api_client_id), MAX(c.client_name), MAX(c.verified),
		SUM(c.requests), SUM(c.errors), SUM(c.rate_limited), SUM(c.accepted),
		SUM(c.cache_hit), SUM(c.cache_stale), SUM(c.cache_miss),
		MAX(c.countries), MAX(c.last_version), COUNT(DISTINCT c.day),
		COALESCE(p.first_seen_at, 0), COALESCE(p.last_seen_at, 0)
		FROM client_daily c
		LEFT JOIN entity_profiles p ON p.entity_type = 'client' AND p.entity_id = c.client_key
		WHERE c.day >= ? AND c.day <= ?
		GROUP BY c.client_key ORDER BY SUM(c.requests) DESC LIMIT ?`,
		DayKey(from), DayKey(to), clampLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClientStat
	for rows.Next() {
		var c ClientStat
		var verified int
		var first, last int64
		if err := rows.Scan(&c.ClientKey, &c.APIClientID, &c.ClientName, &verified,
			&c.Requests, &c.Errors, &c.RateLimited, &c.Accepted,
			&c.CacheHit, &c.CacheStale, &c.CacheMiss,
			&c.Countries, &c.LastVersion, &c.DaysActive, &first, &last); err != nil {
			return nil, err
		}
		c.Verified = verified == 1
		c.FirstSeenAt, c.LastSeenAt = fromMs(first), fromMs(last)
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountryStat merges country_daily rows over a range.
type CountryStat struct {
	CountryCode    string  `json:"countryCode"`
	Requests       int64   `json:"requests"`
	Errors         int64   `json:"errors"`
	RateLimited    int64   `json:"rateLimited"`
	UniqueNetworks int64   `json:"uniqueNetworks"` // max daily uniques (approximate over ranges)
	AvgMs          float64 `json:"avgMs"`
}

func (s *Store) CountryStats(ctx context.Context, from time.Time, to time.Time) ([]CountryStat, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT country_code, SUM(requests), SUM(errors),
		SUM(rate_limited), MAX(unique_networks),
		CASE WHEN SUM(dur_count) > 0 THEN SUM(dur_sum_ms) / SUM(dur_count) ELSE 0 END
		FROM country_daily WHERE day >= ? AND day <= ?
		GROUP BY country_code ORDER BY SUM(requests) DESC`, DayKey(from), DayKey(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CountryStat
	for rows.Next() {
		var c CountryStat
		if err := rows.Scan(&c.CountryCode, &c.Requests, &c.Errors, &c.RateLimited,
			&c.UniqueNetworks, &c.AvgMs); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// NetworkStat merges network_daily rows over a range.
type NetworkStat struct {
	NetworkID   string    `json:"networkId"`
	CountryCode string    `json:"countryCode"`
	ASN         string    `json:"asn,omitempty"`
	NetworkName string    `json:"networkName,omitempty"`
	IsHosting   string    `json:"isHosting"`
	Requests    int64     `json:"requests"`
	Errors      int64     `json:"errors"`
	RateLimited int64     `json:"rateLimited"`
	FirstSeenAt time.Time `json:"firstSeenAt,omitempty"`
	LastSeenAt  time.Time `json:"lastSeenAt,omitempty"`
}

func (s *Store) NetworkStats(ctx context.Context, from time.Time, to time.Time, limit int) ([]NetworkStat, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT n.network_id, MAX(n.country_code),
		MAX(n.asn), MAX(n.network_name), MAX(n.is_hosting),
		SUM(n.requests), SUM(n.errors), SUM(n.rate_limited),
		COALESCE(p.first_seen_at, 0), COALESCE(p.last_seen_at, 0)
		FROM network_daily n
		LEFT JOIN entity_profiles p ON p.entity_type = 'network' AND p.entity_id = n.network_id
		WHERE n.day >= ? AND n.day <= ?
		GROUP BY n.network_id ORDER BY SUM(n.requests) DESC LIMIT ?`,
		DayKey(from), DayKey(to), clampLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NetworkStat
	for rows.Next() {
		var n NetworkStat
		var first, last int64
		if err := rows.Scan(&n.NetworkID, &n.CountryCode, &n.ASN, &n.NetworkName, &n.IsHosting,
			&n.Requests, &n.Errors, &n.RateLimited, &first, &last); err != nil {
			return nil, err
		}
		n.FirstSeenAt, n.LastSeenAt = fromMs(first), fromMs(last)
		out = append(out, n)
	}
	return out, rows.Err()
}

// SearchStat merges search_daily rows over a range.
type SearchStat struct {
	Term         string  `json:"term"`
	Searches     int64   `json:"searches"`
	EmptyResults int64   `json:"emptyResults"`
	Errors       int64   `json:"errors"`
	AvgMs        float64 `json:"avgMs"`
	CacheHitRate float64 `json:"cacheHitRate"`
}

func (s *Store) SearchStats(ctx context.Context, from time.Time, to time.Time, emptyOnly bool, limit int) ([]SearchStat, error) {
	having := ""
	if emptyOnly {
		having = " HAVING SUM(empty_results) > 0"
	}
	rows, err := s.db.QueryContext(ctx, `SELECT term, SUM(searches), SUM(empty_results), SUM(errors),
		CASE WHEN SUM(dur_count) > 0 THEN SUM(dur_sum_ms) / SUM(dur_count) ELSE 0 END,
		CASE WHEN SUM(searches) > 0 THEN CAST(SUM(cache_hit) AS REAL) / SUM(searches) ELSE 0 END
		FROM search_daily WHERE day >= ? AND day <= ?
		GROUP BY term`+having+` ORDER BY SUM(searches) DESC LIMIT ?`,
		DayKey(from), DayKey(to), clampLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchStat
	for rows.Next() {
		var stat SearchStat
		if err := rows.Scan(&stat.Term, &stat.Searches, &stat.EmptyResults, &stat.Errors,
			&stat.AvgMs, &stat.CacheHitRate); err != nil {
			return nil, err
		}
		out = append(out, stat)
	}
	return out, rows.Err()
}

// ModStat merges mod_daily rows over a range for most-requested mods.
type ModStat struct {
	ModID    string `json:"modId"`
	Requests int64  `json:"requests"`
}

func (s *Store) ModStats(ctx context.Context, from time.Time, to time.Time, limit int) ([]ModStat, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT mod_id, SUM(requests) FROM mod_daily
		WHERE day >= ? AND day <= ? GROUP BY mod_id ORDER BY SUM(requests) DESC LIMIT ?`,
		DayKey(from), DayKey(to), clampLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModStat
	for rows.Next() {
		var stat ModStat
		if err := rows.Scan(&stat.ModID, &stat.Requests); err != nil {
			return nil, err
		}
		out = append(out, stat)
	}
	return out, rows.Err()
}

// StorageStats reports database sizes and row counts for the health page.
type StorageStats struct {
	DBPath        string           `json:"dbPath"`
	DBSizeBytes   int64            `json:"dbSizeBytes"`
	RowCounts     map[string]int64 `json:"rowCounts"`
	OldestEventAt time.Time        `json:"oldestEventAt,omitempty"`
	NewestEventAt time.Time        `json:"newestEventAt,omitempty"`
}

func (s *Store) Storage(ctx context.Context) StorageStats {
	out := StorageStats{DBPath: s.dbPath, RowCounts: map[string]int64{}}
	if info, err := os.Stat(s.dbPath); err == nil {
		out.DBSizeBytes = info.Size()
	}
	for _, table := range []string{
		"request_events", "request_errors", "background_jobs", "structured_logs",
		"usage_hourly", "usage_daily", "endpoint_daily", "client_daily",
		"country_daily", "network_daily", "search_daily", "mod_daily",
		"entity_activity", "entity_profiles", "admin_audit_events",
	} {
		var count int64
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM `+table).Scan(&count); err == nil {
			out.RowCounts[table] = count
		}
	}
	var oldest, newest int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MIN(at), 0), COALESCE(MAX(at), 0) FROM request_events`).Scan(&oldest, &newest); err == nil {
		out.OldestEventAt, out.NewestEventAt = fromMs(oldest), fromMs(newest)
	}
	return out
}

// RecentRestarts derives service starts from the stored startup log lines.
func (s *Store) RecentRestarts(ctx context.Context, since time.Time) ([]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT at FROM structured_logs
		WHERE at >= ? AND message LIKE 'ReforgerWorkshopAPI v%is up and running'
		ORDER BY at DESC LIMIT 50`, ms(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var at int64
		if err := rows.Scan(&at); err != nil {
			return nil, err
		}
		out = append(out, fromMs(at))
	}
	return out, rows.Err()
}

// Setting reads a telemetry setting with a fallback default.
func (s *Store) Setting(ctx context.Context, key string, fallback string) string {
	var value string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM telemetry_settings WHERE key = ?`, key).Scan(&value); err != nil {
		return fallback
	}
	if value == "" {
		return fallback
	}
	return value
}

func (s *Store) PutSetting(ctx context.Context, key string, value string, updatedBy string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO telemetry_settings (key, value, updated_at, updated_by)
		VALUES (?,?,?,?) ON CONFLICT(key) DO UPDATE SET
		value=excluded.value, updated_at=excluded.updated_at, updated_by=excluded.updated_by`,
		key, value, ms(s.now()), updatedBy)
	return err
}

func (s *Store) Settings(ctx context.Context) map[string]string {
	out := map[string]string{}
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM telemetry_settings`)
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
