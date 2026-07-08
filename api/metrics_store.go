package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

const metricsStateSchemaVersion = 1

// MetricsStore persists aggregate internal metrics to one local JSON file.
// It is intended for a single API instance.
type MetricsStore struct {
	path          string
	flushInterval time.Duration

	lifecycleMu sync.Mutex
	writeMu     sync.Mutex
	cancel      context.CancelFunc
	done        chan struct{}
}

func NewMetricsStore(path string, flushInterval time.Duration) (*MetricsStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("metrics state path is required")
	}

	if flushInterval <= 0 {
		flushInterval = 15 * time.Second
	}

	return &MetricsStore{
		path:          filepath.Clean(path),
		flushInterval: flushInterval,
	}, nil
}

// Load restores persisted metrics. A missing file is normal on first startup.
// A corrupt or incompatible file is renamed and the API continues with fresh metrics.
func (s *MetricsStore) Load(metrics *Metrics) error {
	if s == nil || metrics == nil {
		return nil
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read metrics state: %w", err)
	}

	var state metricsPersistentState
	if err := json.Unmarshal(data, &state); err != nil {
		return s.archiveInvalidState(fmt.Errorf("decode metrics state: %w", err))
	}

	if state.Version != metricsStateSchemaVersion {
		return s.archiveInvalidState(fmt.Errorf(
			"unsupported metrics state version %d",
			state.Version,
		))
	}

	metrics.restorePersistentState(state)
	return nil
}

// Start begins periodic flushing. Calling Start more than once is safe.
func (s *MetricsStore) Start(metrics *Metrics) {
	if s == nil || metrics == nil {
		return
	}

	s.lifecycleMu.Lock()
	if s.cancel != nil {
		s.lifecycleMu.Unlock()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	s.cancel = cancel
	s.done = done
	s.lifecycleMu.Unlock()

	go func() {
		defer close(done)

		ticker := time.NewTicker(s.flushInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.Save(metrics); err != nil {
					zap.S().Warnw("metrics state flush failed", "error", err)
				}
			}
		}
	}()
}

// Save writes a complete atomic snapshot.
func (s *MetricsStore) Save(metrics *Metrics) error {
	if s == nil || metrics == nil {
		return nil
	}

	state := metrics.persistentState()

	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode metrics state: %w", err)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	return writeFileAtomically(s.path, data, 0600)
}

// Close stops the periodic writer and performs one final synchronous flush.
func (s *MetricsStore) Close(metrics *Metrics) error {
	if s == nil || metrics == nil {
		return nil
	}

	s.lifecycleMu.Lock()
	cancel := s.cancel
	done := s.done
	s.cancel = nil
	s.done = nil
	s.lifecycleMu.Unlock()

	if cancel != nil {
		cancel()
		<-done
	}

	return s.Save(metrics)
}

func (s *MetricsStore) archiveInvalidState(reason error) error {
	archivePath := fmt.Sprintf(
		"%s.invalid-%d",
		s.path,
		time.Now().UTC().UnixNano(),
	)

	if err := os.Rename(s.path, archivePath); err != nil {
		return fmt.Errorf("%w; could not archive invalid state: %v", reason, err)
	}

	return reason
}

func writeFileAtomically(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)

	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create metrics state directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary metrics state: %w", err)
	}

	tmpPath := tmp.Name()
	closed := false

	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmpPath)
	}()

	if err := tmp.Chmod(mode); err != nil {
		return fmt.Errorf("set metrics state permissions: %w", err)
	}

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write metrics state: %w", err)
	}

	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync metrics state: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close metrics state: %w", err)
	}
	closed = true

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace metrics state: %w", err)
	}

	return nil
}

type metricsPersistentState struct {
	Version int       `json:"version"`
	SavedAt time.Time `json:"savedAt"`

	UniqueSalt []byte `json:"uniqueSalt"`

	TotalRequests     uint64 `json:"totalRequests"`
	RequestDayKey     string `json:"requestDayKey"`
	RequestsToday     uint64 `json:"requestsToday"`
	RequestWeekKey    string `json:"requestWeekKey"`
	RequestsThisWeek  uint64 `json:"requestsThisWeek"`
	RequestMonthKey   string `json:"requestMonthKey"`
	RequestsThisMonth uint64 `json:"requestsThisMonth"`

	UniqueToday     []string `json:"uniqueToday"`
	UniqueThisWeek  []string `json:"uniqueThisWeek"`
	UniqueThisMonth []string `json:"uniqueThisMonth"`

	ResponseCount      uint64 `json:"responseCount"`
	ResponseTotalNanos int64  `json:"responseTotalNanos"`
	ResponseMinNanos   int64  `json:"responseMinNanos"`
	ResponseMaxNanos   int64  `json:"responseMaxNanos"`

	CacheHits   uint64 `json:"cacheHits"`
	CacheMisses uint64 `json:"cacheMisses"`
	CacheStales uint64 `json:"cacheStales"`

	ScrapeTotal    uint64 `json:"scrapeTotal"`
	ScrapeErrors   uint64 `json:"scrapeErrors"`
	ScrapeTimeouts uint64 `json:"scrapeTimeouts"`

	RefreshCreated            uint64                `json:"refreshCreated"`
	RefreshDeduplicated       uint64                `json:"refreshDeduplicated"`
	RefreshSucceeded          uint64                `json:"refreshSucceeded"`
	RefreshFailed             uint64                `json:"refreshFailed"`
	RefreshExpired            uint64                `json:"refreshExpired"`
	RefreshRejected           uint64                `json:"refreshRejected"`
	RefreshCompletedToday     uint64                `json:"refreshCompletedToday"`
	RefreshFailedToday        uint64                `json:"refreshFailedToday"`
	RefreshPanicToday         uint64                `json:"refreshPanicToday"`
	RefreshDurationTotalNanos int64                 `json:"refreshDurationTotalNanos"`
	RefreshDurationCount      uint64                `json:"refreshDurationCount"`
	RefreshDurationsMs        []int64               `json:"refreshDurationsMs"`
	SlowestRefreshes          []RefreshEventSummary `json:"slowestRefreshes"`

	AcceptedFlow         persistentAcceptedFlowState              `json:"acceptedFlow"`
	UserAgents           map[string]persistentWindowCounter       `json:"userAgents"`
	TrafficSources       map[string]uint64                        `json:"trafficSources"`
	Endpoints            map[string]persistentEndpointMetricState `json:"endpoints"`
	CacheByEndpoint      map[string]persistentCacheGroupState     `json:"cacheByEndpoint"`
	CacheKeys            map[string]persistentCacheKeyMetricState `json:"cacheKeys"`
	ClientSummaries      map[string]persistentClientMetricState   `json:"clientSummaries"`
	ScrapeErrorsByReason map[string]uint64                        `json:"scrapeErrorsByReason"`

	LatestCaches  []MetricEvent `json:"latestCaches"`
	LatestScrapes []MetricEvent `json:"latestScrapes"`

	Locations    map[string]persistentLocationMetricState `json:"locations"`
	HourBuckets  map[string]persistentMetricBucketState   `json:"hourBuckets"`
	DayBuckets   map[string]persistentMetricBucketState   `json:"dayBuckets"`
	MonthBuckets map[string]persistentMetricBucketState   `json:"monthBuckets"`
}

type persistentWindowCounter struct {
	Total     uint64 `json:"total"`
	Today     uint64 `json:"today"`
	ThisWeek  uint64 `json:"thisWeek"`
	ThisMonth uint64 `json:"thisMonth"`
}

type persistentEndpointMetricState struct {
	Method string                  `json:"method"`
	Path   string                  `json:"path"`
	Group  string                  `json:"group"`
	Counts persistentWindowCounter `json:"counts"`
}

type persistentCacheGroupState struct {
	Hits          uint64 `json:"hits"`
	Misses        uint64 `json:"misses"`
	Stales        uint64 `json:"stales"`
	RefreshQueued uint64 `json:"refreshQueued"`
	RefreshFailed uint64 `json:"refreshFailed"`
}

type persistentCacheKeyMetricState struct {
	Key    string                  `json:"key"`
	Counts persistentWindowCounter `json:"counts"`
}

type persistentClientMetricState struct {
	Name           string                  `json:"name"`
	Counts         persistentWindowCounter `json:"counts"`
	LastSeen       time.Time               `json:"lastSeen"`
	Countries      map[string]uint64       `json:"countries"`
	Sources        map[string]uint64       `json:"sources"`
	EndpointGroups map[string]uint64       `json:"endpointGroups"`
	Cache          CacheRollup             `json:"cache"`
	Accepted202    uint64                  `json:"accepted202"`
	Errors         uint64                  `json:"errors"`
}

type persistentAcceptedFlowState struct {
	Accepted       uint64                                     `json:"accepted"`
	Succeeded      uint64                                     `json:"succeeded"`
	Failed         uint64                                     `json:"failed"`
	Expired        uint64                                     `json:"expired"`
	PanicRecovered uint64                                     `json:"panicRecovered"`
	LaterHit       uint64                                     `json:"laterHit"`
	Resources      map[string]persistentAcceptedResourceState `json:"resources"`
}

type persistentAcceptedResourceState struct {
	Key      string    `json:"key"`
	Created  time.Time `json:"created"`
	Resolved bool      `json:"resolved"`
	LaterHit bool      `json:"laterHit"`
}

type persistentLocationMetricState struct {
	CountryCode string `json:"countryCode"`

	TotalRequests     uint64 `json:"totalRequests"`
	RequestsToday     uint64 `json:"requestsToday"`
	RequestsThisWeek  uint64 `json:"requestsThisWeek"`
	RequestsThisMonth uint64 `json:"requestsThisMonth"`

	UniqueToday     []string `json:"uniqueToday"`
	UniqueThisWeek  []string `json:"uniqueThisWeek"`
	UniqueThisMonth []string `json:"uniqueThisMonth"`
}

type persistentMetricBucketState struct {
	Start time.Time              `json:"start"`
	Data  persistentMetricRollup `json:"data"`
}

type persistentMetricRollup struct {
	Requests uint64 `json:"requests"`

	ResponseCount      uint64 `json:"responseCount"`
	ResponseTotalNanos int64  `json:"responseTotalNanos"`
	ResponseMinNanos   int64  `json:"responseMinNanos"`
	ResponseMaxNanos   int64  `json:"responseMaxNanos"`

	CacheHits   uint64 `json:"cacheHits"`
	CacheMisses uint64 `json:"cacheMisses"`
	CacheStales uint64 `json:"cacheStales"`

	Scrapes      uint64 `json:"scrapes"`
	ScrapeErrors uint64 `json:"scrapeErrors"`
}

func (m *Metrics) persistentState() metricsPersistentState {
	if m == nil {
		return metricsPersistentState{
			Version: metricsStateSchemaVersion,
			SavedAt: time.Now().UTC(),
		}
	}

	now := m.now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.rollRequestWindowsLocked(now)
	m.pruneRetentionLocked(now)

	return metricsPersistentState{
		Version: metricsStateSchemaVersion,
		SavedAt: now,

		UniqueSalt: append([]byte(nil), m.uniqueSalt...),

		TotalRequests:     m.totalRequests,
		RequestDayKey:     m.requestDayKey,
		RequestsToday:     m.requestsToday,
		RequestWeekKey:    m.requestWeekKey,
		RequestsThisWeek:  m.requestsThisWeek,
		RequestMonthKey:   m.requestMonthKey,
		RequestsThisMonth: m.requestsThisMonth,

		UniqueToday:     fingerprintSetToSlice(m.uniqueToday),
		UniqueThisWeek:  fingerprintSetToSlice(m.uniqueThisWeek),
		UniqueThisMonth: fingerprintSetToSlice(m.uniqueThisMonth),

		ResponseCount:      m.responseCount,
		ResponseTotalNanos: int64(m.responseTotal),
		ResponseMinNanos:   int64(m.responseMin),
		ResponseMaxNanos:   int64(m.responseMax),

		CacheHits:   m.cacheHits,
		CacheMisses: m.cacheMisses,
		CacheStales: m.cacheStales,

		ScrapeTotal:    m.scrapeTotal,
		ScrapeErrors:   m.scrapeErrors,
		ScrapeTimeouts: m.scrapeTimeouts,

		RefreshCreated:            m.refreshCreated,
		RefreshDeduplicated:       m.refreshDeduplicated,
		RefreshSucceeded:          m.refreshSucceeded,
		RefreshFailed:             m.refreshFailed,
		RefreshExpired:            m.refreshExpired,
		RefreshRejected:           m.refreshRejected,
		RefreshCompletedToday:     m.refreshCompletedToday,
		RefreshFailedToday:        m.refreshFailedToday,
		RefreshPanicToday:         m.refreshPanicToday,
		RefreshDurationTotalNanos: int64(m.refreshDurationTotal),
		RefreshDurationCount:      m.refreshDurationCount,
		RefreshDurationsMs:        append([]int64(nil), m.refreshDurationsMs...),
		SlowestRefreshes:          cloneRefreshEvents(m.slowestRefreshes),
		AcceptedFlow:              persistentAcceptedFlow(m.acceptedFlow),
		UserAgents:                persistentWindowCounters(m.userAgents),
		TrafficSources:            cloneStringUint64Map(m.trafficSources),
		Endpoints:                 persistentEndpoints(m.endpoints),
		CacheByEndpoint:           persistentCacheGroups(m.cacheByEndpoint),
		CacheKeys:                 persistentCacheKeys(m.cacheKeys),
		ClientSummaries:           persistentClientSummaries(m.clientSummaries),
		ScrapeErrorsByReason:      cloneStringUint64Map(m.scrapeErrorsByReason),

		LatestCaches:  cloneMetricEvents(m.latestCaches),
		LatestScrapes: cloneMetricEvents(m.latestScrapes),

		Locations:    persistentLocations(m.locations),
		HourBuckets:  persistentBuckets(m.hourBuckets),
		DayBuckets:   persistentBuckets(m.dayBuckets),
		MonthBuckets: persistentBuckets(m.monthBuckets),
	}
}

func (m *Metrics) restorePersistentState(state metricsPersistentState) {
	if m == nil {
		return
	}

	now := m.now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.uniqueSalt = append([]byte(nil), state.UniqueSalt...)
	if len(m.uniqueSalt) == 0 {
		m.uniqueSalt = newUniqueSalt()
	}

	m.totalRequests = state.TotalRequests
	m.requestDayKey = state.RequestDayKey
	m.requestsToday = state.RequestsToday
	m.requestWeekKey = state.RequestWeekKey
	m.requestsThisWeek = state.RequestsThisWeek
	m.requestMonthKey = state.RequestMonthKey
	m.requestsThisMonth = state.RequestsThisMonth

	m.uniqueToday = fingerprintSliceToSet(state.UniqueToday)
	m.uniqueThisWeek = fingerprintSliceToSet(state.UniqueThisWeek)
	m.uniqueThisMonth = fingerprintSliceToSet(state.UniqueThisMonth)

	m.responseCount = state.ResponseCount
	m.responseTotal = time.Duration(state.ResponseTotalNanos)
	m.responseMin = time.Duration(state.ResponseMinNanos)
	m.responseMax = time.Duration(state.ResponseMaxNanos)

	m.cacheHits = state.CacheHits
	m.cacheMisses = state.CacheMisses
	m.cacheStales = state.CacheStales

	m.scrapeTotal = state.ScrapeTotal
	m.scrapeErrors = state.ScrapeErrors
	m.scrapeTimeouts = state.ScrapeTimeouts

	m.refreshCreated = state.RefreshCreated
	m.refreshDeduplicated = state.RefreshDeduplicated
	m.refreshSucceeded = state.RefreshSucceeded
	m.refreshFailed = state.RefreshFailed
	m.refreshExpired = state.RefreshExpired
	m.refreshRejected = state.RefreshRejected
	m.refreshCompletedToday = state.RefreshCompletedToday
	m.refreshFailedToday = state.RefreshFailedToday
	m.refreshPanicToday = state.RefreshPanicToday
	m.refreshDurationTotal = time.Duration(state.RefreshDurationTotalNanos)
	m.refreshDurationCount = state.RefreshDurationCount
	m.refreshDurationsMs = append([]int64(nil), state.RefreshDurationsMs...)
	if len(m.refreshDurationsMs) > metricsRecentRefreshesLimit {
		m.refreshDurationsMs = m.refreshDurationsMs[len(m.refreshDurationsMs)-metricsRecentRefreshesLimit:]
	}
	m.slowestRefreshes = cloneRefreshEvents(state.SlowestRefreshes)
	if len(m.slowestRefreshes) > metricsSlowRefreshesLimit {
		m.slowestRefreshes = m.slowestRefreshes[:metricsSlowRefreshesLimit]
	}
	m.acceptedFlow = restoreAcceptedFlow(state.AcceptedFlow)
	m.userAgents = restoreWindowCounters(state.UserAgents)
	m.trafficSources = cloneStringUint64Map(state.TrafficSources)
	m.endpoints = restoreEndpoints(state.Endpoints)
	m.cacheByEndpoint = restoreCacheGroups(state.CacheByEndpoint)
	m.cacheKeys = restoreCacheKeys(state.CacheKeys)
	m.clientSummaries = restoreClientSummaries(state.ClientSummaries)
	m.scrapeErrorsByReason = cloneStringUint64Map(state.ScrapeErrorsByReason)

	// Live values belong to the new process and must not survive restart.
	m.refreshQueueDepth = 0
	m.refreshActiveWorkers = 0

	m.latestCaches = limitMetricEvents(state.LatestCaches)
	m.latestScrapes = limitMetricEvents(state.LatestScrapes)

	m.locations = restoreLocations(state.Locations)
	m.hourBuckets = restoreBuckets(state.HourBuckets)
	m.dayBuckets = restoreBuckets(state.DayBuckets)
	m.monthBuckets = restoreBuckets(state.MonthBuckets)

	// Clear expired day/week/month unique counts and old chart buckets.
	m.rollRequestWindowsLocked(now)
	m.pruneRetentionLocked(now)
}

func persistentLocations(
	locations map[string]*locationMetricState,
) map[string]persistentLocationMetricState {
	out := make(map[string]persistentLocationMetricState, len(locations))

	for key, location := range locations {
		if location == nil {
			continue
		}

		out[key] = persistentLocationMetricState{
			CountryCode:       location.countryCode,
			TotalRequests:     location.totalRequests,
			RequestsToday:     location.requestsToday,
			RequestsThisWeek:  location.requestsThisWeek,
			RequestsThisMonth: location.requestsThisMonth,
			UniqueToday:       fingerprintSetToSlice(location.uniqueToday),
			UniqueThisWeek:    fingerprintSetToSlice(location.uniqueThisWeek),
			UniqueThisMonth:   fingerprintSetToSlice(location.uniqueThisMonth),
		}
	}

	return out
}

func restoreLocations(
	stored map[string]persistentLocationMetricState,
) map[string]*locationMetricState {
	out := make(map[string]*locationMetricState, len(stored))

	for key, location := range stored {
		countryCode := normalizeCountryCode(location.CountryCode)
		if countryCode == "ZZ" {
			countryCode = normalizeCountryCode(key)
		}

		out[countryCode] = &locationMetricState{
			countryCode:       countryCode,
			totalRequests:     location.TotalRequests,
			requestsToday:     location.RequestsToday,
			requestsThisWeek:  location.RequestsThisWeek,
			requestsThisMonth: location.RequestsThisMonth,
			uniqueToday:       fingerprintSliceToSet(location.UniqueToday),
			uniqueThisWeek:    fingerprintSliceToSet(location.UniqueThisWeek),
			uniqueThisMonth:   fingerprintSliceToSet(location.UniqueThisMonth),
		}
	}

	return out
}

func persistentBuckets(
	buckets map[string]*metricBucketState,
) map[string]persistentMetricBucketState {
	out := make(map[string]persistentMetricBucketState, len(buckets))

	for key, bucket := range buckets {
		if bucket == nil {
			continue
		}

		out[key] = persistentMetricBucketState{
			Start: bucket.start.UTC(),
			Data:  persistentRollup(bucket.data),
		}
	}

	return out
}

func restoreBuckets(
	stored map[string]persistentMetricBucketState,
) map[string]*metricBucketState {
	out := make(map[string]*metricBucketState, len(stored))

	for key, bucket := range stored {
		out[key] = &metricBucketState{
			start: bucket.Start.UTC(),
			data:  restoreRollup(bucket.Data),
		}
	}

	return out
}

func persistentRollup(rollup metricRollup) persistentMetricRollup {
	return persistentMetricRollup{
		Requests: rollup.requests,

		ResponseCount:      rollup.responseCount,
		ResponseTotalNanos: int64(rollup.responseTotal),
		ResponseMinNanos:   int64(rollup.responseMin),
		ResponseMaxNanos:   int64(rollup.responseMax),

		CacheHits:   rollup.cacheHits,
		CacheMisses: rollup.cacheMisses,
		CacheStales: rollup.cacheStales,

		Scrapes:      rollup.scrapes,
		ScrapeErrors: rollup.scrapeErrors,
	}
}

func restoreRollup(stored persistentMetricRollup) metricRollup {
	return metricRollup{
		requests: stored.Requests,

		responseCount: stored.ResponseCount,
		responseTotal: time.Duration(stored.ResponseTotalNanos),
		responseMin:   time.Duration(stored.ResponseMinNanos),
		responseMax:   time.Duration(stored.ResponseMaxNanos),

		cacheHits:   stored.CacheHits,
		cacheMisses: stored.CacheMisses,
		cacheStales: stored.CacheStales,

		scrapes:      stored.Scrapes,
		scrapeErrors: stored.ScrapeErrors,
	}
}

func persistentWindowCounters(in map[string]*windowCounter) map[string]persistentWindowCounter {
	out := make(map[string]persistentWindowCounter, len(in))
	for key, counter := range in {
		if counter != nil {
			out[key] = persistentWindowCounter{
				Total: counter.total, Today: counter.today, ThisWeek: counter.thisWeek, ThisMonth: counter.thisMonth,
			}
		}
	}
	return out
}

func restoreWindowCounters(in map[string]persistentWindowCounter) map[string]*windowCounter {
	out := make(map[string]*windowCounter, len(in))
	for key, counter := range in {
		key = sanitizeMetricKey(key, "unknown", 100)
		out[key] = &windowCounter{total: counter.Total, today: counter.Today, thisWeek: counter.ThisWeek, thisMonth: counter.ThisMonth}
	}
	pruneWindowCounters(out, metricsMaxTrackedClients)
	return out
}

func persistentEndpoints(in map[string]*endpointMetricState) map[string]persistentEndpointMetricState {
	out := make(map[string]persistentEndpointMetricState, len(in))
	for key, endpoint := range in {
		if endpoint != nil {
			out[key] = persistentEndpointMetricState{
				Method: endpoint.method, Path: endpoint.path, Group: endpoint.group, Counts: persistentCounter(endpoint.counts),
			}
		}
	}
	return out
}

func restoreEndpoints(in map[string]persistentEndpointMetricState) map[string]*endpointMetricState {
	out := make(map[string]*endpointMetricState, len(in))
	for key, endpoint := range in {
		out[key] = &endpointMetricState{
			method: endpoint.Method, path: endpoint.Path, group: endpoint.Group, counts: restoreCounter(endpoint.Counts),
		}
	}
	pruneEndpoints(out, metricsMaxTrackedEndpoints)
	return out
}

func persistentCacheGroups(in map[string]*cacheGroupState) map[string]persistentCacheGroupState {
	out := make(map[string]persistentCacheGroupState, len(in))
	for key, group := range in {
		if group != nil {
			out[key] = persistentCacheGroupState{Hits: group.hits, Misses: group.misses, Stales: group.stales, RefreshQueued: group.refreshQueued, RefreshFailed: group.refreshFailed}
		}
	}
	return out
}

func restoreCacheGroups(in map[string]persistentCacheGroupState) map[string]*cacheGroupState {
	out := make(map[string]*cacheGroupState, len(in))
	for key, group := range in {
		out[key] = &cacheGroupState{hits: group.Hits, misses: group.Misses, stales: group.Stales, refreshQueued: group.RefreshQueued, refreshFailed: group.RefreshFailed}
	}
	return out
}

func persistentCacheKeys(in map[string]*cacheKeyMetricState) map[string]persistentCacheKeyMetricState {
	out := make(map[string]persistentCacheKeyMetricState, len(in))
	for key, value := range in {
		if value != nil {
			out[key] = persistentCacheKeyMetricState{Key: value.key, Counts: persistentCounter(value.counts)}
		}
	}
	return out
}

func restoreCacheKeys(in map[string]persistentCacheKeyMetricState) map[string]*cacheKeyMetricState {
	out := make(map[string]*cacheKeyMetricState, len(in))
	for key, value := range in {
		displayKey := NormalizeCacheMetricKey(value.Key)
		if displayKey == "" || displayKey == "unknown" {
			displayKey = NormalizeCacheMetricKey(key)
		}
		out[key] = &cacheKeyMetricState{key: displayKey, counts: restoreCounter(value.Counts)}
	}
	pruneCacheKeys(out, metricsMaxTrackedCacheKeys)
	return out
}

func persistentClientSummaries(in map[string]*clientMetricState) map[string]persistentClientMetricState {
	out := make(map[string]persistentClientMetricState, len(in))
	for key, client := range in {
		if client != nil {
			out[key] = persistentClientMetricState{
				Name: client.name, Counts: persistentCounter(client.counts), LastSeen: client.lastSeen.UTC(),
				Countries: cloneStringUint64Map(client.countries), Sources: cloneStringUint64Map(client.sources), EndpointGroups: cloneStringUint64Map(client.endpointGroups),
				Cache: client.cache, Accepted202: client.accepted202, Errors: client.errors,
			}
		}
	}
	return out
}

func restoreClientSummaries(in map[string]persistentClientMetricState) map[string]*clientMetricState {
	out := make(map[string]*clientMetricState, len(in))
	for key, client := range in {
		name := NormalizeUserAgent(client.Name)
		if name == "unknown" {
			name = NormalizeUserAgent(key)
		}
		out[name] = &clientMetricState{
			name: name, counts: restoreCounter(client.Counts), lastSeen: client.LastSeen.UTC(),
			countries: cloneStringUint64Map(client.Countries), sources: cloneStringUint64Map(client.Sources), endpointGroups: cloneStringUint64Map(client.EndpointGroups),
			cache: client.Cache, accepted202: client.Accepted202, errors: client.Errors,
		}
	}
	pruneClients(out, metricsMaxTrackedClients)
	return out
}

func persistentAcceptedFlow(in acceptedFlowState) persistentAcceptedFlowState {
	out := persistentAcceptedFlowState{
		Accepted: in.accepted, Succeeded: in.succeeded, Failed: in.failed, Expired: in.expired, PanicRecovered: in.panicRecovered, LaterHit: in.laterHit,
		Resources: make(map[string]persistentAcceptedResourceState, len(in.resources)),
	}
	for key, resource := range in.resources {
		if resource != nil {
			out.Resources[key] = persistentAcceptedResourceState{Key: resource.key, Created: resource.created.UTC(), Resolved: resource.resolved, LaterHit: resource.laterHit}
		}
	}
	return out
}

func restoreAcceptedFlow(in persistentAcceptedFlowState) acceptedFlowState {
	out := acceptedFlowState{
		accepted: in.Accepted, succeeded: in.Succeeded, failed: in.Failed, expired: in.Expired, panicRecovered: in.PanicRecovered, laterHit: in.LaterHit,
		resources: make(map[string]*acceptedResourceState, len(in.Resources)),
	}
	for key, resource := range in.Resources {
		out.resources[key] = &acceptedResourceState{key: resource.Key, created: resource.Created.UTC(), resolved: resource.Resolved, laterHit: resource.LaterHit}
	}
	pruneAcceptedResources(out.resources, metricsMaxTrackedCacheKeys)
	return out
}

func persistentCounter(counter windowCounter) persistentWindowCounter {
	return persistentWindowCounter{Total: counter.total, Today: counter.today, ThisWeek: counter.thisWeek, ThisMonth: counter.thisMonth}
}

func restoreCounter(counter persistentWindowCounter) windowCounter {
	return windowCounter{total: counter.Total, today: counter.Today, thisWeek: counter.ThisWeek, thisMonth: counter.ThisMonth}
}

func fingerprintSetToSlice(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))

	for value := range values {
		if value != "" {
			out = append(out, value)
		}
	}

	sort.Strings(out)
	return out
}

func fingerprintSliceToSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = struct{}{}
		}
	}

	return out
}

func limitMetricEvents(events []MetricEvent) []MetricEvent {
	if len(events) > latestMetricEventsLimit {
		events = events[:latestMetricEventsLimit]
	}

	return cloneMetricEvents(events)
}
