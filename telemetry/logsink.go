package telemetry

import (
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap/zapcore"
)

// LogSink is a zapcore.Core that mirrors structured log events into the
// telemetry database so the admin log explorer can search and correlate them.
// It is attached with zapcore.NewTee next to the stdout/file cores; recording
// is asynchronous through the Recorder so logging never blocks on SQLite.
type LogSink struct {
	zapcore.LevelEnabler
	recorder   *Recorder
	instanceID string
	appVersion string
	fields     []zapcore.Field
}

func NewLogSink(recorder *Recorder, level zapcore.LevelEnabler, instanceID string, appVersion string) *LogSink {
	return &LogSink{
		LevelEnabler: level,
		recorder:     recorder,
		instanceID:   instanceID,
		appVersion:   appVersion,
	}
}

func (s *LogSink) With(fields []zapcore.Field) zapcore.Core {
	clone := *s
	clone.fields = append(append([]zapcore.Field(nil), s.fields...), fields...)
	return &clone
}

func (s *LogSink) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if s.Enabled(entry.Level) {
		return checked.AddCore(entry, s)
	}
	return checked
}

func (s *LogSink) Sync() error { return nil }

func (s *LogSink) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	if s.recorder == nil {
		return nil
	}
	encoder := zapcore.NewMapObjectEncoder()
	for _, field := range s.fields {
		field.AddTo(encoder)
	}
	for _, field := range fields {
		field.AddTo(encoder)
	}
	event := LogEvent{
		At:         entry.Time,
		Level:      entry.Level.String(),
		Caller:     entry.Caller.TrimmedPath(),
		Message:    SanitizeText(entry.Message, 300),
		InstanceID: s.instanceID,
		AppVersion: s.appVersion,
	}
	rest := make(map[string]any, len(encoder.Fields))
	for name, value := range encoder.Fields {
		if IsRedactedLogField(name) {
			continue
		}
		switch strings.ToLower(name) {
		case "requestid", "request_id":
			event.RequestID = stringValue(value)
		case "traceid", "trace_id":
			event.TraceID = stringValue(value)
		case "jobid", "job_id":
			event.JobID = stringValue(value)
		case "route", "routetemplate":
			event.Route = stringValue(value)
		case "path":
			event.Path = SanitizePath(stringValue(value))
		case "status", "statuscode":
			event.Status = intValue(value)
		case "errorcategory", "error_category":
			event.ErrorCategory = stringValue(value)
		case "countrycode", "country_code":
			event.CountryCode = stringValue(value)
		case "networkid", "network_id":
			event.NetworkID = stringValue(value)
		case "accountid", "account_id":
			event.AccountID = stringValue(value)
		case "client", "clientname", "apiclient":
			event.ClientName = SanitizeText(stringValue(value), 80)
		case "keyid", "key_id", "apikeyid":
			event.APIKeyID = stringValue(value)
		case "cachestatus", "cache_status", "x-cache":
			event.CacheStatus = stringValue(value)
		default:
			rest[name] = value
		}
	}
	if len(rest) > 0 {
		if data, err := json.Marshal(rest); err == nil {
			// Catch-all: no address-shaped value may reach storage even if a
			// log call site slips one into a field.
			event.Fields = truncate(ScrubIPs(string(data)), 4000)
		}
	}
	s.recorder.RecordLog(event)
	return nil
}

func stringValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

func intValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int8:
		return int(v)
	case int16:
		return int(v)
	case int32:
		return int(v)
	case int64:
		return int(v)
	case uint:
		return int(v)
	case uint8:
		return int(v)
	case uint16:
		return int(v)
	case uint32:
		return int(v)
	case uint64:
		return int(v)
	case float32:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

var _ zapcore.Core = (*LogSink)(nil)
