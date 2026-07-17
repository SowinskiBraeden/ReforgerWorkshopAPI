package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

type RefreshJobStatus string
type RefreshPriority string

const (
	RefreshJobQueued    RefreshJobStatus = "queued"
	RefreshJobRunning   RefreshJobStatus = "running"
	RefreshJobSucceeded RefreshJobStatus = "succeeded"
	RefreshJobFailed    RefreshJobStatus = "failed"
	RefreshJobExpired   RefreshJobStatus = "expired"
)

const (
	RefreshPriorityHigh   RefreshPriority = "high"
	RefreshPriorityNormal RefreshPriority = "normal"
	RefreshPriorityLow    RefreshPriority = "low"
)

var (
	ErrRefreshQueueFull = errors.New("refresh queue is full")
	ErrRefreshShutdown  = errors.New("refresh manager is shutting down")
)

type RefreshFetchFunc func(context.Context) CachedResponse

type RefreshRequest struct {
	ResourceKey string
	ResourceURL string
	TTL         time.Duration
	Stale       time.Duration
	Fetch       RefreshFetchFunc
	RequestID   string
	Priority    RefreshPriority
}

type RefreshJobSnapshot struct {
	ID                string           `json:"id"`
	Status            RefreshJobStatus `json:"status"`
	ResourceURL       string           `json:"resource_url"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
	RetryAfterSeconds int              `json:"retry_after_seconds"`
	CompletedAt       *time.Time       `json:"completed_at,omitempty"`
	Message           string           `json:"message,omitempty"`
}

type RefreshManagerSnapshot struct {
	QueueDepth    int `json:"queueDepth"`
	QueueCapacity int `json:"queueCapacity"`
	ActiveWorkers int `json:"activeWorkers"`
	Workers       int `json:"workers"`
}

type refreshManager struct {
	timeout    time.Duration
	retention  time.Duration
	retryAfter time.Duration
	onComplete func(*refreshJob, CachedResponse, time.Duration)
	hooks      *TelemetryHooks
	now        func() time.Time

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	wake   chan struct{}

	mu          sync.Mutex
	jobs        map[string]*refreshJob
	activeByKey map[string]string
	shutting    bool
	workers     int
	active      int
	queueSize   int
	queued      []*refreshJob
}

type refreshJob struct {
	id          string
	resourceKey string
	resourceURL string
	ttl         time.Duration
	stale       time.Duration
	fetch       RefreshFetchFunc
	requestID   string
	priority    RefreshPriority

	status      RefreshJobStatus
	createdAt   time.Time
	startedAt   time.Time
	updatedAt   time.Time
	completedAt *time.Time
	message     string
}

func newRefreshManager(workers int, queueSize int, timeout time.Duration, retention time.Duration, retryAfter time.Duration, hooks *TelemetryHooks, now func() time.Time, onComplete func(*refreshJob, CachedResponse, time.Duration)) *refreshManager {
	if workers <= 0 {
		workers = 1
	}
	if queueSize <= 0 {
		queueSize = 64
	}
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	if retention <= 0 {
		retention = 15 * time.Minute
	}
	if retryAfter <= 0 {
		retryAfter = 2 * time.Second
	}
	if now == nil {
		now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &refreshManager{
		timeout:     timeout,
		retention:   retention,
		retryAfter:  retryAfter,
		onComplete:  onComplete,
		hooks:       hooks,
		now:         now,
		ctx:         ctx,
		cancel:      cancel,
		wake:        make(chan struct{}, 1),
		jobs:        make(map[string]*refreshJob),
		activeByKey: make(map[string]string),
		workers:     workers,
		queueSize:   queueSize,
	}
	for i := 0; i < workers; i++ {
		m.wg.Add(1)
		go m.worker(i + 1)
	}
	m.wg.Add(1)
	go m.cleanupLoop()
	return m
}

func (m *refreshManager) Enqueue(req RefreshRequest) (RefreshJobSnapshot, bool, error) {
	if strings.TrimSpace(req.ResourceKey) == "" || req.Fetch == nil {
		return RefreshJobSnapshot{}, false, ErrRefreshQueueFull
	}
	if req.Priority == "" {
		req.Priority = RefreshPriorityNormal
	}
	now := m.now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.shutting {
		m.hooks.JobRejected()
		m.publishQueueStateLocked()
		return RefreshJobSnapshot{}, false, ErrRefreshShutdown
	}
	if id := m.activeByKey[req.ResourceKey]; id != "" {
		job := m.jobs[id]
		if job != nil {
			m.hooks.JobDeduplicated(job.id)
			return m.snapshotLocked(job), false, nil
		}
		delete(m.activeByKey, req.ResourceKey)
	}

	if len(m.queued) >= m.queueSize {
		m.hooks.JobRejected()
		m.publishQueueStateLocked()
		return RefreshJobSnapshot{}, false, ErrRefreshQueueFull
	}
	job := &refreshJob{
		id:          newRefreshJobID(),
		resourceKey: req.ResourceKey,
		resourceURL: req.ResourceURL,
		ttl:         req.TTL,
		stale:       req.Stale,
		fetch:       req.Fetch,
		requestID:   req.RequestID,
		priority:    req.Priority,
		status:      RefreshJobQueued,
		createdAt:   now,
		updatedAt:   now,
	}
	m.queued = append(m.queued, job)
	m.jobs[job.id] = job
	m.activeByKey[job.resourceKey] = job.id
	m.hooks.JobQueued(job.id, job.requestID, jobKindFor(job.priority, job.requestID), job.resourceKey, job.resourceURL, string(job.priority), now)
	m.publishQueueStateLocked()
	select {
	case m.wake <- struct{}{}:
	default:
	}
	zap.S().Infow("refresh job queued", "requestId", req.RequestID, "jobId", job.id, "resourceKey", job.resourceKey, "resourceURL", job.resourceURL, "priority", job.priority, "queueDepth", len(m.queued))
	return m.snapshotLocked(job), true, nil
}

func (m *refreshManager) Get(id string) (RefreshJobSnapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.jobs[id]
	if job == nil {
		return RefreshJobSnapshot{}, false
	}
	return m.snapshotLocked(job), true
}

func (m *refreshManager) Snapshot() RefreshManagerSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return RefreshManagerSnapshot{
		QueueDepth:    len(m.queued),
		QueueCapacity: m.queueSize,
		ActiveWorkers: m.active,
		Workers:       m.workers,
	}
}

func (m *refreshManager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	if m.shutting {
		m.mu.Unlock()
		return nil
	}
	m.shutting = true
	m.mu.Unlock()
	m.cancel()

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *refreshManager) worker(workerID int) {
	defer m.wg.Done()
	for {
		job := m.nextJob()
		if job == nil {
			return
		}
		m.runJob(workerID, job)
	}
}

func (m *refreshManager) nextJob() *refreshJob {
	for {
		m.mu.Lock()
		if len(m.queued) > 0 {
			best := 0
			for i := 1; i < len(m.queued); i++ {
				if priorityRank(m.queued[i].priority) < priorityRank(m.queued[best].priority) {
					best = i
				}
			}
			job := m.queued[best]
			m.queued = append(m.queued[:best], m.queued[best+1:]...)
			m.mu.Unlock()
			return job
		}
		m.mu.Unlock()
		select {
		case <-m.ctx.Done():
			return nil
		case <-m.wake:
		}
	}
}

func (m *refreshManager) runJob(workerID int, job *refreshJob) {
	m.markRunning(job, workerID)
	start := m.now()

	defer func() {
		if recovered := recover(); recovered != nil {
			duration := m.now().Sub(start)

			resp := CachedResponse{
				Err:            fmt.Errorf("scraper panic while refreshing resource"),
				ErrorCode:      "UPSTREAM_UNAVAILABLE",
				Message:        "Workshop data is temporarily unavailable.",
				PanicRecovered: true,
			}

			status := m.complete(job, resp, duration)

			zap.S().Errorw(
				"refresh job panicked",
				"requestId", job.requestID,
				"jobId", job.id,
				"resourceKey", job.resourceKey,
				"status", status,
				"worker", workerID,
				"panic", recovered,
				"stack", string(debug.Stack()),
			)
		}
	}()

	ctx, cancel := context.WithTimeout(m.ctx, m.timeout)
	resp := job.fetch(ctx)
	cancel()

	duration := m.now().Sub(start)
	status := m.complete(job, resp, duration)

	zap.S().Infow(
		"refresh job finished",
		"requestId", job.requestID,
		"jobId", job.id,
		"resourceKey", job.resourceKey,
		"status", status,
		"statusCode", resp.StatusCode,
		"durationMs", duration.Milliseconds(),
		"worker", workerID,
	)
}

func (m *refreshManager) markRunning(job *refreshJob, workerID int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	job.status = RefreshJobRunning
	job.startedAt = now
	job.updatedAt = now
	m.active++
	m.hooks.JobStarted(job.id, workerID, now)
	m.publishQueueStateLocked()
}

func (m *refreshManager) complete(job *refreshJob, resp CachedResponse, duration time.Duration) RefreshJobStatus {
	if resp.Err == nil && resp.StatusCode == 0 {
		resp.StatusCode = http.StatusOK
	}
	if m.onComplete != nil {
		m.onComplete(job, resp, duration)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	if resp.Err != nil {
		job.status = RefreshJobFailed
		job.message = safeRefreshFailureMessage(resp)
	} else {
		job.status = RefreshJobSucceeded
	}
	job.updatedAt = now
	job.completedAt = &now
	job.fetch = nil
	if m.activeByKey[job.resourceKey] == job.id {
		delete(m.activeByKey, job.resourceKey)
	}
	if m.active > 0 {
		m.active--
	}
	failureReason := ""
	if resp.Err != nil {
		failureReason = ClassifyScrapeError(resp.Err, resp.StatusCode)
		if resp.PanicRecovered {
			failureReason = "panic_recovered"
		}
	}
	m.hooks.JobFinished(job.id, job.createdAt, job.startedAt, now, resp.Err == nil, resp.StatusCode, failureReason, resp.PanicRecovered)
	m.publishQueueStateLocked()
	return job.status
}

func (m *refreshManager) cleanupLoop() {
	defer m.wg.Done()
	interval := m.retention / 2
	if interval < time.Minute {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.cleanup()
		}
	}
}

func (m *refreshManager) cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	for id, job := range m.jobs {
		if job.status == RefreshJobQueued || job.status == RefreshJobRunning {
			continue
		}
		if job.completedAt == nil {
			continue
		}
		age := now.Sub(*job.completedAt)
		if job.status != RefreshJobExpired && age >= m.retention {
			job.status = RefreshJobExpired
			job.updatedAt = now
			job.message = "Refresh job status expired."
			m.hooks.JobExpired(job.id)
			continue
		}
		if job.status == RefreshJobExpired && age >= 2*m.retention {
			delete(m.jobs, id)
		}
	}
}

func (m *refreshManager) snapshotLocked(job *refreshJob) RefreshJobSnapshot {
	snapshot := RefreshJobSnapshot{
		ID:                job.id,
		Status:            job.status,
		ResourceURL:       job.resourceURL,
		CreatedAt:         job.createdAt.UTC(),
		UpdatedAt:         job.updatedAt.UTC(),
		RetryAfterSeconds: retryAfterSeconds(m.retryAfter),
		Message:           job.message,
	}
	if job.completedAt != nil {
		completedAt := job.completedAt.UTC()
		snapshot.CompletedAt = &completedAt
	}
	return snapshot
}

func (m *refreshManager) publishQueueStateLocked() {
	m.hooks.RefreshQueueState(len(m.queued), m.queueSize, m.active, m.workers)
}

// jobKindFor distinguishes request-driven cache refreshes from scheduler
// work: scheduler enqueues carry no request ID.
func jobKindFor(priority RefreshPriority, requestID string) string {
	if requestID == "" {
		return "index_refresh"
	}
	return "cache_refresh"
}

func priorityRank(priority RefreshPriority) int {
	switch priority {
	case RefreshPriorityHigh:
		return 0
	case RefreshPriorityNormal:
		return 1
	case RefreshPriorityLow:
		return 2
	default:
		return 1
	}
}

func safeRefreshFailureMessage(resp CachedResponse) string {
	if resp.Message != "" {
		return resp.Message
	}
	return "Refresh failed. Retry the resource URL after the suggested delay."
}

func newRefreshJobID() string {
	var b [18]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
}

func retryAfterSeconds(d time.Duration) int {
	if d <= 0 {
		return 1
	}
	seconds := int(d.Seconds())
	if seconds < 1 {
		return 1
	}
	return seconds
}
