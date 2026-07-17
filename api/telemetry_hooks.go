package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/telemetry"
)

// TelemetryHooks is the instrumentation surface for the cache, refresh and
// index subsystems. It records background-job lifecycles and upstream errors
// into the telemetry store and keeps in-process counters/gauges for the
// health page. It deliberately has no request-recording methods: request
// events are emitted only by the telemetry middleware, so cache and job
// activity can never inflate request counts.
type TelemetryHooks struct {
	recorder *telemetry.Recorder

	mu       sync.Mutex
	counters map[string]int64
}

func NewTelemetryHooks(recorder *telemetry.Recorder) *TelemetryHooks {
	return &TelemetryHooks{recorder: recorder, counters: map[string]int64{}}
}

func (h *TelemetryHooks) inc(name string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.counters[name]++
	h.mu.Unlock()
}

// Counters returns a copy of the in-process counters (reset on restart; the
// durable numbers live in the telemetry database).
func (h *TelemetryHooks) Counters() map[string]int64 {
	out := map[string]int64{}
	if h == nil {
		return out
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for name, value := range h.counters {
		out[name] = value
	}
	return out
}

func (h *TelemetryHooks) SetGauge(name string, value int64) {
	if h == nil || h.recorder == nil {
		return
	}
	h.recorder.SetGauge(name, value)
}

// JobQueued records a refresh job entering the queue.
func (h *TelemetryHooks) JobQueued(jobID string, requestID string, kind string, resourceKey string, resourceURL string, priority string, at time.Time) {
	if h == nil || h.recorder == nil {
		return
	}
	h.inc("jobs_queued")
	h.recorder.RecordJob(telemetry.JobEvent{
		JobID:       jobID,
		RequestID:   requestID,
		Kind:        kind,
		ResourceKey: telemetry.SanitizeText(resourceKey, 200),
		ResourceURL: telemetry.SanitizePath(resourceURL),
		Priority:    priority,
		EnqueuedAt:  at,
		Status:      telemetry.JobQueued,
	})
}

// JobDeduplicated marks an enqueue that coalesced into an existing job.
func (h *TelemetryHooks) JobDeduplicated(jobID string) {
	if h == nil || h.recorder == nil {
		return
	}
	h.inc("jobs_deduplicated")
	h.recorder.RecordJob(telemetry.JobEvent{JobID: jobID, Status: telemetry.JobQueued, Deduplicated: true})
}

// JobRejected counts queue-full/shutdown rejections (no row: nothing ran).
func (h *TelemetryHooks) JobRejected() { h.inc("jobs_rejected") }

// JobStarted records a job picked up by a worker.
func (h *TelemetryHooks) JobStarted(jobID string, worker int, at time.Time) {
	if h == nil || h.recorder == nil {
		return
	}
	h.recorder.RecordJob(telemetry.JobEvent{
		JobID:     jobID,
		StartedAt: at,
		Worker:    worker,
		Status:    telemetry.JobRunning,
	})
}

// JobFinished records the outcome of a job.
func (h *TelemetryHooks) JobFinished(jobID string, enqueuedAt time.Time, startedAt time.Time, finishedAt time.Time, succeeded bool, statusCode int, failureReason string, panicRecovered bool) {
	if h == nil || h.recorder == nil {
		return
	}
	status := telemetry.JobSucceeded
	if !succeeded {
		status = telemetry.JobFailed
		h.inc("jobs_failed")
	} else {
		h.inc("jobs_succeeded")
	}
	h.recorder.RecordJob(telemetry.JobEvent{
		JobID:          jobID,
		EnqueuedAt:     enqueuedAt,
		StartedAt:      startedAt,
		FinishedAt:     finishedAt,
		Status:         status,
		StatusCode:     statusCode,
		FailureReason:  telemetry.SanitizeText(failureReason, 200),
		PanicRecovered: panicRecovered,
	})
}

// JobExpired marks a completed job whose status record aged out.
func (h *TelemetryHooks) JobExpired(jobID string) {
	if h == nil || h.recorder == nil {
		return
	}
	h.inc("jobs_expired")
}

// RefreshQueueState publishes live queue gauges.
func (h *TelemetryHooks) RefreshQueueState(queueDepth int, queueCapacity int, activeWorkers int, workers int) {
	if h == nil || h.recorder == nil {
		return
	}
	h.recorder.SetGauge("refresh_queue_depth", int64(queueDepth))
	h.recorder.SetGauge("refresh_queue_capacity", int64(queueCapacity))
	h.recorder.SetGauge("refresh_active_workers", int64(activeWorkers))
	h.recorder.SetGauge("refresh_workers", int64(workers))
}

// UpstreamFetch records the outcome of one upstream Workshop fetch. Failures
// become structured error events; everything else only counts.
func (h *TelemetryHooks) UpstreamFetch(resourceKey string, statusCode int, duration time.Duration, err error, jobID string) {
	if h == nil {
		return
	}
	h.inc("upstream_fetches")
	if err == nil {
		return
	}
	h.inc("upstream_errors")
	category := ClassifyScrapeError(err, statusCode)
	if h.recorder != nil {
		h.recorder.RecordError(telemetry.ErrorEvent{
			JobID:       jobID,
			At:          time.Now(),
			Severity:    "error",
			Category:    "upstream_" + strings.TrimPrefix(category, "upstream_"),
			Message:     telemetry.SanitizeText(err.Error(), 300),
			RequestPath: telemetry.SanitizeText(resourceKey, 200),
			Status:      statusCode,
			Source:      "background",
		})
	}
}

// PersistentCache counts index-store lookups backing the in-memory cache.
func (h *TelemetryHooks) PersistentCache(status string) {
	h.inc("persistent_cache_" + strings.ToLower(strings.TrimSpace(status)))
}

// IndexEvent counts index-scheduler activity.
func (h *TelemetryHooks) IndexEvent(event string) { h.inc("index_" + event) }

// DatabaseError counts storage failures and surfaces them as error events.
func (h *TelemetryHooks) DatabaseError(component string, err error) {
	if h == nil {
		return
	}
	h.inc("database_errors")
	if h.recorder != nil && err != nil {
		h.recorder.RecordError(telemetry.ErrorEvent{
			At:       time.Now(),
			Severity: "error",
			Category: "database",
			Code:     component,
			Message:  telemetry.SanitizeText(err.Error(), 300),
			Source:   "background",
		})
	}
}

// ClassifyScrapeError buckets upstream fetch failures for error analytics.
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
