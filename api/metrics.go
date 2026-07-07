package api

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const latestMetricEventsLimit = 25

type Metrics struct {
	mu sync.Mutex

	now       func() time.Time
	startedAt time.Time

	totalRequests     uint64
	requestDayKey     string
	requestsToday     uint64
	requestWeekKey    string
	requestsThisWeek  uint64
	requestMonthKey   string
	requestsThisMonth uint64

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
}

type RequestMetrics struct {
	Total     uint64 `json:"total"`
	Today     uint64 `json:"today"`
	ThisWeek  uint64 `json:"thisWeek"`
	ThisMonth uint64 `json:"thisMonth"`
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
		now:          now,
		startedAt:    startedAt,
		hourBuckets:  make(map[string]*metricBucketState),
		dayBuckets:   make(map[string]*metricBucketState),
		monthBuckets: make(map[string]*metricBucketState),
	}
	m.resetRequestWindowsLocked(startedAt)
	return m
}

func (m *Metrics) RecordRequest(duration time.Duration) {
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
	}
}

func (m *Metrics) rollRequestWindowsLocked(now time.Time) {
	dayKey, weekKey, monthKey := requestWindowKeys(now)
	if m.requestDayKey != dayKey {
		m.requestDayKey = dayKey
		m.requestsToday = 0
	}
	if m.requestWeekKey != weekKey {
		m.requestWeekKey = weekKey
		m.requestsThisWeek = 0
	}
	if m.requestMonthKey != monthKey {
		m.requestMonthKey = monthKey
		m.requestsThisMonth = 0
	}
}

func (m *Metrics) resetRequestWindowsLocked(now time.Time) {
	m.requestDayKey, m.requestWeekKey, m.requestMonthKey = requestWindowKeys(now)
}

func requestWindowKeys(now time.Time) (string, string, string) {
	year, week := now.ISOWeek()
	return now.Format("2006-01-02"), fmt.Sprintf("%04d-W%02d", year, week), now.Format("2006-01")
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
