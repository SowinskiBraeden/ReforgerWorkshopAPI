package telemetry

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const (
	recorderQueueSize    = 4096
	recorderBatchSize    = 256
	recorderFlushEvery   = 2 * time.Second
	recorderCloseTimeout = 10 * time.Second
)

// Recorder persists telemetry asynchronously. Producers never block on the
// database: events go into a bounded queue drained by one writer goroutine
// that batches inserts inside transactions. Dropped events (full queue) are
// counted and surfaced on the health page rather than silently lost.
type Recorder struct {
	store *Store
	queue chan any
	done  chan struct{}
	wg    sync.WaitGroup

	dropped   atomic.Int64
	written   atomic.Int64
	writeErrs atomic.Int64
	lastError atomic.Value // string

	// live gauges reported by other subsystems; not persisted per-sample.
	gauges sync.Map // name -> int64
}

func NewRecorder(store *Store) *Recorder {
	r := &Recorder{
		store: store,
		queue: make(chan any, recorderQueueSize),
		done:  make(chan struct{}),
	}
	r.wg.Add(1)
	go r.run()
	return r
}

// RecordRequest enqueues one request event. Exactly one call is made per
// inbound HTTP request by the telemetry middleware.
func (r *Recorder) RecordRequest(event RequestEvent) { r.enqueue(event) }

// RecordError enqueues a structured error event.
func (r *Recorder) RecordError(event ErrorEvent) {
	if event.ErrorID == "" {
		event.ErrorID = NewID("err")
	}
	if event.At.IsZero() {
		event.At = time.Now()
	}
	if event.Fingerprint == "" {
		event.Fingerprint = ErrorFingerprint(event.Category, event.Code, event.RouteTemplate, event.Message)
	}
	if event.Resolution == "" {
		event.Resolution = ErrorOpen
	}
	r.enqueue(event)
}

// RecordJob upserts a background job row keyed by JobID.
func (r *Recorder) RecordJob(event JobEvent) { r.enqueue(event) }

// RecordLog enqueues a structured log line for the explorer.
func (r *Recorder) RecordLog(event LogEvent) { r.enqueue(event) }

// SetGauge publishes a live gauge (queue depth, workers, cache entries…)
// shown on the health/real-time pages.
func (r *Recorder) SetGauge(name string, value int64) {
	if r == nil {
		return
	}
	r.gauges.Store(name, value)
}

// Gauge returns a live gauge value.
func (r *Recorder) Gauge(name string) int64 {
	if r == nil {
		return 0
	}
	if v, ok := r.gauges.Load(name); ok {
		if n, ok := v.(int64); ok {
			return n
		}
	}
	return 0
}

// Gauges returns a copy of all live gauges.
func (r *Recorder) Gauges() map[string]int64 {
	out := map[string]int64{}
	if r == nil {
		return out
	}
	r.gauges.Range(func(k any, v any) bool {
		if name, ok := k.(string); ok {
			if n, ok := v.(int64); ok {
				out[name] = n
			}
		}
		return true
	})
	return out
}

// Stats reports recorder health for the system-health page.
func (r *Recorder) Stats() (written int64, dropped int64, writeErrors int64, lastError string) {
	if r == nil {
		return 0, 0, 0, ""
	}
	if v, ok := r.lastError.Load().(string); ok {
		lastError = v
	}
	return r.written.Load(), r.dropped.Load(), r.writeErrs.Load(), lastError
}

func (r *Recorder) enqueue(event any) {
	if r == nil {
		return
	}
	select {
	case r.queue <- event:
	default:
		r.dropped.Add(1)
	}
}

// Close flushes pending events and stops the writer.
func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	close(r.done)
	waited := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(waited)
	}()
	select {
	case <-waited:
		return nil
	case <-time.After(recorderCloseTimeout):
		return fmt.Errorf("telemetry recorder close timed out")
	}
}

func (r *Recorder) run() {
	defer r.wg.Done()
	ticker := time.NewTicker(recorderFlushEvery)
	defer ticker.Stop()
	batch := make([]any, 0, recorderBatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := r.writeBatch(batch); err != nil {
			r.writeErrs.Add(1)
			r.lastError.Store(err.Error())
		} else {
			r.written.Add(int64(len(batch)))
		}
		batch = batch[:0]
	}
	for {
		select {
		case event := <-r.queue:
			batch = append(batch, event)
			if len(batch) >= recorderBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-r.done:
			// Drain whatever is already queued, then exit.
			for {
				select {
				case event := <-r.queue:
					batch = append(batch, event)
					if len(batch) >= recorderBatchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

func (r *Recorder) writeBatch(batch []any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, item := range batch {
		switch event := item.(type) {
		case RequestEvent:
			err = insertRequestEvent(ctx, tx, event)
		case ErrorEvent:
			err = insertErrorEvent(ctx, tx, event)
		case JobEvent:
			err = upsertJobEvent(ctx, tx, event)
		case LogEvent:
			err = insertLogEvent(ctx, tx, event)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func insertRequestEvent(ctx context.Context, tx *sql.Tx, e RequestEvent) error {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO request_events (
		request_id, trace_id, at, method, route_template, request_path, query,
		endpoint_group, status, duration_ms, response_bytes, request_bytes,
		api_version, source, client_kind, auth_type, account_id, api_key_id,
		api_client_id, client_name, client_version, client_verified, user_agent,
		country_code, asn, network_name, network_id, is_hosting, via_proxy,
		cache_status, refresh_result, rate_limited, rate_limit_limit, rate_bucket,
		error_category, error_code, search_term, result_count, mod_id,
		app_version, instance_id, dedupe_key
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.RequestID, e.TraceID, ms(e.At), e.Method, e.RouteTemplate, e.RequestPath, e.Query,
		e.EndpointGroup, e.Status, e.DurationMs, e.ResponseBytes, e.RequestBytes,
		e.APIVersion, e.Source, e.ClientKind, e.AuthType, e.AccountID, e.APIKeyID,
		e.APIClientID, e.ClientName, e.ClientVersion, boolToInt(e.ClientVerified), e.UserAgent,
		e.CountryCode, e.ASN, e.NetworkName, e.NetworkID, e.IsHosting, boolToInt(e.ViaProxy),
		e.CacheStatus, e.RefreshResult, boolToInt(e.RateLimited), e.RateLimitLimit, e.RateBucket,
		e.ErrorCategory, e.ErrorCode, e.SearchTerm, e.ResultCount, e.ModID,
		e.AppVersion, e.InstanceID, nullableText(e.DedupeKey),
	)
	return err
}

func insertErrorEvent(ctx context.Context, tx *sql.Tx, e ErrorEvent) error {
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO request_errors (
		error_id, request_id, trace_id, job_id, at, severity, category, code,
		message, stack, method, route_template, request_path, status, source,
		account_id, api_key_id, client_name, country_code, network_id,
		app_version, fingerprint, resolution, notes, updated_at, dedupe_key
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ErrorID, e.RequestID, e.TraceID, e.JobID, ms(e.At), e.Severity, e.Category, e.Code,
		e.Message, e.Stack, e.Method, e.RouteTemplate, e.RequestPath, e.Status, e.Source,
		e.AccountID, e.APIKeyID, e.ClientName, e.CountryCode, e.NetworkID,
		e.AppVersion, e.Fingerprint, e.Resolution, e.Notes, ms(e.At), nullableText(""),
	)
	return err
}

func upsertJobEvent(ctx context.Context, tx *sql.Tx, e JobEvent) error {
	if e.JobID == "" {
		e.JobID = NewID("job")
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO background_jobs (
		job_id, request_id, parent_job_id, kind, resource_key, resource_url,
		queue, priority, enqueued_at, started_at, finished_at, queue_wait_ms,
		duration_ms, worker, attempt, status, status_code, failure_reason,
		deduplicated, panic_recovered
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(job_id) DO UPDATE SET
		request_id=CASE WHEN excluded.request_id <> '' THEN excluded.request_id ELSE background_jobs.request_id END,
		started_at=CASE WHEN excluded.started_at > 0 THEN excluded.started_at ELSE background_jobs.started_at END,
		finished_at=CASE WHEN excluded.finished_at > 0 THEN excluded.finished_at ELSE background_jobs.finished_at END,
		queue_wait_ms=CASE WHEN excluded.queue_wait_ms > 0 THEN excluded.queue_wait_ms ELSE background_jobs.queue_wait_ms END,
		duration_ms=CASE WHEN excluded.duration_ms > 0 THEN excluded.duration_ms ELSE background_jobs.duration_ms END,
		worker=CASE WHEN excluded.worker > 0 THEN excluded.worker ELSE background_jobs.worker END,
		attempt=MAX(excluded.attempt, background_jobs.attempt),
		status=excluded.status,
		status_code=CASE WHEN excluded.status_code > 0 THEN excluded.status_code ELSE background_jobs.status_code END,
		failure_reason=CASE WHEN excluded.failure_reason <> '' THEN excluded.failure_reason ELSE background_jobs.failure_reason END,
		deduplicated=MAX(excluded.deduplicated, background_jobs.deduplicated),
		panic_recovered=MAX(excluded.panic_recovered, background_jobs.panic_recovered)`,
		e.JobID, e.RequestID, e.ParentJobID, e.Kind, e.ResourceKey, e.ResourceURL,
		e.Queue, e.Priority, ms(e.EnqueuedAt), ms(e.StartedAt), ms(e.FinishedAt), queueWaitMs(e),
		durationMs(e), e.Worker, e.Attempt, e.Status, e.StatusCode, e.FailureReason,
		boolToInt(e.Deduplicated), boolToInt(e.PanicRecovered),
	)
	return err
}

func insertLogEvent(ctx context.Context, tx *sql.Tx, e LogEvent) error {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO structured_logs (
		at, level, caller, message, request_id, trace_id, job_id, route, path,
		status, error_category, country_code, network_id, account_id,
		client_name, api_key_id, cache_status, instance_id, app_version,
		fields, dedupe_key
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		ms(e.At), e.Level, e.Caller, e.Message, e.RequestID, e.TraceID, e.JobID, e.Route, e.Path,
		e.Status, e.ErrorCategory, e.CountryCode, e.NetworkID, e.AccountID,
		e.ClientName, e.APIKeyID, e.CacheStatus, e.InstanceID, e.AppVersion,
		e.Fields, nullableText(e.DedupeKey),
	)
	return err
}

func queueWaitMs(e JobEvent) float64 {
	if !e.StartedAt.IsZero() && !e.EnqueuedAt.IsZero() && e.StartedAt.After(e.EnqueuedAt) {
		return float64(e.StartedAt.Sub(e.EnqueuedAt).Microseconds()) / 1000
	}
	return 0
}

func durationMs(e JobEvent) float64 {
	if !e.FinishedAt.IsZero() && !e.StartedAt.IsZero() && e.FinishedAt.After(e.StartedAt) {
		return float64(e.FinishedAt.Sub(e.StartedAt).Microseconds()) / 1000
	}
	return 0
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// ErrorFingerprint groups similar errors for new-vs-recurring analytics.
func ErrorFingerprint(category string, code string, route string, message string) string {
	sum := sha256.Sum256([]byte(category + "\x00" + code + "\x00" + route + "\x00" + truncate(message, 120)))
	return hex.EncodeToString(sum[:])[:16]
}

func truncate(value string, maxLen int) string {
	if len(value) > maxLen {
		return value[:maxLen]
	}
	return value
}
