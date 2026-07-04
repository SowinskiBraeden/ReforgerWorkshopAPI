package util

import (
	"context"
	"errors"
	"testing"
	"time"
)

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
