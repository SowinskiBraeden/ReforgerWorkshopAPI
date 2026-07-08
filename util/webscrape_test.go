package util

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestScenarioField(t *testing.T) {
	raw := "Scenario ID{39AB5D9094E502AA}Missions/OG_Conflict.confGame modeConflictPlayer count64"

	tests := []struct {
		label     string
		nextLabel string
		want      string
	}{
		{"Scenario ID", "Game mode", "{39AB5D9094E502AA}Missions/OG_Conflict.conf"},
		{"Game mode", "Player count", "Conflict"},
		{"Player count", "", "64"},
		{"Missing", "", ""},
	}

	for _, tt := range tests {
		got := scenarioField(raw, tt.label, tt.nextLabel)
		if got != tt.want {
			t.Errorf("scenarioField(%q, %q): got %q, want %q", tt.label, tt.nextLabel, got, tt.want)
		}
	}
}

func TestScraperHonorsContextWhenConcurrencyIsExhausted(t *testing.T) {
	ConfigureScraper(ScraperConfig{
		Timeout:     time.Second,
		Retries:     0,
		Concurrency: 1,
		UserAgent:   "test-agent",
	})

	if err := acquireScraper(context.Background()); err != nil {
		t.Fatalf("failed to acquire scraper slot: %v", err)
	}
	defer releaseScraper()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ScrapeModsContext(ctx, 1, "", "", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
