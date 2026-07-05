package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDailyLogWriterSeparatesFilesByDay(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 4, 23, 59, 0, 0, time.UTC)
	writer, err := newDailyLogWriter(dir, func() time.Time { return now })
	if err != nil {
		t.Fatalf("newDailyLogWriter() error = %v", err)
	}

	if _, err := writer.Write([]byte("first\n")); err != nil {
		t.Fatalf("first write error = %v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := writer.Write([]byte("second\n")); err != nil {
		t.Fatalf("second write error = %v", err)
	}

	first, err := os.ReadFile(filepath.Join(dir, "2026-07-04.log"))
	if err != nil {
		t.Fatalf("read first log: %v", err)
	}
	if string(first) != "first\n" {
		t.Fatalf("first log = %q", string(first))
	}

	second, err := os.ReadFile(filepath.Join(dir, "2026-07-05.log"))
	if err != nil {
		t.Fatalf("read second log: %v", err)
	}
	if string(second) != "second\n" {
		t.Fatalf("second log = %q", string(second))
	}
}
