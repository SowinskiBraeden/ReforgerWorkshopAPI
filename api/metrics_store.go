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

	RefreshCreated      uint64 `json:"refreshCreated"`
	RefreshDeduplicated uint64 `json:"refreshDeduplicated"`
	RefreshSucceeded    uint64 `json:"refreshSucceeded"`
	RefreshFailed       uint64 `json:"refreshFailed"`
	RefreshExpired      uint64 `json:"refreshExpired"`
	RefreshRejected     uint64 `json:"refreshRejected"`

	LatestCaches  []MetricEvent `json:"latestCaches"`
	LatestScrapes []MetricEvent `json:"latestScrapes"`

	Locations    map[string]persistentLocationMetricState `json:"locations"`
	HourBuckets  map[string]persistentMetricBucketState   `json:"hourBuckets"`
	DayBuckets   map[string]persistentMetricBucketState   `json:"dayBuckets"`
	MonthBuckets map[string]persistentMetricBucketState   `json:"monthBuckets"`
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

		RefreshCreated:      m.refreshCreated,
		RefreshDeduplicated: m.refreshDeduplicated,
		RefreshSucceeded:    m.refreshSucceeded,
		RefreshFailed:       m.refreshFailed,
		RefreshExpired:      m.refreshExpired,
		RefreshRejected:     m.refreshRejected,

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
