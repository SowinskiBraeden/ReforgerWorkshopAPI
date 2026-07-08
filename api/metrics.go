package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const latestMetricEventsLimit = 25
const (
	metricsTopLimit             = 25
	metricsMaxTrackedClients    = 200
	metricsMaxTrackedEndpoints  = 200
	metricsMaxTrackedCacheKeys  = 200
	metricsSlowRefreshesLimit   = 10
	metricsRecentRefreshesLimit = 256
)

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

	scrapeTotal    uint64
	scrapeErrors   uint64
	scrapeTimeouts uint64

	refreshCreated        uint64
	refreshDeduplicated   uint64
	refreshSucceeded      uint64
	refreshFailed         uint64
	refreshExpired        uint64
	refreshRejected       uint64
	refreshQueueDepth     int
	refreshActiveWorkers  int
	refreshWorkers        int
	refreshQueueCapacity  int
	refreshCompletedToday uint64
	refreshFailedToday    uint64
	refreshPanicToday     uint64
	refreshDurationTotal  time.Duration
	refreshDurationCount  uint64
	refreshDurationsMs    []int64
	slowestRefreshes      []RefreshEventSummary

	acceptedFlow acceptedFlowState

	userAgents           map[string]*windowCounter
	trafficSources       map[string]uint64
	endpoints            map[string]*endpointMetricState
	cacheByEndpoint      map[string]*cacheGroupState
	cacheKeys            map[string]*cacheKeyMetricState
	clientSummaries      map[string]*clientMetricState
	scrapeErrorsByReason map[string]uint64

	latestCaches  []MetricEvent
	latestScrapes []MetricEvent
	locations     map[string]*locationMetricState

	hourBuckets  map[string]*metricBucketState
	dayBuckets   map[string]*metricBucketState
	monthBuckets map[string]*metricBucketState
}

type RequestMetricDetails struct {
	Duration      time.Duration
	ClientIP      string
	CountryCode   string
	UserAgent     string
	Source        string
	Method        string
	Path          string
	RawQuery      string
	StatusCode    int
	CacheStatus   string
	EndpointGroup string
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
	StartedAt            time.Time                     `json:"startedAt"`
	GeneratedAt          time.Time                     `json:"generatedAt"`
	UptimeSeconds        int64                         `json:"uptimeSeconds"`
	Requests             RequestMetrics                `json:"requests"`
	ResponseTime         ResponseTimeMetrics           `json:"responseTime"`
	Cache                CacheMetricsSnapshot          `json:"cache"`
	Scrapes              ScrapeMetricsSnapshot         `json:"scrapes"`
	Refresh              RefreshMetricsSnapshot        `json:"refresh"`
	Clients              ClientMetricsSnapshot         `json:"clients"`
	TrafficSources       TrafficSourceMetrics          `json:"trafficSources"`
	CacheByEndpoint      map[string]CacheGroupSnapshot `json:"cacheByEndpoint"`
	AcceptedFlow         AcceptedFlowSnapshot          `json:"acceptedFlow"`
	RefreshQueue         RefreshQueueSnapshot          `json:"refreshQueue"`
	ScrapeErrorsByReason map[string]uint64             `json:"scrapeErrorsByReason"`
	TopEndpointsToday    []EndpointUsageSnapshot       `json:"topEndpointsToday"`
	TopCacheKeysToday    []CacheKeyUsageSnapshot       `json:"topCacheKeysToday"`
	ClientSummaries      []ClientSummarySnapshot       `json:"clientSummaries"`
	Retention            RetentionMetrics              `json:"retention"`
	Geography            GeographyMetrics              `json:"geography"`
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
	Timeouts     uint64        `json:"timeouts"`
	LatestEvents []MetricEvent `json:"latestEvents"`
}

type RefreshMetricsSnapshot struct {
	Created       uint64 `json:"created"`
	Deduplicated  uint64 `json:"deduplicated"`
	Succeeded     uint64 `json:"succeeded"`
	Failed        uint64 `json:"failed"`
	Expired       uint64 `json:"expired"`
	Rejected      uint64 `json:"rejected"`
	QueueDepth    int    `json:"queueDepth"`
	ActiveWorkers int    `json:"activeWorkers"`
}

type ClientMetricsSnapshot struct {
	TopUserAgentsToday []NamedRequestCount `json:"topUserAgentsToday"`
	TopUserAgentsWeek  []NamedRequestCount `json:"topUserAgentsWeek"`
	TopUserAgentsMonth []NamedRequestCount `json:"topUserAgentsMonth"`
	TopUserAgentsTotal []NamedRequestCount `json:"topUserAgentsTotal"`
}

type NamedRequestCount struct {
	Name     string `json:"name"`
	Requests uint64 `json:"requests"`
}

type TrafficSourceMetrics struct {
	External         uint64 `json:"external"`
	Internal         uint64 `json:"internal"`
	InternalLoopback uint64 `json:"internalLoopback"`
	OwnPanel         uint64 `json:"ownPanel"`
	Unknown          uint64 `json:"unknown"`
}

type CacheGroupSnapshot struct {
	Total         uint64  `json:"total"`
	Hits          uint64  `json:"hits"`
	Stales        uint64  `json:"stales"`
	Misses        uint64  `json:"misses"`
	RefreshQueued uint64  `json:"refreshQueued"`
	RefreshFailed uint64  `json:"refreshFailed"`
	HitPercent    float64 `json:"hitPercent"`
}

type AcceptedFlowSnapshot struct {
	Accepted         uint64  `json:"accepted"`
	Succeeded        uint64  `json:"succeeded"`
	Failed           uint64  `json:"failed"`
	Expired          uint64  `json:"expired"`
	PanicRecovered   uint64  `json:"panicRecovered"`
	LaterHit         uint64  `json:"laterHit"`
	AverageRefreshMs float64 `json:"averageRefreshMs"`
	P95RefreshMs     int64   `json:"p95RefreshMs"`
}

type RefreshQueueSnapshot struct {
	QueuedNow           int                   `json:"queuedNow"`
	RunningNow          int                   `json:"runningNow"`
	Workers             int                   `json:"workers"`
	QueueCapacity       int                   `json:"queueCapacity"`
	CompletedToday      uint64                `json:"completedToday"`
	FailedToday         uint64                `json:"failedToday"`
	PanicRecoveredToday uint64                `json:"panicRecoveredToday"`
	AverageRefreshMs    float64               `json:"averageRefreshMs"`
	P95RefreshMs        int64                 `json:"p95RefreshMs"`
	SlowestRecent       []RefreshEventSummary `json:"slowestRecentRefreshes"`
}

type RefreshEventSummary struct {
	At         time.Time `json:"at"`
	Key        string    `json:"key"`
	Status     string    `json:"status"`
	DurationMs int64     `json:"durationMs"`
	Reason     string    `json:"reason,omitempty"`
}

type EndpointUsageSnapshot struct {
	Method        string `json:"method"`
	Path          string `json:"path"`
	EndpointGroup string `json:"endpointGroup"`
	Requests      uint64 `json:"requests"`
}

type CacheKeyUsageSnapshot struct {
	Key      string `json:"key"`
	Requests uint64 `json:"requests"`
}

type ClientSummarySnapshot struct {
	Name           string            `json:"name"`
	RequestsToday  uint64            `json:"requestsToday"`
	LastSeen       time.Time         `json:"lastSeen"`
	Countries      map[string]uint64 `json:"countries"`
	Sources        map[string]uint64 `json:"sources"`
	EndpointGroups map[string]uint64 `json:"endpointGroups"`
	Cache          CacheRollup       `json:"cache"`
	Accepted202    uint64            `json:"accepted202"`
	Errors         uint64            `json:"errors"`
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

type windowCounter struct {
	total     uint64
	today     uint64
	thisWeek  uint64
	thisMonth uint64
}

type endpointMetricState struct {
	method string
	path   string
	group  string
	counts windowCounter
}

type cacheGroupState struct {
	hits          uint64
	misses        uint64
	stales        uint64
	refreshQueued uint64
	refreshFailed uint64
}

type cacheKeyMetricState struct {
	key    string
	counts windowCounter
}

type clientMetricState struct {
	name           string
	counts         windowCounter
	lastSeen       time.Time
	countries      map[string]uint64
	sources        map[string]uint64
	endpointGroups map[string]uint64
	cache          CacheRollup
	accepted202    uint64
	errors         uint64
}

type acceptedResourceState struct {
	key      string
	created  time.Time
	resolved bool
	laterHit bool
}

type acceptedFlowState struct {
	accepted       uint64
	succeeded      uint64
	failed         uint64
	expired        uint64
	panicRecovered uint64
	laterHit       uint64
	resources      map[string]*acceptedResourceState
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
		now:                  now,
		startedAt:            startedAt,
		uniqueSalt:           newUniqueSalt(),
		uniqueToday:          make(map[string]struct{}),
		uniqueThisWeek:       make(map[string]struct{}),
		uniqueThisMonth:      make(map[string]struct{}),
		locations:            make(map[string]*locationMetricState),
		userAgents:           make(map[string]*windowCounter),
		trafficSources:       make(map[string]uint64),
		endpoints:            make(map[string]*endpointMetricState),
		cacheByEndpoint:      make(map[string]*cacheGroupState),
		cacheKeys:            make(map[string]*cacheKeyMetricState),
		clientSummaries:      make(map[string]*clientMetricState),
		scrapeErrorsByReason: make(map[string]uint64),
		acceptedFlow: acceptedFlowState{
			resources: make(map[string]*acceptedResourceState),
		},
		hourBuckets:  make(map[string]*metricBucketState),
		dayBuckets:   make(map[string]*metricBucketState),
		monthBuckets: make(map[string]*metricBucketState),
	}
	m.resetRequestWindowsLocked(startedAt)
	return m
}

func (m *Metrics) RecordRequest(duration time.Duration) {
	m.RecordRequestDetails(duration, "", "")
}

func (m *Metrics) RecordRequestDetails(duration time.Duration, clientIP string, countryCode string) {
	m.RecordRequestMetric(RequestMetricDetails{
		Duration:    duration,
		ClientIP:    clientIP,
		CountryCode: countryCode,
		Source:      TrafficSourceUnknown,
		Method:      "GET",
		Path:        "unknown",
	})
}

func (m *Metrics) RecordRequestMetric(details RequestMetricDetails) {
	if m == nil {
		return
	}
	now := m.now().UTC()
	client := NormalizeUserAgent(details.UserAgent)
	source := normalizeTrafficSource(details.Source)
	method := strings.ToUpper(strings.TrimSpace(details.Method))
	if method == "" {
		method = "GET"
	}
	path := NormalizeEndpointPath(details.Path)
	group := details.EndpointGroup
	if group == "" {
		group = EndpointGroupForRequest(details.Path, details.RawQuery)
	}
	countryCode := normalizeCountryCode(details.CountryCode)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.rollRequestWindowsLocked(now)
	m.totalRequests++
	m.requestsToday++
	m.requestsThisWeek++
	m.requestsThisMonth++
	m.recordUniqueClientNetworkLocked(details.ClientIP, countryCode)

	m.responseCount++
	m.responseTotal += details.Duration
	if m.responseMin == 0 || details.Duration < m.responseMin {
		m.responseMin = details.Duration
	}
	if details.Duration > m.responseMax {
		m.responseMax = details.Duration
	}
	m.rollupForLocked(now, hourBucket).recordRequest(details.Duration)
	m.rollupForLocked(now, dayBucket).recordRequest(details.Duration)
	m.rollupForLocked(now, monthBucket).recordRequest(details.Duration)
	m.incrementWindowCounterLocked(m.userAgents, client, now, metricsMaxTrackedClients)
	m.trafficSources[source]++
	m.recordEndpointLocked(method, path, group, now)
	m.recordClientRequestLocked(client, now, countryCode, source, group, details.StatusCode)
	if details.CacheStatus != "" {
		m.recordClientCacheLocked(client, details.CacheStatus)
	}
	m.pruneRetentionLocked(now)
}

func (m *Metrics) RecordCache(key string, status string, statusCode int) {
	if m == nil {
		return
	}
	status = strings.ToUpper(strings.TrimSpace(status))
	now := m.now().UTC()
	group := EndpointGroupForCacheKey(key)
	displayKey := NormalizeCacheMetricKey(key)

	m.mu.Lock()
	defer m.mu.Unlock()
	cacheGroup := m.cacheByEndpoint[group]
	if cacheGroup == nil {
		cacheGroup = &cacheGroupState{}
		m.cacheByEndpoint[group] = cacheGroup
	}
	switch status {
	case "HIT":
		m.cacheHits++
		cacheGroup.hits++
		m.markAcceptedLaterHitLocked(key)
		m.rollupForLocked(now, hourBucket).cacheHits++
		m.rollupForLocked(now, dayBucket).cacheHits++
		m.rollupForLocked(now, monthBucket).cacheHits++
	case "MISS":
		m.cacheMisses++
		cacheGroup.misses++
		if statusCode == http.StatusAccepted {
			cacheGroup.refreshQueued++
			m.recordAcceptedLocked(key, now)
		}
		m.rollupForLocked(now, hourBucket).cacheMisses++
		m.rollupForLocked(now, dayBucket).cacheMisses++
		m.rollupForLocked(now, monthBucket).cacheMisses++
	case "STALE":
		m.cacheStales++
		cacheGroup.stales++
		cacheGroup.refreshQueued++
		m.rollupForLocked(now, hourBucket).cacheStales++
		m.rollupForLocked(now, dayBucket).cacheStales++
		m.rollupForLocked(now, monthBucket).cacheStales++
	}
	m.incrementCacheKeyLocked(displayKey, now)
	m.latestCaches = appendLatestMetricEvent(m.latestCaches, MetricEvent{
		At:         now,
		Key:        displayKey,
		Status:     status,
		StatusCode: statusCode,
	})
	m.pruneRetentionLocked(now)
}

func (m *Metrics) RecordScrape(key string, statusCode int, duration time.Duration, err error) {
	m.RecordScrapeResult(key, statusCode, duration, err, "")
}

func (m *Metrics) RecordScrapeResult(key string, statusCode int, duration time.Duration, err error, reason string) {
	if m == nil {
		return
	}
	if reason == "" {
		reason = ClassifyScrapeError(err, statusCode)
	}
	event := MetricEvent{
		At:         m.now().UTC(),
		Key:        NormalizeCacheMetricKey(key),
		StatusCode: statusCode,
		DurationMs: duration.Milliseconds(),
	}
	if err != nil {
		event.Error = reason
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.scrapeTotal++
	if err != nil {
		m.scrapeErrors++
		m.scrapeErrorsByReason[reason]++
		if errors.Is(err, context.DeadlineExceeded) {
			m.scrapeTimeouts++
		}
	}
	m.rollupForLocked(event.At, hourBucket).recordScrape(err)
	m.rollupForLocked(event.At, dayBucket).recordScrape(err)
	m.rollupForLocked(event.At, monthBucket).recordScrape(err)
	m.latestScrapes = appendLatestMetricEvent(m.latestScrapes, event)
	m.pruneRetentionLocked(event.At)
}

func (m *Metrics) RecordRefreshEvent(event string, queueDepth int, activeWorkers int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	switch event {
	case "created":
		m.refreshCreated++
	case "deduplicated":
		m.refreshDeduplicated++
	case "succeeded":
		m.refreshSucceeded++
	case "failed":
		m.refreshFailed++
	case "expired":
		m.refreshExpired++
		m.acceptedFlow.expired++
	case "rejected":
		m.refreshRejected++
	}
	m.refreshQueueDepth = queueDepth
	m.refreshActiveWorkers = activeWorkers
}

func (m *Metrics) RecordRefreshSnapshot(queueDepth int, queueCapacity int, activeWorkers int, workers int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshQueueDepth = queueDepth
	m.refreshQueueCapacity = queueCapacity
	m.refreshActiveWorkers = activeWorkers
	m.refreshWorkers = workers
}

func (m *Metrics) RecordRefreshCompletion(key string, status RefreshJobStatus, duration time.Duration, reason string, panicRecovered bool) {
	if m == nil {
		return
	}
	now := m.now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rollRequestWindowsLocked(now)
	if status == RefreshJobSucceeded {
		m.acceptedFlow.succeeded++
		m.refreshCompletedToday++
	} else if status == RefreshJobFailed {
		m.acceptedFlow.failed++
		m.refreshFailedToday++
		group := EndpointGroupForCacheKey(key)
		cacheGroup := m.cacheByEndpoint[group]
		if cacheGroup == nil {
			cacheGroup = &cacheGroupState{}
			m.cacheByEndpoint[group] = cacheGroup
		}
		cacheGroup.refreshFailed++
	}
	if panicRecovered {
		m.acceptedFlow.panicRecovered++
		m.refreshPanicToday++
	}
	m.markAcceptedResolvedLocked(key)
	if duration > 0 {
		m.refreshDurationTotal += duration
		m.refreshDurationCount++
		m.refreshDurationsMs = appendBoundedInt64(m.refreshDurationsMs, duration.Milliseconds(), metricsRecentRefreshesLimit)
	}
	m.slowestRefreshes = appendSlowRefresh(m.slowestRefreshes, RefreshEventSummary{
		At:         now,
		Key:        NormalizeCacheMetricKey(key),
		Status:     string(status),
		DurationMs: duration.Milliseconds(),
		Reason:     reason,
	}, metricsSlowRefreshesLimit)
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
		Timeouts:     m.scrapeTimeouts,
		LatestEvents: cloneMetricEvents(m.latestScrapes),
	}
	refreshMetrics := RefreshMetricsSnapshot{
		Created:       m.refreshCreated,
		Deduplicated:  m.refreshDeduplicated,
		Succeeded:     m.refreshSucceeded,
		Failed:        m.refreshFailed,
		Expired:       m.refreshExpired,
		Rejected:      m.refreshRejected,
		QueueDepth:    m.refreshQueueDepth,
		ActiveWorkers: m.refreshActiveWorkers,
	}
	clients := ClientMetricsSnapshot{
		TopUserAgentsToday: topWindowCounters(m.userAgents, "today", metricsTopLimit),
		TopUserAgentsWeek:  topWindowCounters(m.userAgents, "week", metricsTopLimit),
		TopUserAgentsMonth: topWindowCounters(m.userAgents, "month", metricsTopLimit),
		TopUserAgentsTotal: topWindowCounters(m.userAgents, "total", metricsTopLimit),
	}
	trafficSources := TrafficSourceMetrics{
		External:         m.trafficSources[TrafficSourceExternal],
		Internal:         m.trafficSources[TrafficSourceInternal],
		InternalLoopback: m.trafficSources[TrafficSourceInternalLoopback],
		OwnPanel:         m.trafficSources[TrafficSourceOwnPanel],
		Unknown:          m.trafficSources[TrafficSourceUnknown],
	}
	cacheByEndpoint := snapshotCacheGroups(m.cacheByEndpoint)
	acceptedFlow := m.acceptedFlow.snapshot(m.refreshDurationTotal, m.refreshDurationCount, m.refreshDurationsMs)
	refreshQueue := RefreshQueueSnapshot{
		QueuedNow:           m.refreshQueueDepth,
		RunningNow:          m.refreshActiveWorkers,
		Workers:             m.refreshWorkers,
		QueueCapacity:       m.refreshQueueCapacity,
		CompletedToday:      m.refreshCompletedToday,
		FailedToday:         m.refreshFailedToday,
		PanicRecoveredToday: m.refreshPanicToday,
		AverageRefreshMs:    averageDurationMs(m.refreshDurationTotal, m.refreshDurationCount),
		P95RefreshMs:        percentileInt64(m.refreshDurationsMs, 95),
		SlowestRecent:       cloneRefreshEvents(m.slowestRefreshes),
	}
	scrapeReasons := cloneStringUint64Map(m.scrapeErrorsByReason)
	topEndpoints := topEndpointSnapshots(m.endpoints, metricsTopLimit)
	topCacheKeys := topCacheKeySnapshots(m.cacheKeys, metricsTopLimit)
	clientSummaries := topClientSummaries(m.clientSummaries, metricsTopLimit)
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
		StartedAt:            startedAt,
		GeneratedAt:          now,
		UptimeSeconds:        int64(now.Sub(startedAt).Seconds()),
		Requests:             requests,
		ResponseTime:         responseTime,
		Cache:                cacheMetrics,
		Scrapes:              scrapeMetrics,
		Refresh:              refreshMetrics,
		Clients:              clients,
		TrafficSources:       trafficSources,
		CacheByEndpoint:      cacheByEndpoint,
		AcceptedFlow:         acceptedFlow,
		RefreshQueue:         refreshQueue,
		ScrapeErrorsByReason: scrapeReasons,
		TopEndpointsToday:    topEndpoints,
		TopCacheKeysToday:    topCacheKeys,
		ClientSummaries:      clientSummaries,
		Retention:            retention,
		Geography:            geography,
	}
}

func (m *Metrics) rollRequestWindowsLocked(now time.Time) {
	dayKey, weekKey, monthKey := requestWindowKeys(now)
	if m.requestDayKey != dayKey {
		m.requestDayKey = dayKey
		m.requestsToday = 0
		m.uniqueToday = make(map[string]struct{})
		m.refreshCompletedToday = 0
		m.refreshFailedToday = 0
		m.refreshPanicToday = 0
		resetWindowCounters(m.userAgents, "today")
		resetEndpointCounters(m.endpoints, "today")
		resetCacheKeyCounters(m.cacheKeys, "today")
		resetClientCounters(m.clientSummaries, "today")
		for _, location := range m.locations {
			location.requestsToday = 0
			location.uniqueToday = make(map[string]struct{})
		}
	}
	if m.requestWeekKey != weekKey {
		m.requestWeekKey = weekKey
		m.requestsThisWeek = 0
		m.uniqueThisWeek = make(map[string]struct{})
		resetWindowCounters(m.userAgents, "week")
		resetEndpointCounters(m.endpoints, "week")
		resetCacheKeyCounters(m.cacheKeys, "week")
		for _, location := range m.locations {
			location.requestsThisWeek = 0
			location.uniqueThisWeek = make(map[string]struct{})
		}
	}
	if m.requestMonthKey != monthKey {
		m.requestMonthKey = monthKey
		m.requestsThisMonth = 0
		m.uniqueThisMonth = make(map[string]struct{})
		resetWindowCounters(m.userAgents, "month")
		resetEndpointCounters(m.endpoints, "month")
		resetCacheKeyCounters(m.cacheKeys, "month")
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
	resetWindowCounters(m.userAgents, "all")
	resetEndpointCounters(m.endpoints, "all")
	resetCacheKeyCounters(m.cacheKeys, "all")
	resetClientCounters(m.clientSummaries, "today")
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

const (
	TrafficSourceExternal         = "external"
	TrafficSourceInternal         = "internal"
	TrafficSourceInternalLoopback = "internal-loopback"
	TrafficSourceOwnPanel         = "own-panel"
	TrafficSourceUnknown          = "unknown"
)

func NormalizeUserAgent(raw string) string {
	ua := strings.TrimSpace(raw)
	if ua == "" {
		return "unknown"
	}
	ua = strings.Join(strings.Fields(ua), " ")
	if len(ua) > 160 {
		ua = ua[:160]
	}
	lower := strings.ToLower(ua)
	switch {
	case strings.Contains(lower, "mozilla/") || strings.Contains(lower, "chrome/") || strings.Contains(lower, "safari/") || strings.Contains(lower, "firefox/") || strings.Contains(lower, "edg/"):
		return "browser"
	case strings.HasPrefix(lower, "node"):
		return "node"
	case strings.HasPrefix(lower, "curl/"):
		return firstUserAgentProduct(ua)
	case strings.Contains(ua, " "):
		return firstUserAgentProduct(ua)
	default:
		return sanitizeMetricKey(ua, "unknown", 80)
	}
}

func firstUserAgentProduct(ua string) string {
	product := strings.TrimSpace(strings.Split(ua, " ")[0])
	return sanitizeMetricKey(product, "unknown", 80)
}

func sanitizeMetricKey(value string, fallback string, maxLen int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	var b strings.Builder
	for _, r := range value {
		if r < 32 || r == 127 {
			continue
		}
		b.WriteRune(r)
		if maxLen > 0 && b.Len() >= maxLen {
			break
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return fallback
	}
	return out
}

func normalizeTrafficSource(source string) string {
	switch strings.TrimSpace(strings.ToLower(source)) {
	case TrafficSourceExternal:
		return TrafficSourceExternal
	case TrafficSourceInternal:
		return TrafficSourceInternal
	case TrafficSourceInternalLoopback:
		return TrafficSourceInternalLoopback
	case TrafficSourceOwnPanel:
		return TrafficSourceOwnPanel
	default:
		return TrafficSourceUnknown
	}
}

func NormalizeEndpointPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "unknown"
	}
	path = strings.TrimRight(path, "/")
	if path == "" {
		return "/"
	}
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "" {
			continue
		}
		if looksDynamicID(part) {
			parts[i] = "{id}"
		}
	}
	normalized := strings.Join(parts, "/")
	normalized = strings.ReplaceAll(normalized, "/mods/{id}", "/mods/{page}")
	normalized = strings.ReplaceAll(normalized, "/mod/{id}", "/mod/{id}")
	normalized = strings.ReplaceAll(normalized, "/refresh/jobs/{id}", "/refresh/jobs/{id}")
	return normalized
}

func looksDynamicID(part string) bool {
	if part == "" {
		return false
	}
	if _, err := strconv.Atoi(part); err == nil {
		return true
	}
	if len(part) >= 10 {
		for _, r := range part {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
				continue
			}
			return false
		}
		return true
	}
	return false
}

func EndpointGroupForRequest(path string, rawQuery string) string {
	normalized := NormalizeEndpointPath(path)
	query, _ := url.ParseQuery(rawQuery)
	if query.Get("search") != "" {
		return "search"
	}
	switch normalized {
	case "/v1/mod/{id}", "/mod/{id}":
		return "mod_detail"
	case "/v1/mods", "/mods", "/v1/mods/{page}", "/mods/{page}":
		return "mod_list"
	case "/v1/search", "/search":
		return "search"
	case "/v1/refresh/jobs/{id}", "/refresh/jobs/{id}":
		return "refresh_job"
	case "/v1/health", "/health":
		return "health"
	}
	if strings.HasPrefix(normalized, "/static/") || strings.HasPrefix(normalized, "/docs/") {
		return "docs_static"
	}
	return "other"
}

func EndpointGroupForCacheKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	switch {
	case strings.Contains(key, ":mod:"):
		return "mod_detail"
	case strings.Contains(key, ":mods:") && !strings.Contains(key, "::") && strings.Count(key, ":") >= 4:
		return "search"
	case strings.Contains(key, ":mods:") || strings.Contains(key, "/mods"):
		if strings.Contains(key, "search=") || strings.Contains(key, "?search=") {
			return "search"
		}
		return "mod_list"
	case strings.Contains(key, "refresh/jobs"):
		return "refresh_job"
	case strings.Contains(key, "health"):
		return "health"
	default:
		return "other"
	}
}

func NormalizeCacheMetricKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return "unknown"
	}
	if len(key) > 160 {
		key = key[:160]
	}
	return key
}

func ClassifyScrapeError(err error, statusCode int) string {
	if err == nil {
		if statusCode == http.StatusNotFound {
			return "not_found"
		}
		if statusCode >= 500 {
			return "upstream_status"
		}
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "upstream_timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "context_cancelled"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "panic"):
		return "panic_recovered"
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline"):
		return "upstream_timeout"
	case strings.Contains(msg, "status"):
		return "upstream_status"
	case strings.Contains(msg, "no mod") || strings.Contains(msg, "not found"):
		return "not_found"
	case strings.Contains(msg, "index out of range") || strings.Contains(msg, "slice bounds") || strings.Contains(msg, "parse") || strings.Contains(msg, "strconv"):
		return "parser_error"
	case strings.Contains(msg, "connection") || strings.Contains(msg, "unavailable") || strings.Contains(msg, "no such host"):
		return "upstream_unavailable"
	default:
		return "unknown"
	}
}

func (m *Metrics) incrementWindowCounterLocked(counters map[string]*windowCounter, key string, now time.Time, max int) {
	key = sanitizeMetricKey(key, "unknown", 100)
	counter := counters[key]
	if counter == nil {
		if len(counters) >= max {
			pruneWindowCounters(counters, max-1)
		}
		counter = &windowCounter{}
		counters[key] = counter
	}
	counter.total++
	counter.today++
	counter.thisWeek++
	counter.thisMonth++
}

func (m *Metrics) recordEndpointLocked(method string, path string, group string, now time.Time) {
	key := method + " " + path
	endpoint := m.endpoints[key]
	if endpoint == nil {
		if len(m.endpoints) >= metricsMaxTrackedEndpoints {
			pruneEndpoints(m.endpoints, metricsMaxTrackedEndpoints-1)
		}
		endpoint = &endpointMetricState{method: method, path: path, group: group}
		m.endpoints[key] = endpoint
	}
	endpoint.group = group
	endpoint.counts.total++
	endpoint.counts.today++
	endpoint.counts.thisWeek++
	endpoint.counts.thisMonth++
}

func (m *Metrics) incrementCacheKeyLocked(key string, now time.Time) {
	state := m.cacheKeys[key]
	if state == nil {
		if len(m.cacheKeys) >= metricsMaxTrackedCacheKeys {
			pruneCacheKeys(m.cacheKeys, metricsMaxTrackedCacheKeys-1)
		}
		state = &cacheKeyMetricState{key: key}
		m.cacheKeys[key] = state
	}
	state.counts.total++
	state.counts.today++
	state.counts.thisWeek++
	state.counts.thisMonth++
}

func (m *Metrics) recordClientRequestLocked(client string, now time.Time, countryCode string, source string, group string, statusCode int) {
	state := m.clientSummaries[client]
	if state == nil {
		if len(m.clientSummaries) >= metricsMaxTrackedClients {
			pruneClients(m.clientSummaries, metricsMaxTrackedClients-1)
		}
		state = &clientMetricState{
			name:           client,
			countries:      make(map[string]uint64),
			sources:        make(map[string]uint64),
			endpointGroups: make(map[string]uint64),
		}
		m.clientSummaries[client] = state
	}
	state.counts.total++
	state.counts.today++
	state.counts.thisWeek++
	state.counts.thisMonth++
	state.lastSeen = now
	state.countries[countryCode]++
	state.sources[source]++
	state.endpointGroups[group]++
	if statusCode == http.StatusAccepted {
		state.accepted202++
	}
	if statusCode >= 400 {
		state.errors++
	}
}

func (m *Metrics) RecordClientCache(client string, cacheStatus string) {
	if m == nil {
		return
	}
	client = NormalizeUserAgent(client)
	cacheStatus = strings.ToUpper(strings.TrimSpace(cacheStatus))
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.clientSummaries[client]
	if state == nil {
		return
	}
	m.recordClientCacheLocked(client, cacheStatus)
}

func (m *Metrics) recordClientCacheLocked(client string, cacheStatus string) {
	state := m.clientSummaries[client]
	if state == nil {
		return
	}
	switch cacheStatus {
	case "HIT":
		state.cache.Hits++
	case "MISS":
		state.cache.Misses++
	case "STALE":
		state.cache.Stales++
	}
}

func (m *Metrics) recordAcceptedLocked(key string, now time.Time) {
	key = NormalizeCacheMetricKey(key)
	m.acceptedFlow.accepted++
	if m.acceptedFlow.resources == nil {
		m.acceptedFlow.resources = make(map[string]*acceptedResourceState)
	}
	if len(m.acceptedFlow.resources) >= metricsMaxTrackedCacheKeys {
		pruneAcceptedResources(m.acceptedFlow.resources, metricsMaxTrackedCacheKeys-1)
	}
	m.acceptedFlow.resources[key] = &acceptedResourceState{key: key, created: now}
}

func (m *Metrics) markAcceptedResolvedLocked(key string) {
	key = NormalizeCacheMetricKey(key)
	if state := m.acceptedFlow.resources[key]; state != nil {
		state.resolved = true
	}
}

func (m *Metrics) markAcceptedLaterHitLocked(key string) {
	key = NormalizeCacheMetricKey(key)
	if state := m.acceptedFlow.resources[key]; state != nil && state.resolved && !state.laterHit {
		state.laterHit = true
		m.acceptedFlow.laterHit++
	}
}

func (s acceptedFlowState) snapshot(total time.Duration, count uint64, durations []int64) AcceptedFlowSnapshot {
	return AcceptedFlowSnapshot{
		Accepted:         s.accepted,
		Succeeded:        s.succeeded,
		Failed:           s.failed,
		Expired:          s.expired,
		PanicRecovered:   s.panicRecovered,
		LaterHit:         s.laterHit,
		AverageRefreshMs: averageDurationMs(total, count),
		P95RefreshMs:     percentileInt64(durations, 95),
	}
}

func topWindowCounters(counters map[string]*windowCounter, window string, limit int) []NamedRequestCount {
	out := make([]NamedRequestCount, 0, len(counters))
	for name, counter := range counters {
		if counter == nil {
			continue
		}
		value := windowCounterValue(*counter, window)
		if value > 0 {
			out = append(out, NamedRequestCount{Name: name, Requests: value})
		}
	}
	sort.Slice(out, func(i int, j int) bool {
		if out[i].Requests == out[j].Requests {
			return out[i].Name < out[j].Name
		}
		return out[i].Requests > out[j].Requests
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func windowCounterValue(counter windowCounter, window string) uint64 {
	switch window {
	case "today":
		return counter.today
	case "week":
		return counter.thisWeek
	case "month":
		return counter.thisMonth
	default:
		return counter.total
	}
}

func snapshotCacheGroups(groups map[string]*cacheGroupState) map[string]CacheGroupSnapshot {
	out := make(map[string]CacheGroupSnapshot, len(groups))
	for group, state := range groups {
		if state == nil {
			continue
		}
		total := state.hits + state.misses + state.stales
		hitPercent := 0.0
		if total > 0 {
			hitPercent = float64(state.hits+state.stales) / float64(total) * 100
		}
		out[group] = CacheGroupSnapshot{
			Total:         total,
			Hits:          state.hits,
			Stales:        state.stales,
			Misses:        state.misses,
			RefreshQueued: state.refreshQueued,
			RefreshFailed: state.refreshFailed,
			HitPercent:    hitPercent,
		}
	}
	return out
}

func topEndpointSnapshots(endpoints map[string]*endpointMetricState, limit int) []EndpointUsageSnapshot {
	out := make([]EndpointUsageSnapshot, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if endpoint == nil || endpoint.counts.today == 0 {
			continue
		}
		out = append(out, EndpointUsageSnapshot{
			Method:        endpoint.method,
			Path:          endpoint.path,
			EndpointGroup: endpoint.group,
			Requests:      endpoint.counts.today,
		})
	}
	sort.Slice(out, func(i int, j int) bool {
		if out[i].Requests == out[j].Requests {
			return out[i].Path < out[j].Path
		}
		return out[i].Requests > out[j].Requests
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func topCacheKeySnapshots(keys map[string]*cacheKeyMetricState, limit int) []CacheKeyUsageSnapshot {
	out := make([]CacheKeyUsageSnapshot, 0, len(keys))
	for _, key := range keys {
		if key == nil || key.counts.today == 0 {
			continue
		}
		out = append(out, CacheKeyUsageSnapshot{Key: key.key, Requests: key.counts.today})
	}
	sort.Slice(out, func(i int, j int) bool {
		if out[i].Requests == out[j].Requests {
			return out[i].Key < out[j].Key
		}
		return out[i].Requests > out[j].Requests
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func topClientSummaries(clients map[string]*clientMetricState, limit int) []ClientSummarySnapshot {
	out := make([]ClientSummarySnapshot, 0, len(clients))
	for _, client := range clients {
		if client == nil || client.counts.today == 0 {
			continue
		}
		out = append(out, ClientSummarySnapshot{
			Name:           client.name,
			RequestsToday:  client.counts.today,
			LastSeen:       client.lastSeen,
			Countries:      cloneStringUint64Map(client.countries),
			Sources:        cloneStringUint64Map(client.sources),
			EndpointGroups: cloneStringUint64Map(client.endpointGroups),
			Cache:          client.cache,
			Accepted202:    client.accepted202,
			Errors:         client.errors,
		})
	}
	sort.Slice(out, func(i int, j int) bool {
		if out[i].RequestsToday == out[j].RequestsToday {
			return out[i].Name < out[j].Name
		}
		return out[i].RequestsToday > out[j].RequestsToday
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func resetWindowCounters(counters map[string]*windowCounter, window string) {
	for _, counter := range counters {
		if counter == nil {
			continue
		}
		switch window {
		case "today":
			counter.today = 0
		case "week":
			counter.thisWeek = 0
		case "month":
			counter.thisMonth = 0
		case "all":
			counter.today = 0
			counter.thisWeek = 0
			counter.thisMonth = 0
		}
	}
}

func resetEndpointCounters(endpoints map[string]*endpointMetricState, window string) {
	for _, endpoint := range endpoints {
		if endpoint != nil {
			resetCounterWindow(&endpoint.counts, window)
		}
	}
}

func resetCacheKeyCounters(keys map[string]*cacheKeyMetricState, window string) {
	for _, key := range keys {
		if key != nil {
			resetCounterWindow(&key.counts, window)
		}
	}
}

func resetClientCounters(clients map[string]*clientMetricState, window string) {
	for _, client := range clients {
		if client == nil {
			continue
		}
		resetCounterWindow(&client.counts, window)
		if window == "today" || window == "all" {
			client.cache = CacheRollup{}
			client.accepted202 = 0
			client.errors = 0
			client.countries = make(map[string]uint64)
			client.sources = make(map[string]uint64)
			client.endpointGroups = make(map[string]uint64)
		}
	}
}

func resetCounterWindow(counter *windowCounter, window string) {
	switch window {
	case "today":
		counter.today = 0
	case "week":
		counter.thisWeek = 0
	case "month":
		counter.thisMonth = 0
	case "all":
		counter.today = 0
		counter.thisWeek = 0
		counter.thisMonth = 0
	}
}

func pruneWindowCounters(counters map[string]*windowCounter, keep int) {
	type item struct {
		key   string
		value uint64
	}
	items := make([]item, 0, len(counters))
	for key, counter := range counters {
		if counter != nil {
			items = append(items, item{key: key, value: counter.total})
		}
	}
	sort.Slice(items, func(i int, j int) bool { return items[i].value > items[j].value })
	allowed := make(map[string]struct{}, keep)
	for i := 0; i < keep && i < len(items); i++ {
		allowed[items[i].key] = struct{}{}
	}
	for key := range counters {
		if _, ok := allowed[key]; !ok {
			delete(counters, key)
		}
	}
}

func pruneEndpoints(endpoints map[string]*endpointMetricState, keep int) {
	pruneByKey(endpoints, keep, func(v *endpointMetricState) uint64 {
		if v == nil {
			return 0
		}
		return v.counts.total
	})
}

func pruneCacheKeys(keys map[string]*cacheKeyMetricState, keep int) {
	pruneByKey(keys, keep, func(v *cacheKeyMetricState) uint64 {
		if v == nil {
			return 0
		}
		return v.counts.total
	})
}

func pruneClients(clients map[string]*clientMetricState, keep int) {
	pruneByKey(clients, keep, func(v *clientMetricState) uint64 {
		if v == nil {
			return 0
		}
		return v.counts.total
	})
}

func pruneAcceptedResources(resources map[string]*acceptedResourceState, keep int) {
	type item struct {
		key string
		at  time.Time
	}
	items := make([]item, 0, len(resources))
	for key, resource := range resources {
		if resource != nil {
			items = append(items, item{key: key, at: resource.created})
		}
	}
	sort.Slice(items, func(i int, j int) bool { return items[i].at.After(items[j].at) })
	allowed := make(map[string]struct{}, keep)
	for i := 0; i < keep && i < len(items); i++ {
		allowed[items[i].key] = struct{}{}
	}
	for key := range resources {
		if _, ok := allowed[key]; !ok {
			delete(resources, key)
		}
	}
}

func pruneByKey[T any](values map[string]T, keep int, score func(T) uint64) {
	type item struct {
		key   string
		value uint64
	}
	items := make([]item, 0, len(values))
	for key, value := range values {
		items = append(items, item{key: key, value: score(value)})
	}
	sort.Slice(items, func(i int, j int) bool { return items[i].value > items[j].value })
	allowed := make(map[string]struct{}, keep)
	for i := 0; i < keep && i < len(items); i++ {
		allowed[items[i].key] = struct{}{}
	}
	for key := range values {
		if _, ok := allowed[key]; !ok {
			delete(values, key)
		}
	}
}

func cloneStringUint64Map(in map[string]uint64) map[string]uint64 {
	out := make(map[string]uint64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func averageDurationMs(total time.Duration, count uint64) float64 {
	if count == 0 {
		return 0
	}
	return float64(total.Microseconds()) / float64(count) / 1000
}

func percentileInt64(values []int64, percentile int) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i int, j int) bool { return sorted[i] < sorted[j] })
	index := (len(sorted)*percentile + 99) / 100
	if index <= 0 {
		index = 1
	}
	if index > len(sorted) {
		index = len(sorted)
	}
	return sorted[index-1]
}

func appendBoundedInt64(values []int64, value int64, limit int) []int64 {
	values = append(values, value)
	if len(values) > limit {
		values = values[len(values)-limit:]
	}
	return values
}

func appendSlowRefresh(events []RefreshEventSummary, event RefreshEventSummary, limit int) []RefreshEventSummary {
	events = append(events, event)
	sort.Slice(events, func(i int, j int) bool { return events[i].DurationMs > events[j].DurationMs })
	if len(events) > limit {
		events = events[:limit]
	}
	return events
}

func cloneRefreshEvents(events []RefreshEventSummary) []RefreshEventSummary {
	return append([]RefreshEventSummary(nil), events...)
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
