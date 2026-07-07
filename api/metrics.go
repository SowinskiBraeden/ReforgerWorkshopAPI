package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

const latestMetricEventsLimit = 25

type Metrics struct {
	mu sync.Mutex

	now        func() time.Time
	startedAt  time.Time
	uniqueSalt []byte

	totalRequests     uint64
	requestDayKey     string
	requestsToday     uint64
	requestWeekKey    string
	requestsThisWeek  uint64
	requestMonthKey   string
	requestsThisMonth uint64
	uniqueToday       map[string]struct{}
	uniqueThisWeek    map[string]struct{}
	uniqueThisMonth   map[string]struct{}

	responseCount uint64
	responseTotal time.Duration
	responseMin   time.Duration
	responseMax   time.Duration

	cacheHits   uint64
	cacheMisses uint64
	cacheStales uint64

	scrapeTotal  uint64
	scrapeErrors uint64

	latestCaches  []MetricEvent
	latestScrapes []MetricEvent
	locations     map[string]*locationMetricState

	hourBuckets  map[string]*metricBucketState
	dayBuckets   map[string]*metricBucketState
	monthBuckets map[string]*metricBucketState
}

type MetricEvent struct {
	At         time.Time `json:"at"`
	Key        string    `json:"key,omitempty"`
	Status     string    `json:"status,omitempty"`
	StatusCode int       `json:"statusCode,omitempty"`
	DurationMs int64     `json:"durationMs,omitempty"`
	Error      string    `json:"error,omitempty"`
}

type MetricsSnapshot struct {
	StartedAt     time.Time             `json:"startedAt"`
	GeneratedAt   time.Time             `json:"generatedAt"`
	UptimeSeconds int64                 `json:"uptimeSeconds"`
	Requests      RequestMetrics        `json:"requests"`
	ResponseTime  ResponseTimeMetrics   `json:"responseTime"`
	Cache         CacheMetricsSnapshot  `json:"cache"`
	Scrapes       ScrapeMetricsSnapshot `json:"scrapes"`
	Retention     RetentionMetrics      `json:"retention"`
	Geography     GeographyMetrics      `json:"geography"`
}

type RequestMetrics struct {
	Total                uint64                     `json:"total"`
	Today                uint64                     `json:"today"`
	ThisWeek             uint64                     `json:"thisWeek"`
	ThisMonth            uint64                     `json:"thisMonth"`
	UniqueClientNetworks UniqueClientNetworkMetrics `json:"uniqueClientNetworks"`
}

type ResponseTimeMetrics struct {
	AverageMs float64 `json:"averageMs"`
	HighMs    int64   `json:"highMs"`
	LowMs     int64   `json:"lowMs"`
}

type CacheMetricsSnapshot struct {
	Total         uint64        `json:"total"`
	Hits          uint64        `json:"hits"`
	Misses        uint64        `json:"misses"`
	Stales        uint64        `json:"stales"`
	Entries       int           `json:"entries"`
	MaxEntries    int           `json:"maxEntries"`
	LatestEvents  []MetricEvent `json:"latestEvents"`
	LatestEntries []CacheInfo   `json:"latestEntries,omitempty"`
}

type ScrapeMetricsSnapshot struct {
	Total        uint64        `json:"total"`
	Errors       uint64        `json:"errors"`
	LatestEvents []MetricEvent `json:"latestEvents"`
}

type RetentionMetrics struct {
	Day   RetentionWindow `json:"day"`
	Week  RetentionWindow `json:"week"`
	Month RetentionWindow `json:"month"`
	Year  RetentionWindow `json:"year"`
}

type UniqueClientNetworkMetrics struct {
	Today     int `json:"today"`
	ThisWeek  int `json:"thisWeek"`
	ThisMonth int `json:"thisMonth"`
}

type GeographyMetrics struct {
	Countries []CountryMetric `json:"countries"`
}

type CountryMetric struct {
	CountryCode          string                     `json:"countryCode"`
	Requests             RequestWindowMetrics       `json:"requests"`
	UniqueClientNetworks UniqueClientNetworkMetrics `json:"uniqueClientNetworks"`
}

type RequestWindowMetrics struct {
	Total     uint64 `json:"total"`
	Today     uint64 `json:"today"`
	ThisWeek  uint64 `json:"thisWeek"`
	ThisMonth uint64 `json:"thisMonth"`
}

type RetentionWindow struct {
	Window     string         `json:"window"`
	BucketSize string         `json:"bucketSize"`
	Buckets    []MetricBucket `json:"buckets"`
}

type MetricBucket struct {
	Start        time.Time           `json:"start"`
	End          time.Time           `json:"end"`
	Requests     uint64              `json:"requests"`
	ResponseTime ResponseTimeMetrics `json:"responseTime"`
	Cache        CacheRollup         `json:"cache"`
	Scrapes      ScrapeRollup        `json:"scrapes"`
}

type CacheRollup struct {
	Hits   uint64 `json:"hits"`
	Misses uint64 `json:"misses"`
	Stales uint64 `json:"stales"`
}

type ScrapeRollup struct {
	Total  uint64 `json:"total"`
	Errors uint64 `json:"errors"`
}

type metricBucketState struct {
	start time.Time
	data  metricRollup
}

type locationMetricState struct {
	countryCode       string
	totalRequests     uint64
	requestsToday     uint64
	requestsThisWeek  uint64
	requestsThisMonth uint64
	uniqueToday       map[string]struct{}
	uniqueThisWeek    map[string]struct{}
	uniqueThisMonth   map[string]struct{}
}

type metricRollup struct {
	requests      uint64
	responseCount uint64
	responseTotal time.Duration
	responseMin   time.Duration
	responseMax   time.Duration
	cacheHits     uint64
	cacheMisses   uint64
	cacheStales   uint64
	scrapes       uint64
	scrapeErrors  uint64
}

func NewMetrics() *Metrics {
	now := time.Now
	startedAt := now().UTC()
	m := &Metrics{
		now:             now,
		startedAt:       startedAt,
		uniqueSalt:      newUniqueSalt(),
		uniqueToday:     make(map[string]struct{}),
		uniqueThisWeek:  make(map[string]struct{}),
		uniqueThisMonth: make(map[string]struct{}),
		locations:       make(map[string]*locationMetricState),
		hourBuckets:     make(map[string]*metricBucketState),
		dayBuckets:      make(map[string]*metricBucketState),
		monthBuckets:    make(map[string]*metricBucketState),
	}
	m.resetRequestWindowsLocked(startedAt)
	return m
}

func (m *Metrics) RecordRequest(duration time.Duration) {
	m.RecordRequestDetails(duration, "", "")
}

func (m *Metrics) RecordRequestDetails(duration time.Duration, clientIP string, countryCode string) {
	if m == nil {
		return
	}
	now := m.now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()

	m.rollRequestWindowsLocked(now)
	m.totalRequests++
	m.requestsToday++
	m.requestsThisWeek++
	m.requestsThisMonth++
	m.recordUniqueClientNetworkLocked(clientIP, countryCode)

	m.responseCount++
	m.responseTotal += duration
	if m.responseMin == 0 || duration < m.responseMin {
		m.responseMin = duration
	}
	if duration > m.responseMax {
		m.responseMax = duration
	}
	m.rollupForLocked(now, hourBucket).recordRequest(duration)
	m.rollupForLocked(now, dayBucket).recordRequest(duration)
	m.rollupForLocked(now, monthBucket).recordRequest(duration)
	m.pruneRetentionLocked(now)
}

func (m *Metrics) RecordCache(key string, status string, statusCode int) {
	if m == nil {
		return
	}
	status = strings.ToUpper(strings.TrimSpace(status))
	now := m.now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()
	switch status {
	case "HIT":
		m.cacheHits++
		m.rollupForLocked(now, hourBucket).cacheHits++
		m.rollupForLocked(now, dayBucket).cacheHits++
		m.rollupForLocked(now, monthBucket).cacheHits++
	case "MISS":
		m.cacheMisses++
		m.rollupForLocked(now, hourBucket).cacheMisses++
		m.rollupForLocked(now, dayBucket).cacheMisses++
		m.rollupForLocked(now, monthBucket).cacheMisses++
	case "STALE":
		m.cacheStales++
		m.rollupForLocked(now, hourBucket).cacheStales++
		m.rollupForLocked(now, dayBucket).cacheStales++
		m.rollupForLocked(now, monthBucket).cacheStales++
	}
	m.latestCaches = appendLatestMetricEvent(m.latestCaches, MetricEvent{
		At:         now,
		Key:        key,
		Status:     status,
		StatusCode: statusCode,
	})
	m.pruneRetentionLocked(now)
}

func (m *Metrics) RecordScrape(key string, statusCode int, duration time.Duration, err error) {
	if m == nil {
		return
	}
	event := MetricEvent{
		At:         m.now().UTC(),
		Key:        key,
		StatusCode: statusCode,
		DurationMs: duration.Milliseconds(),
	}
	if err != nil {
		event.Error = err.Error()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.scrapeTotal++
	if err != nil {
		m.scrapeErrors++
	}
	m.rollupForLocked(event.At, hourBucket).recordScrape(err)
	m.rollupForLocked(event.At, dayBucket).recordScrape(err)
	m.rollupForLocked(event.At, monthBucket).recordScrape(err)
	m.latestScrapes = appendLatestMetricEvent(m.latestScrapes, event)
	m.pruneRetentionLocked(event.At)
}

func (m *Metrics) Snapshot(cache *ResponseCache) MetricsSnapshot {
	if m == nil {
		return MetricsSnapshot{}
	}
	now := m.now().UTC()

	m.mu.Lock()
	m.rollRequestWindowsLocked(now)
	requests := RequestMetrics{
		Total:     m.totalRequests,
		Today:     m.requestsToday,
		ThisWeek:  m.requestsThisWeek,
		ThisMonth: m.requestsThisMonth,
		UniqueClientNetworks: UniqueClientNetworkMetrics{
			Today:     len(m.uniqueToday),
			ThisWeek:  len(m.uniqueThisWeek),
			ThisMonth: len(m.uniqueThisMonth),
		},
	}
	responseTime := ResponseTimeMetrics{
		HighMs: m.responseMax.Milliseconds(),
		LowMs:  m.responseMin.Milliseconds(),
	}
	if m.responseCount > 0 {
		responseTime.AverageMs = float64(m.responseTotal.Microseconds()) / float64(m.responseCount) / 1000
	}
	cacheMetrics := CacheMetricsSnapshot{
		Total:        m.cacheHits + m.cacheMisses + m.cacheStales,
		Hits:         m.cacheHits,
		Misses:       m.cacheMisses,
		Stales:       m.cacheStales,
		LatestEvents: cloneMetricEvents(m.latestCaches),
	}
	scrapeMetrics := ScrapeMetricsSnapshot{
		Total:        m.scrapeTotal,
		Errors:       m.scrapeErrors,
		LatestEvents: cloneMetricEvents(m.latestScrapes),
	}
	retention := m.retentionLocked(now)
	geography := m.geographyLocked()
	startedAt := m.startedAt
	m.mu.Unlock()

	if cache != nil {
		info := cache.Snapshot(25)
		cacheMetrics.Entries = info.Entries
		cacheMetrics.MaxEntries = info.MaxEntries
		cacheMetrics.LatestEntries = info.LatestEntries
	}

	return MetricsSnapshot{
		StartedAt:     startedAt,
		GeneratedAt:   now,
		UptimeSeconds: int64(now.Sub(startedAt).Seconds()),
		Requests:      requests,
		ResponseTime:  responseTime,
		Cache:         cacheMetrics,
		Scrapes:       scrapeMetrics,
		Retention:     retention,
		Geography:     geography,
	}
}

func (m *Metrics) rollRequestWindowsLocked(now time.Time) {
	dayKey, weekKey, monthKey := requestWindowKeys(now)
	if m.requestDayKey != dayKey {
		m.requestDayKey = dayKey
		m.requestsToday = 0
		m.uniqueToday = make(map[string]struct{})
		for _, location := range m.locations {
			location.requestsToday = 0
			location.uniqueToday = make(map[string]struct{})
		}
	}
	if m.requestWeekKey != weekKey {
		m.requestWeekKey = weekKey
		m.requestsThisWeek = 0
		m.uniqueThisWeek = make(map[string]struct{})
		for _, location := range m.locations {
			location.requestsThisWeek = 0
			location.uniqueThisWeek = make(map[string]struct{})
		}
	}
	if m.requestMonthKey != monthKey {
		m.requestMonthKey = monthKey
		m.requestsThisMonth = 0
		m.uniqueThisMonth = make(map[string]struct{})
		for _, location := range m.locations {
			location.requestsThisMonth = 0
			location.uniqueThisMonth = make(map[string]struct{})
		}
	}
}

func (m *Metrics) resetRequestWindowsLocked(now time.Time) {
	m.requestDayKey, m.requestWeekKey, m.requestMonthKey = requestWindowKeys(now)
	m.uniqueToday = make(map[string]struct{})
	m.uniqueThisWeek = make(map[string]struct{})
	m.uniqueThisMonth = make(map[string]struct{})
	for _, location := range m.locations {
		location.requestsToday = 0
		location.requestsThisWeek = 0
		location.requestsThisMonth = 0
		location.uniqueToday = make(map[string]struct{})
		location.uniqueThisWeek = make(map[string]struct{})
		location.uniqueThisMonth = make(map[string]struct{})
	}
}

func requestWindowKeys(now time.Time) (string, string, string) {
	year, week := now.ISOWeek()
	return now.Format("2006-01-02"), fmt.Sprintf("%04d-W%02d", year, week), now.Format("2006-01")
}

func (m *Metrics) recordUniqueClientNetworkLocked(clientIP string, countryCode string) {
	dayKey := m.clientNetworkFingerprint(clientIP, m.requestDayKey)
	weekKey := m.clientNetworkFingerprint(clientIP, m.requestWeekKey)
	monthKey := m.clientNetworkFingerprint(clientIP, m.requestMonthKey)
	if dayKey != "" {
		m.uniqueToday[dayKey] = struct{}{}
	}
	if weekKey != "" {
		m.uniqueThisWeek[weekKey] = struct{}{}
	}
	if monthKey != "" {
		m.uniqueThisMonth[monthKey] = struct{}{}
	}

	countryCode = normalizeCountryCode(countryCode)
	location := m.locations[countryCode]
	if location == nil {
		location = newLocationMetricState(countryCode)
		m.locations[countryCode] = location
	}
	location.totalRequests++
	location.requestsToday++
	location.requestsThisWeek++
	location.requestsThisMonth++
	if dayKey != "" {
		location.uniqueToday[dayKey] = struct{}{}
	}
	if weekKey != "" {
		location.uniqueThisWeek[weekKey] = struct{}{}
	}
	if monthKey != "" {
		location.uniqueThisMonth[monthKey] = struct{}{}
	}
}

func (m *Metrics) clientNetworkFingerprint(clientIP string, windowKey string) string {
	prefix := coarseClientNetwork(clientIP)
	if prefix == "" || windowKey == "" || len(m.uniqueSalt) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, m.uniqueSalt)
	_, _ = mac.Write([]byte(windowKey))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(prefix))
	return hex.EncodeToString(mac.Sum(nil)[:16])
}

func coarseClientNetwork(clientIP string) string {
	ip := net.ParseIP(strings.TrimSpace(clientIP))
	if ip == nil {
		return ""
	}
	if ipv4 := ip.To4(); ipv4 != nil {
		return (&net.IPNet{IP: ipv4.Mask(net.CIDRMask(24, 32)), Mask: net.CIDRMask(24, 32)}).String()
	}
	ipv6 := ip.To16()
	if ipv6 == nil {
		return ""
	}
	return (&net.IPNet{IP: ipv6.Mask(net.CIDRMask(48, 128)), Mask: net.CIDRMask(48, 128)}).String()
}

func normalizeCountryCode(countryCode string) string {
	code := strings.ToUpper(strings.TrimSpace(countryCode))
	if len(code) != 2 || code == "XX" {
		return "ZZ"
	}
	for _, r := range code {
		if r < 'A' || r > 'Z' {
			return "ZZ"
		}
	}
	return code
}

func newLocationMetricState(countryCode string) *locationMetricState {
	return &locationMetricState{
		countryCode:     countryCode,
		uniqueToday:     make(map[string]struct{}),
		uniqueThisWeek:  make(map[string]struct{}),
		uniqueThisMonth: make(map[string]struct{}),
	}
}

func (m *Metrics) geographyLocked() GeographyMetrics {
	countries := make([]CountryMetric, 0, len(m.locations))
	for _, location := range m.locations {
		countries = append(countries, CountryMetric{
			CountryCode: location.countryCode,
			Requests: RequestWindowMetrics{
				Total:     location.totalRequests,
				Today:     location.requestsToday,
				ThisWeek:  location.requestsThisWeek,
				ThisMonth: location.requestsThisMonth,
			},
			UniqueClientNetworks: UniqueClientNetworkMetrics{
				Today:     len(location.uniqueToday),
				ThisWeek:  len(location.uniqueThisWeek),
				ThisMonth: len(location.uniqueThisMonth),
			},
		})
	}
	sort.Slice(countries, func(i int, j int) bool {
		if countries[i].Requests.Today == countries[j].Requests.Today {
			if countries[i].Requests.ThisMonth == countries[j].Requests.ThisMonth {
				return countries[i].CountryCode < countries[j].CountryCode
			}
			return countries[i].Requests.ThisMonth > countries[j].Requests.ThisMonth
		}
		return countries[i].Requests.Today > countries[j].Requests.Today
	})
	return GeographyMetrics{Countries: countries}
}

func newUniqueSalt() []byte {
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err == nil {
		return salt
	}
	return []byte(fmt.Sprintf("%d", time.Now().UnixNano()))
}

type bucketKind int

const (
	hourBucket bucketKind = iota
	dayBucket
	monthBucket
)

func (m *Metrics) rollupForLocked(now time.Time, kind bucketKind) *metricRollup {
	start := bucketStart(now.UTC(), kind)
	key := bucketKey(start, kind)
	buckets := m.hourBuckets
	switch kind {
	case dayBucket:
		buckets = m.dayBuckets
	case monthBucket:
		buckets = m.monthBuckets
	}
	bucket := buckets[key]
	if bucket == nil {
		bucket = &metricBucketState{start: start}
		buckets[key] = bucket
	}
	return &bucket.data
}

func (m *Metrics) retentionLocked(now time.Time) RetentionMetrics {
	now = now.UTC()
	return RetentionMetrics{
		Day: RetentionWindow{
			Window:     "24h",
			BucketSize: "1h",
			Buckets:    m.bucketsLocked(now, hourBucket, 24),
		},
		Week: RetentionWindow{
			Window:     "7d",
			BucketSize: "1d",
			Buckets:    m.bucketsLocked(now, dayBucket, 7),
		},
		Month: RetentionWindow{
			Window:     "31d",
			BucketSize: "1d",
			Buckets:    m.bucketsLocked(now, dayBucket, 31),
		},
		Year: RetentionWindow{
			Window:     "12mo",
			BucketSize: "1mo",
			Buckets:    m.bucketsLocked(now, monthBucket, 12),
		},
	}
}

func (m *Metrics) bucketsLocked(now time.Time, kind bucketKind, count int) []MetricBucket {
	if count <= 0 {
		return nil
	}
	currentStart := bucketStart(now, kind)
	out := make([]MetricBucket, 0, count)
	for i := count - 1; i >= 0; i-- {
		start := addBuckets(currentStart, kind, -i)
		key := bucketKey(start, kind)
		var rollup metricRollup
		if bucket := m.bucketMap(kind)[key]; bucket != nil {
			rollup = bucket.data
		}
		out = append(out, rollup.metricBucket(start, addBuckets(start, kind, 1)))
	}
	return out
}

func (m *Metrics) bucketMap(kind bucketKind) map[string]*metricBucketState {
	switch kind {
	case dayBucket:
		return m.dayBuckets
	case monthBucket:
		return m.monthBuckets
	default:
		return m.hourBuckets
	}
}

func (m *Metrics) pruneRetentionLocked(now time.Time) {
	pruneBuckets(m.hourBuckets, addBuckets(bucketStart(now, hourBucket), hourBucket, -23))
	pruneBuckets(m.dayBuckets, addBuckets(bucketStart(now, dayBucket), dayBucket, -30))
	pruneBuckets(m.monthBuckets, addBuckets(bucketStart(now, monthBucket), monthBucket, -11))
}

func pruneBuckets(buckets map[string]*metricBucketState, oldest time.Time) {
	for key, bucket := range buckets {
		if bucket.start.Before(oldest) {
			delete(buckets, key)
		}
	}
}

func bucketStart(now time.Time, kind bucketKind) time.Time {
	now = now.UTC()
	switch kind {
	case dayBucket:
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	case monthBucket:
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return now.Truncate(time.Hour)
	}
}

func addBuckets(start time.Time, kind bucketKind, delta int) time.Time {
	switch kind {
	case dayBucket:
		return start.AddDate(0, 0, delta)
	case monthBucket:
		return start.AddDate(0, delta, 0)
	default:
		return start.Add(time.Duration(delta) * time.Hour)
	}
}

func bucketKey(start time.Time, kind bucketKind) string {
	switch kind {
	case dayBucket:
		return start.Format("2006-01-02")
	case monthBucket:
		return start.Format("2006-01")
	default:
		return start.Format("2006-01-02T15")
	}
}

func (r *metricRollup) recordRequest(duration time.Duration) {
	r.requests++
	r.responseCount++
	r.responseTotal += duration
	if r.responseMin == 0 || duration < r.responseMin {
		r.responseMin = duration
	}
	if duration > r.responseMax {
		r.responseMax = duration
	}
}

func (r *metricRollup) recordScrape(err error) {
	r.scrapes++
	if err != nil {
		r.scrapeErrors++
	}
}

func (r metricRollup) metricBucket(start time.Time, end time.Time) MetricBucket {
	return MetricBucket{
		Start:        start,
		End:          end,
		Requests:     r.requests,
		ResponseTime: r.responseTime(),
		Cache: CacheRollup{
			Hits:   r.cacheHits,
			Misses: r.cacheMisses,
			Stales: r.cacheStales,
		},
		Scrapes: ScrapeRollup{
			Total:  r.scrapes,
			Errors: r.scrapeErrors,
		},
	}
}

func (r metricRollup) responseTime() ResponseTimeMetrics {
	out := ResponseTimeMetrics{
		HighMs: r.responseMax.Milliseconds(),
		LowMs:  r.responseMin.Milliseconds(),
	}
	if r.responseCount > 0 {
		out.AverageMs = float64(r.responseTotal.Microseconds()) / float64(r.responseCount) / 1000
	}
	return out
}

func appendLatestMetricEvent(events []MetricEvent, event MetricEvent) []MetricEvent {
	events = append([]MetricEvent{event}, events...)
	if len(events) > latestMetricEventsLimit {
		return events[:latestMetricEventsLimit]
	}
	return events
}

func cloneMetricEvents(events []MetricEvent) []MetricEvent {
	out := make([]MetricEvent, len(events))
	copy(out, events)
	return out
}
