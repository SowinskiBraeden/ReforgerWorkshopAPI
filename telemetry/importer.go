package telemetry

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Importer performs the one-time backfill of historical zap JSON log files
// into the telemetry database. It streams files line by line, converts raw
// IPs immediately into privacy-safe fields (the IP is never written), skips
// malformed lines while counting them, correlates job events by job ID, and
// is resumable and idempotent: per-file byte cursors live in import_files and
// every row carries a deterministic dedupe key.
type Importer struct {
	store      *Store
	anonymizer *Anonymizer
}

type ImporterConfig struct {
	HashSecret string
	Rotation   string
}

type ImportOptions struct {
	DryRun  bool
	FromDay string // inclusive YYYY-MM-DD, optional
	ToDay   string // inclusive YYYY-MM-DD, optional
	Fresh   bool   // ignore stored cursors and re-scan (dedupe keys still apply)
}

type ImportSummary struct {
	DryRun       bool      `json:"dryRun"`
	Files        int       `json:"files"`
	Requests     int       `json:"requests"`
	Jobs         int       `json:"jobs"`
	Errors       int       `json:"errors"`
	Logs         int       `json:"logs"`
	Malformed    int       `json:"malformed"`
	Skipped      int       `json:"skipped"` // outside date range or already imported
	FirstEventAt time.Time `json:"firstEventAt,omitempty"`
	LastEventAt  time.Time `json:"lastEventAt,omitempty"`
}

func NewImporter(store *Store, cfg ImporterConfig) *Importer {
	return &Importer{
		store:      store,
		anonymizer: NewAnonymizer(cfg.HashSecret, cfg.Rotation),
	}
}

// ImportDir imports every *.log file in a directory (sorted by name, which is
// by date for the daily log layout).
func (im *Importer) ImportDir(ctx context.Context, dir string, opts ImportOptions) (ImportSummary, error) {
	summary := ImportSummary{DryRun: opts.DryRun}
	paths, err := filepath.Glob(filepath.Join(dir, "*.log"))
	if err != nil {
		return summary, err
	}
	sort.Strings(paths)
	started := time.Now()
	for _, path := range paths {
		if err := im.importFile(ctx, path, opts, &summary); err != nil {
			return summary, fmt.Errorf("%s: %w", path, err)
		}
		summary.Files++
	}
	if !opts.DryRun {
		_, err = im.store.db.ExecContext(ctx, `INSERT INTO import_runs (
			started_at, finished_at, dry_run, from_day, to_day, files, requests,
			jobs, errors_recorded, logs, malformed, skipped
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			ms(started), ms(time.Now()), boolToInt(opts.DryRun), opts.FromDay, opts.ToDay,
			summary.Files, summary.Requests, summary.Jobs, summary.Errors,
			summary.Logs, summary.Malformed, summary.Skipped)
		if err != nil {
			return summary, err
		}
	}
	return summary, nil
}

// flexStatus tolerates the two shapes the historical logs use for "status":
// an HTTP integer on request lines, a state string ("succeeded") on job lines.
type flexStatus struct {
	Int  int
	Text string
}

func (f *flexStatus) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, `"`) {
		f.Text = strings.Trim(trimmed, `"`)
		return nil
	}
	_, err := fmt.Sscanf(trimmed, "%d", &f.Int)
	if err != nil {
		return nil // tolerate anything; the line still imports as a log event
	}
	return nil
}

// logLine is the union of fields across historical and current log formats.
type logLine struct {
	Level       string     `json:"level"`
	TS          string     `json:"ts"`
	Caller      string     `json:"caller"`
	Msg         string     `json:"msg"`
	RequestID   string     `json:"requestId"`
	JobID       string     `json:"jobId"`
	ClientIP    string     `json:"clientIP"`
	ClientIPAlt string     `json:"clientIp"`
	NetworkID   string     `json:"networkId"`
	CountryCode string     `json:"countryCode"`
	Method      string     `json:"method"`
	Route       string     `json:"route"`
	Path        string     `json:"path"`
	Query       string     `json:"query"`
	Status      flexStatus `json:"status"`
	StatusCode  int        `json:"statusCode"`
	LatencyMs   int64      `json:"latencyMs"`
	DurationMs  int64      `json:"durationMs"`
	APIClient   string     `json:"apiClient"`
	Client      string     `json:"client"`
	Source      string     `json:"source"`
	UserAgent   string     `json:"userAgent"`
	ResourceKey string     `json:"resourceKey"`
	ResourceURL string     `json:"resourceURL"`
	Priority    string     `json:"priority"`
	Worker      int        `json:"worker"`
	Key         string     `json:"key"`
	Error       string     `json:"error"`
	CacheStatus string     `json:"cacheStatus"`
}

func (im *Importer) importFile(ctx context.Context, path string, opts ImportOptions, summary *ImportSummary) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = filepath.Clean(path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	offset := int64(0)
	if !opts.Fresh {
		_ = im.store.db.QueryRowContext(ctx, `SELECT offset FROM import_files WHERE path = ?`, absPath).Scan(&offset)
	}
	if offset < 0 || offset > info.Size() {
		offset = 0
	}
	if offset == info.Size() {
		return nil // already fully imported
	}

	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if offset > 0 {
		if _, err := file.Seek(offset, 0); err != nil {
			return err
		}
	}

	var fromDay, toDay time.Time
	if opts.FromDay != "" {
		fromDay, _ = time.ParseInLocation("2006-01-02", opts.FromDay, time.UTC)
	}
	if opts.ToDay != "" {
		toDay, _ = time.ParseInLocation("2006-01-02", opts.ToDay, time.UTC)
		toDay = toDay.AddDate(0, 0, 1)
	}

	tx, err := im.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	flushed := 0
	malformedInFile := 0

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineStart := offset
	for scanner.Scan() {
		raw := scanner.Bytes()
		lineOffset := lineStart
		lineStart += int64(len(raw)) + 1

		var line logLine
		if err := json.Unmarshal(raw, &line); err != nil {
			summary.Malformed++
			malformedInFile++
			continue
		}
		at := parseLogTime(line.TS)
		if at.IsZero() {
			summary.Malformed++
			malformedInFile++
			continue
		}
		if (!fromDay.IsZero() && at.Before(fromDay)) || (!toDay.IsZero() && !at.Before(toDay)) {
			summary.Skipped++
			continue
		}
		if summary.FirstEventAt.IsZero() || at.Before(summary.FirstEventAt) {
			summary.FirstEventAt = at
		}
		if at.After(summary.LastEventAt) {
			summary.LastEventAt = at
		}

		dedupe := lineDedupeKey(absPath, lineOffset)
		if !opts.DryRun {
			if err := im.writeLine(ctx, tx, line, raw, at, dedupe, summary); err != nil {
				return err
			}
		} else {
			im.countLine(line, summary)
		}
		flushed++
		if flushed >= 500 && !opts.DryRun {
			if err := tx.Commit(); err != nil {
				return err
			}
			tx, err = im.store.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			flushed = 0
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !opts.DryRun {
		if _, err := tx.ExecContext(ctx, `INSERT INTO import_files (path, offset, size, malformed_lines, updated_at)
			VALUES (?,?,?,?,?) ON CONFLICT(path) DO UPDATE SET
			offset=excluded.offset, size=excluded.size,
			malformed_lines=import_files.malformed_lines+excluded.malformed_lines,
			updated_at=excluded.updated_at`,
			absPath, info.Size(), info.Size(), malformedInFile, ms(time.Now())); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		tx = nil
	}
	return nil
}

func (im *Importer) countLine(line logLine, summary *ImportSummary) {
	switch line.Msg {
	case "request completed":
		if IsIgnoredMetricsPath(line.Path) {
			break
		}
		summary.Requests++
	case "refresh job queued", "refresh job finished":
		summary.Jobs++
	default:
		if line.Level == "error" || line.Level == "fatal" {
			summary.Errors++
		}
	}
	summary.Logs++
}

func (im *Importer) writeLine(ctx context.Context, tx *sql.Tx, line logLine, raw []byte, at time.Time, dedupe string, summary *ImportSummary) error {
	// The raw IP (old log format) is converted to the anonymous network ID
	// here and then discarded; it never reaches any table.
	clientIP := firstNonBlank(line.ClientIP, line.ClientIPAlt)
	networkID := line.NetworkID
	if networkID == "" && clientIP != "" {
		networkID = im.anonymizer.NetworkID(clientIP, at)
	}
	status := line.Status.Int
	if status == 0 {
		status = line.StatusCode
	}

	switch line.Msg {
	case "request completed":
		if IsIgnoredMetricsPath(line.Path) {
			break
		}
		event := im.requestEventFromLine(line, at, networkID, status, dedupe)
		if err := insertRequestEvent(ctx, tx, event); err != nil {
			return err
		}
		summary.Requests++
	case "refresh job queued":
		if line.JobID != "" {
			if err := upsertJobEvent(ctx, tx, JobEvent{
				JobID:       line.JobID,
				RequestID:   line.RequestID,
				Kind:        jobKindFromLine(line),
				ResourceKey: SanitizeText(line.ResourceKey, 200),
				ResourceURL: SanitizePath(line.ResourceURL),
				Priority:    line.Priority,
				EnqueuedAt:  at,
				Status:      JobQueued,
			}); err != nil {
				return err
			}
			summary.Jobs++
		}
	case "refresh job finished":
		if line.JobID != "" {
			jobStatus := JobSucceeded
			if line.Status.Text == "failed" {
				jobStatus = JobFailed
			}
			finished := JobEvent{
				JobID:      line.JobID,
				RequestID:  line.RequestID,
				Status:     jobStatus,
				StatusCode: line.StatusCode,
				Worker:     line.Worker,
				FinishedAt: at,
			}
			if line.DurationMs > 0 {
				finished.StartedAt = at.Add(-time.Duration(line.DurationMs) * time.Millisecond)
			}
			if err := upsertJobEvent(ctx, tx, finished); err != nil {
				return err
			}
			summary.Jobs++
		}
	}

	if line.Level == "error" || line.Level == "fatal" {
		errorEvent := ErrorEvent{
			ErrorID:     "imp_" + dedupe,
			RequestID:   line.RequestID,
			JobID:       line.JobID,
			At:          at,
			Severity:    line.Level,
			Category:    "imported",
			Message:     SanitizeText(line.Msg+errSuffix(line.Error), 300),
			RequestPath: SanitizePath(line.Path),
			Status:      status,
			CountryCode: line.CountryCode,
			NetworkID:   networkID,
			Fingerprint: ErrorFingerprint("imported", "", line.Caller, line.Msg),
			Resolution:  ErrorOpen,
		}
		if err := insertErrorEvent(ctx, tx, errorEvent); err != nil {
			return err
		}
		summary.Errors++
	}

	// Every line lands in the log explorer, with IP-bearing fields dropped.
	fields := map[string]any{}
	_ = json.Unmarshal(raw, &fields)
	for _, drop := range []string{"level", "ts", "caller", "msg", "requestId", "jobId", "path", "query", "status", "statusCode", "countryCode", "networkId", "userAgent"} {
		delete(fields, drop)
	}
	for name := range fields {
		if IsRedactedLogField(name) {
			delete(fields, name)
		}
	}
	fieldsJSON := ""
	if len(fields) > 0 {
		if data, err := json.Marshal(fields); err == nil {
			// Historical fields may embed IPs (e.g. rate-limit buckets);
			// scrub anything address-shaped before storage.
			fieldsJSON = truncate(ScrubIPs(string(data)), 4000)
		}
	}
	logEvent := LogEvent{
		At:          at,
		Level:       line.Level,
		Caller:      line.Caller,
		Message:     SanitizeText(line.Msg, 300),
		RequestID:   line.RequestID,
		JobID:       line.JobID,
		Route:       line.Route,
		Path:        SanitizePath(line.Path),
		Status:      status,
		CountryCode: line.CountryCode,
		NetworkID:   networkID,
		ClientName:  SanitizeText(firstNonBlank(line.APIClient, line.Client), 80),
		CacheStatus: strings.ToUpper(line.CacheStatus),
		Fields:      fieldsJSON,
		DedupeKey:   dedupe,
	}
	if err := insertLogEvent(ctx, tx, logEvent); err != nil {
		return err
	}
	summary.Logs++
	return nil
}

func (im *Importer) requestEventFromLine(line logLine, at time.Time, networkID string, status int, dedupe string) RequestEvent {
	query := SanitizeQuery(line.Query)
	route := line.Route
	if route == "" {
		route = routeTemplateFromPath(line.Path)
	}
	group := EndpointGroup(route, query)
	clientName := SanitizeText(firstNonBlank(line.APIClient, line.Client), 80)
	if clientName == "" {
		clientName = ClientNameFromUserAgent(line.UserAgent)
	}
	source := line.Source
	if source == "" || source == "unknown" || source == "external" || source == "internal" || source == "internal-loopback" || source == "own-panel" {
		source = ClassifySource(ClassifyInput{
			RouteTemplate: route,
			Path:          line.Path,
			UserAgent:     line.UserAgent,
			AuthType:      AuthNone,
			IsInternalIP:  source == "internal" || source == "internal-loopback",
			IsAPIRoute:    strings.HasPrefix(route, "/v1/") || route == "/mod/{id}" || route == "/mods" || route == "/mods/{page}" || route == "/search",
			IsAdminRoute:  strings.HasPrefix(line.Path, "/internal"),
			IsHealthRoute: group == "health",
		})
	}
	duration := line.LatencyMs
	if duration == 0 {
		duration = line.DurationMs
	}
	term := ""
	if group == "search" {
		term = searchTermFromQuery(line.Query)
	}
	rateLimited := status == http.StatusTooManyRequests
	return RequestEvent{
		RequestID:     line.RequestID,
		At:            at,
		Method:        strings.ToUpper(firstNonBlank(line.Method, "GET")),
		RouteTemplate: route,
		RequestPath:   SanitizePath(line.Path),
		Query:         query,
		EndpointGroup: group,
		Status:        status,
		DurationMs:    float64(duration),
		APIVersion:    apiVersionFromRoute(route),
		Source:        source,
		ClientKind:    ClassifyClientKind(line.UserAgent),
		AuthType:      AuthNone,
		ClientName:    clientName,
		UserAgent:     SanitizeText(line.UserAgent, 240),
		CountryCode:   normalizeImportCountry(line.CountryCode),
		ResultCount:   -1,
		NetworkID:     networkID,
		IsHosting:     "unknown",
		RateLimited:   rateLimited,
		ErrorCategory: importErrorCategory(status, rateLimited),
		SearchTerm:    term,
		ModID:         modIDFromRoute(route, line.Path),
		DedupeKey:     "req:" + dedupe,
	}
}

func jobKindFromLine(line logLine) string {
	if line.RequestID == "" {
		return "index_refresh"
	}
	return JobKindCacheRefresh
}

func errSuffix(errText string) string {
	if strings.TrimSpace(errText) == "" {
		return ""
	}
	return ": " + errText
}

func parseLogTime(value string) time.Time {
	for _, layout := range []string{"2006-01-02T15:04:05.000Z0700", time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func lineDedupeKey(path string, offset int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", filepath.Base(path), offset)))
	return hex.EncodeToString(sum[:])[:20]
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// routeTemplateFromPath reconstructs a route template from an exact path in
// old logs that carried no template.
func routeTemplateFromPath(path string) string {
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	if path == "" {
		return "/"
	}
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "" {
			continue
		}
		if isAllDigits(part) {
			parts[i] = "{page}"
		} else if len(part) >= 10 && isIDLike(part) {
			parts[i] = "{id}"
		}
	}
	return strings.Join(parts, "/")
}

func isAllDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isIDLike(value string) bool {
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func apiVersionFromRoute(route string) string {
	if strings.HasPrefix(route, "/v1/") {
		return "v1"
	}
	switch route {
	case "/mod/{id}", "/mods", "/mods/{page}", "/search", "/health", "/refresh/jobs/{id}":
		return "legacy"
	}
	return ""
}

func importErrorCategory(status int, rateLimited bool) string {
	switch {
	case rateLimited:
		return "rate_limited"
	case status == http.StatusNotFound:
		return "not_found"
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "auth"
	case status >= 500:
		return "server_error"
	case status >= 400:
		return "client_error"
	default:
		return ""
	}
}

func searchTermFromQuery(rawQuery string) string {
	for _, pair := range strings.Split(rawQuery, "&") {
		if value, found := strings.CutPrefix(pair, "search="); found {
			if decoded, err := url.QueryUnescape(value); err == nil {
				return NormalizeSearchTerm(decoded)
			}
		}
	}
	return ""
}

func modIDFromRoute(route string, path string) string {
	if !strings.HasSuffix(route, "/mod/{id}") {
		return ""
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	id := strings.ToUpper(parts[len(parts)-1])
	if len(id) >= 8 && len(id) <= 64 && isIDLike(id) {
		return id
	}
	return ""
}

func normalizeImportCountry(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
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
