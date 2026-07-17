package telemetry

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// LogFilter drives the structured-log explorer.
type LogFilter struct {
	From          time.Time
	To            time.Time
	Level         string
	Message       string // prefix match on the event message
	RequestID     string
	TraceID       string
	JobID         string
	Route         string
	Path          string
	Status        int
	ErrorCategory string
	CountryCode   string
	NetworkID     string
	AccountID     string
	ClientName    string
	APIKeyID      string
	CacheStatus   string
	InstanceID    string
	AppVersion    string
	Search        string // free text over message + fields
}

func (f LogFilter) where() (string, []any) {
	clauses := []string{"at >= ?", "at < ?"}
	args := []any{ms(f.From), ms(f.To)}
	add := func(clause string, value any) {
		clauses = append(clauses, clause)
		args = append(args, value)
	}
	if f.Level != "" {
		add("level = ?", strings.ToLower(f.Level))
	}
	if f.Message != "" {
		add("message LIKE ?", escapeLike(f.Message)+"%")
	}
	if f.RequestID != "" {
		add("request_id = ?", f.RequestID)
	}
	if f.TraceID != "" {
		add("trace_id = ?", f.TraceID)
	}
	if f.JobID != "" {
		add("job_id = ?", f.JobID)
	}
	if f.Route != "" {
		add("route = ?", f.Route)
	}
	if f.Path != "" {
		add("path LIKE ?", escapeLike(f.Path)+"%")
	}
	if f.Status > 0 {
		add("status = ?", f.Status)
	}
	if f.ErrorCategory != "" {
		add("error_category = ?", f.ErrorCategory)
	}
	if f.CountryCode != "" {
		add("country_code = ?", strings.ToUpper(f.CountryCode))
	}
	if f.NetworkID != "" {
		add("network_id = ?", f.NetworkID)
	}
	if f.AccountID != "" {
		add("account_id = ?", f.AccountID)
	}
	if f.ClientName != "" {
		add("client_name = ?", f.ClientName)
	}
	if f.APIKeyID != "" {
		add("api_key_id = ?", f.APIKeyID)
	}
	if f.CacheStatus != "" {
		add("cache_status = ?", strings.ToUpper(f.CacheStatus))
	}
	if f.InstanceID != "" {
		add("instance_id = ?", f.InstanceID)
	}
	if f.AppVersion != "" {
		add("app_version = ?", f.AppVersion)
	}
	if f.Search != "" {
		like := "%" + escapeLike(f.Search) + "%"
		clauses = append(clauses, "(message LIKE ? ESCAPE '\\' OR fields LIKE ? ESCAPE '\\')")
		args = append(args, like, like)
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

var logColumns = `id, at, level, caller, message, request_id, trace_id, job_id,
	route, path, status, error_category, country_code, network_id, account_id,
	client_name, api_key_id, cache_status, instance_id, app_version, fields`

func scanLogEvent(rows interface{ Scan(...any) error }) (LogEvent, error) {
	var e LogEvent
	var at int64
	err := rows.Scan(&e.ID, &at, &e.Level, &e.Caller, &e.Message, &e.RequestID, &e.TraceID,
		&e.JobID, &e.Route, &e.Path, &e.Status, &e.ErrorCategory, &e.CountryCode, &e.NetworkID,
		&e.AccountID, &e.ClientName, &e.APIKeyID, &e.CacheStatus, &e.InstanceID, &e.AppVersion, &e.Fields)
	if err != nil {
		return e, err
	}
	e.At = fromMs(at)
	return e, nil
}

// ListLogs returns a page of matching log lines plus total count.
func (s *Store) ListLogs(ctx context.Context, filter LogFilter, limit int, offset int) ([]LogEvent, int, error) {
	where, args := filter.where()
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM structured_logs `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+logColumns+` FROM structured_logs `+where+
		` ORDER BY at DESC, id DESC LIMIT ? OFFSET ?`, append(args, clampLimit(limit), maxOffset(offset))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []LogEvent
	for rows.Next() {
		event, err := scanLogEvent(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, event)
	}
	return out, total, rows.Err()
}

// LogContext bundles a log line with everything correlated to it.
type LogContext struct {
	Event      LogEvent      `json:"event"`
	Correlated []LogEvent    `json:"correlated,omitempty"` // same request/trace/job ID
	Nearby     []LogEvent    `json:"nearby,omitempty"`     // time neighbours on the same instance
	Request    *RequestEvent `json:"request,omitempty"`
	Job        *JobRecord    `json:"job,omitempty"`
	Errors     []ErrorEvent  `json:"errors,omitempty"`
}

// GetLogContext loads one log line with correlated and nearby events.
func (s *Store) GetLogContext(ctx context.Context, id int64) (LogContext, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+logColumns+` FROM structured_logs WHERE id = ?`, id)
	event, err := scanLogEvent(row)
	if err == sql.ErrNoRows {
		return LogContext{}, false, nil
	}
	if err != nil {
		return LogContext{}, false, err
	}
	out := LogContext{Event: event}

	if event.RequestID != "" || event.JobID != "" || event.TraceID != "" {
		rows, err := s.db.QueryContext(ctx, `SELECT `+logColumns+` FROM structured_logs
			WHERE id <> ? AND (
				(request_id <> '' AND request_id = ?) OR
				(job_id <> '' AND job_id = ?) OR
				(trace_id <> '' AND trace_id = ?)
			) ORDER BY at LIMIT 100`, id, event.RequestID, event.JobID, event.TraceID)
		if err != nil {
			return out, true, err
		}
		defer rows.Close()
		for rows.Next() {
			correlated, err := scanLogEvent(rows)
			if err != nil {
				return out, true, err
			}
			out.Correlated = append(out.Correlated, correlated)
		}
		if err := rows.Err(); err != nil {
			return out, true, err
		}
	}

	nearbyRows, err := s.db.QueryContext(ctx, `SELECT `+logColumns+` FROM (
			SELECT `+logColumns+` FROM structured_logs WHERE id < ? ORDER BY id DESC LIMIT 5
		) UNION ALL SELECT * FROM (
			SELECT `+logColumns+` FROM structured_logs WHERE id > ? ORDER BY id ASC LIMIT 5
		) ORDER BY id`, id, id)
	if err != nil {
		return out, true, err
	}
	defer nearbyRows.Close()
	for nearbyRows.Next() {
		nearby, err := scanLogEvent(nearbyRows)
		if err != nil {
			return out, true, err
		}
		out.Nearby = append(out.Nearby, nearby)
	}
	if err := nearbyRows.Err(); err != nil {
		return out, true, err
	}

	if event.RequestID != "" {
		if request, ok, err := s.GetRequest(ctx, event.RequestID); err == nil && ok {
			out.Request = &request
		}
	}
	if event.JobID != "" {
		if job, ok, err := s.GetJob(ctx, event.JobID); err == nil && ok {
			out.Job = &job
		}
	}
	if event.RequestID != "" {
		errors, _, err := s.ListErrors(ctx, ErrorFilter{
			From: event.At.Add(-24 * time.Hour), To: event.At.Add(24 * time.Hour),
			RequestID: event.RequestID,
		}, 10, 0)
		if err == nil {
			out.Errors = errors
		}
	}
	return out, true, nil
}

// ErrorFilter drives the error explorer.
type ErrorFilter struct {
	From        time.Time
	To          time.Time
	Severity    string
	Category    string
	Code        string
	Route       string
	Status      int
	Resolution  string
	Fingerprint string
	RequestID   string
	AccountID   string
	ClientName  string
	AppVersion  string
	Search      string
}

func (f ErrorFilter) where() (string, []any) {
	clauses := []string{"at >= ?", "at < ?"}
	args := []any{ms(f.From), ms(f.To)}
	add := func(clause string, value any) {
		clauses = append(clauses, clause)
		args = append(args, value)
	}
	if f.Severity != "" {
		add("severity = ?", strings.ToLower(f.Severity))
	}
	if f.Category != "" {
		add("category = ?", f.Category)
	}
	if f.Code != "" {
		add("code = ?", f.Code)
	}
	if f.Route != "" {
		add("route_template = ?", f.Route)
	}
	if f.Status > 0 {
		add("status = ?", f.Status)
	}
	if f.Resolution != "" {
		add("resolution = ?", strings.ToLower(f.Resolution))
	}
	if f.Fingerprint != "" {
		add("fingerprint = ?", f.Fingerprint)
	}
	if f.RequestID != "" {
		add("request_id = ?", f.RequestID)
	}
	if f.AccountID != "" {
		add("account_id = ?", f.AccountID)
	}
	if f.ClientName != "" {
		add("client_name = ?", f.ClientName)
	}
	if f.AppVersion != "" {
		add("app_version = ?", f.AppVersion)
	}
	if f.Search != "" {
		like := "%" + escapeLike(f.Search) + "%"
		clauses = append(clauses, "(message LIKE ? ESCAPE '\\' OR request_path LIKE ? ESCAPE '\\')")
		args = append(args, like, like)
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

var errorColumns = `id, error_id, request_id, trace_id, job_id, at, severity,
	category, code, message, stack, method, route_template, request_path,
	status, source, account_id, api_key_id, client_name, country_code,
	network_id, app_version, fingerprint, resolution, notes`

func scanErrorEvent(rows interface{ Scan(...any) error }) (ErrorEvent, error) {
	var e ErrorEvent
	var at int64
	err := rows.Scan(&e.ID, &e.ErrorID, &e.RequestID, &e.TraceID, &e.JobID, &at, &e.Severity,
		&e.Category, &e.Code, &e.Message, &e.Stack, &e.Method, &e.RouteTemplate, &e.RequestPath,
		&e.Status, &e.Source, &e.AccountID, &e.APIKeyID, &e.ClientName, &e.CountryCode,
		&e.NetworkID, &e.AppVersion, &e.Fingerprint, &e.Resolution, &e.Notes)
	if err != nil {
		return e, err
	}
	e.At = fromMs(at)
	return e, nil
}

func (s *Store) ListErrors(ctx context.Context, filter ErrorFilter, limit int, offset int) ([]ErrorEvent, int, error) {
	where, args := filter.where()
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM request_errors `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+errorColumns+` FROM request_errors `+where+
		` ORDER BY at DESC LIMIT ? OFFSET ?`, append(args, clampLimit(limit), maxOffset(offset))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []ErrorEvent
	for rows.Next() {
		event, err := scanErrorEvent(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, event)
	}
	return out, total, rows.Err()
}

func (s *Store) GetError(ctx context.Context, errorID string) (ErrorEvent, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+errorColumns+` FROM request_errors WHERE error_id = ?`, errorID)
	event, err := scanErrorEvent(row)
	if err == sql.ErrNoRows {
		return event, false, nil
	}
	if err != nil {
		return event, false, err
	}
	return event, true, nil
}

// UpdateErrorResolution moves an error pattern (all rows sharing the
// fingerprint) through the triage workflow.
func (s *Store) UpdateErrorResolution(ctx context.Context, errorID string, resolution string, notes string, wholePattern bool) error {
	resolution = strings.ToLower(strings.TrimSpace(resolution))
	switch resolution {
	case ErrorOpen, ErrorAcknowledged, ErrorInvestigating, ErrorResolved, ErrorIgnored:
	default:
		return sql.ErrNoRows
	}
	now := ms(s.now())
	if wholePattern {
		_, err := s.db.ExecContext(ctx, `UPDATE request_errors SET resolution = ?, notes = ?, updated_at = ?
			WHERE fingerprint = (SELECT fingerprint FROM request_errors WHERE error_id = ?)`,
			resolution, notes, now, errorID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE request_errors SET resolution = ?, notes = ?, updated_at = ?
		WHERE error_id = ?`, resolution, notes, now, errorID)
	return err
}

// ErrorPattern summarizes one fingerprint for the errors dashboard.
type ErrorPattern struct {
	Fingerprint string    `json:"fingerprint"`
	Category    string    `json:"category"`
	Code        string    `json:"code"`
	Message     string    `json:"message"`
	Route       string    `json:"route"`
	Count       int64     `json:"count"`
	FirstAt     time.Time `json:"firstAt"`
	LastAt      time.Time `json:"lastAt"`
	Resolution  string    `json:"resolution"`
	IsNew       bool      `json:"isNew"` // first seen inside the selected range
	SampleID    string    `json:"sampleId"`
}

// ErrorPatterns groups errors by fingerprint over a range and flags patterns
// first seen inside the range as new.
func (s *Store) ErrorPatterns(ctx context.Context, from time.Time, to time.Time, limit int) ([]ErrorPattern, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.fingerprint, e.category, e.code,
		e.message, e.route_template, counts.n, counts.first_at, counts.last_at,
		e.resolution, e.error_id,
		CASE WHEN NOT EXISTS (
			SELECT 1 FROM request_errors older
			WHERE older.fingerprint = e.fingerprint AND older.at < ?
		) THEN 1 ELSE 0 END AS is_new
		FROM (
			SELECT fingerprint, COUNT(1) AS n, MIN(at) AS first_at, MAX(at) AS last_at, MAX(id) AS last_id
			FROM request_errors WHERE at >= ? AND at < ? GROUP BY fingerprint
		) counts JOIN request_errors e ON e.id = counts.last_id
		ORDER BY counts.n DESC LIMIT ?`, ms(from), ms(from), ms(to), clampLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ErrorPattern
	for rows.Next() {
		var p ErrorPattern
		var firstAt, lastAt int64
		var isNew int
		if err := rows.Scan(&p.Fingerprint, &p.Category, &p.Code, &p.Message, &p.Route,
			&p.Count, &firstAt, &lastAt, &p.Resolution, &p.SampleID, &isNew); err != nil {
			return nil, err
		}
		p.FirstAt, p.LastAt = fromMs(firstAt), fromMs(lastAt)
		p.IsNew = isNew == 1
		out = append(out, p)
	}
	return out, rows.Err()
}

// JobRecord is one background job row.
type JobRecord struct {
	JobID          string    `json:"jobId"`
	RequestID      string    `json:"requestId,omitempty"`
	ParentJobID    string    `json:"parentJobId,omitempty"`
	Kind           string    `json:"kind"`
	ResourceKey    string    `json:"resourceKey"`
	ResourceURL    string    `json:"resourceUrl,omitempty"`
	Queue          string    `json:"queue,omitempty"`
	Priority       string    `json:"priority,omitempty"`
	EnqueuedAt     time.Time `json:"enqueuedAt"`
	StartedAt      time.Time `json:"startedAt,omitempty"`
	FinishedAt     time.Time `json:"finishedAt,omitempty"`
	QueueWaitMs    float64   `json:"queueWaitMs"`
	DurationMs     float64   `json:"durationMs"`
	Worker         int       `json:"worker,omitempty"`
	Attempt        int       `json:"attempt"`
	Status         string    `json:"status"`
	StatusCode     int       `json:"statusCode,omitempty"`
	FailureReason  string    `json:"failureReason,omitempty"`
	Deduplicated   bool      `json:"deduplicated"`
	PanicRecovered bool      `json:"panicRecovered"`
}

// JobFilter drives the background-jobs explorer.
type JobFilter struct {
	From        time.Time
	To          time.Time
	Kind        string
	Status      string
	ResourceKey string
	RequestID   string
	MinMs       float64
}

func (f JobFilter) where() (string, []any) {
	clauses := []string{"enqueued_at >= ?", "enqueued_at < ?"}
	args := []any{ms(f.From), ms(f.To)}
	if f.Kind != "" {
		clauses = append(clauses, "kind = ?")
		args = append(args, f.Kind)
	}
	if f.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, f.Status)
	}
	if f.ResourceKey != "" {
		clauses = append(clauses, "resource_key LIKE ? ESCAPE '\\'")
		args = append(args, "%"+escapeLike(f.ResourceKey)+"%")
	}
	if f.RequestID != "" {
		clauses = append(clauses, "request_id = ?")
		args = append(args, f.RequestID)
	}
	if f.MinMs > 0 {
		clauses = append(clauses, "duration_ms >= ?")
		args = append(args, f.MinMs)
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

var jobColumns = `job_id, request_id, parent_job_id, kind, resource_key,
	resource_url, queue, priority, enqueued_at, started_at, finished_at,
	queue_wait_ms, duration_ms, worker, attempt, status, status_code,
	failure_reason, deduplicated, panic_recovered`

func scanJob(rows interface{ Scan(...any) error }) (JobRecord, error) {
	var j JobRecord
	var enqueued, started, finished int64
	var deduplicated, panicked int
	err := rows.Scan(&j.JobID, &j.RequestID, &j.ParentJobID, &j.Kind, &j.ResourceKey,
		&j.ResourceURL, &j.Queue, &j.Priority, &enqueued, &started, &finished,
		&j.QueueWaitMs, &j.DurationMs, &j.Worker, &j.Attempt, &j.Status, &j.StatusCode,
		&j.FailureReason, &deduplicated, &panicked)
	if err != nil {
		return j, err
	}
	j.EnqueuedAt, j.StartedAt, j.FinishedAt = fromMs(enqueued), fromMs(started), fromMs(finished)
	j.Deduplicated = deduplicated == 1
	j.PanicRecovered = panicked == 1
	return j, nil
}

func (s *Store) ListJobs(ctx context.Context, filter JobFilter, orderBy string, limit int, offset int) ([]JobRecord, int, error) {
	where, args := filter.where()
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM background_jobs `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "enqueued_at DESC"
	if orderBy == "slowest" {
		order = "duration_ms DESC"
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobColumns+` FROM background_jobs `+where+
		` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(args, clampLimit(limit), maxOffset(offset))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []JobRecord
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, job)
	}
	return out, total, rows.Err()
}

func (s *Store) GetJob(ctx context.Context, jobID string) (JobRecord, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM background_jobs WHERE job_id = ?`, jobID)
	job, err := scanJob(row)
	if err == sql.ErrNoRows {
		return job, false, nil
	}
	if err != nil {
		return job, false, err
	}
	return job, true, nil
}

// JobStats summarizes job outcomes for a range.
type JobStats struct {
	Total          int64   `json:"total"`
	Succeeded      int64   `json:"succeeded"`
	Failed         int64   `json:"failed"`
	Running        int64   `json:"running"`
	Queued         int64   `json:"queued"`
	Expired        int64   `json:"expired"`
	Deduplicated   int64   `json:"deduplicated"`
	Retried        int64   `json:"retried"`
	PanicRecovered int64   `json:"panicRecovered"`
	AvgDurationMs  float64 `json:"avgDurationMs"`
	AvgQueueWaitMs float64 `json:"avgQueueWaitMs"`
	P95DurationMs  float64 `json:"p95DurationMs"`
}

func (s *Store) JobStatsFor(ctx context.Context, filter JobFilter) (JobStats, error) {
	where, args := filter.where()
	var out JobStats
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(1),
		COALESCE(SUM(CASE WHEN status = 'succeeded' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = 'running' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = 'queued' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = 'expired' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(deduplicated), 0),
		COALESCE(SUM(CASE WHEN attempt > 1 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(panic_recovered), 0),
		COALESCE(AVG(CASE WHEN duration_ms > 0 THEN duration_ms END), 0),
		COALESCE(AVG(CASE WHEN queue_wait_ms > 0 THEN queue_wait_ms END), 0)
		FROM background_jobs `+where, args...)
	if err := row.Scan(&out.Total, &out.Succeeded, &out.Failed, &out.Running, &out.Queued,
		&out.Expired, &out.Deduplicated, &out.Retried, &out.PanicRecovered,
		&out.AvgDurationMs, &out.AvgQueueWaitMs); err != nil {
		return out, err
	}
	durationRows, err := s.db.QueryContext(ctx, `SELECT duration_ms FROM background_jobs `+where+` AND duration_ms > 0`, args...)
	if err != nil {
		return out, err
	}
	defer durationRows.Close()
	var durations []float64
	for durationRows.Next() {
		var d float64
		if err := durationRows.Scan(&d); err != nil {
			return out, err
		}
		durations = append(durations, d)
	}
	if len(durations) > 0 {
		stats := durationStats(durations)
		out.P95DurationMs = stats.p95
	}
	return out, durationRows.Err()
}
