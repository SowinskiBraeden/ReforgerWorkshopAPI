package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/models"
)

func TestIndexStoreCacheEntryReadWrite(t *testing.T) {
	store := newTestIndexStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	entry := CacheEntryFromResponse("v1:mods:1::popularity:", CachedResponse{
		StatusCode: http.StatusOK,
		Body:       []byte(`{"status":"success"}`),
		TTL:        time.Minute,
		Stale:      time.Hour,
	}, now)
	if err := store.PutCacheEntry(ctx, entry); err != nil {
		t.Fatalf("PutCacheEntry error = %v", err)
	}

	got, ok, err := store.GetCacheEntry(ctx, entry.CacheKey)
	if err != nil {
		t.Fatalf("GetCacheEntry error = %v", err)
	}
	if !ok {
		t.Fatal("cache entry was not found")
	}
	if string(got.Body) != `{"status":"success"}` {
		t.Fatalf("body = %s", got.Body)
	}
	if !got.FreshUntil.Equal(now.Add(time.Minute)) {
		t.Fatalf("fresh until = %s", got.FreshUntil)
	}
}

func TestResponseCachePromotesPersistentFreshEntry(t *testing.T) {
	store := newTestIndexStore(t)
	cfg := testConfig()
	cache := NewResponseCache(cfg)
	cache.SetIndexStore(store)
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	cache.now = func() time.Time { return now }
	store.now = cache.now

	key := "v1:mods:persistent"
	if err := store.PutCacheEntry(context.Background(), CacheEntryFromResponse(key, CachedResponse{
		StatusCode: http.StatusOK,
		Body:       []byte(`{"from":"sqlite"}`),
		TTL:        time.Minute,
		Stale:      time.Hour,
	}, now)); err != nil {
		t.Fatalf("PutCacheEntry error = %v", err)
	}

	entry, status := cache.lookupPersistent(context.Background(), key, now)
	if entry == nil || status != "HIT" {
		t.Fatalf("persistent lookup status = %s entry=%v, want HIT", status, entry)
	}
	mem, memStatus := cache.lookup(key, now)
	if mem == nil || memStatus != "HIT" || string(mem.response.Body) != `{"from":"sqlite"}` {
		t.Fatalf("memory promotion status=%s entry=%+v", memStatus, mem)
	}
}

func TestResponseCachePromotesPersistentStaleEntry(t *testing.T) {
	store := newTestIndexStore(t)
	cfg := testConfig()
	cache := NewResponseCache(cfg)
	cache.SetIndexStore(store)
	created := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	now := created.Add(2 * time.Minute)
	cache.now = func() time.Time { return now }
	store.now = func() time.Time { return created }

	key := "v1:mods:persistent-stale"
	if err := store.PutCacheEntry(context.Background(), CacheEntryFromResponse(key, CachedResponse{
		StatusCode: http.StatusOK,
		Body:       []byte(`{"from":"stale"}`),
		TTL:        time.Minute,
		Stale:      time.Hour,
	}, created)); err != nil {
		t.Fatalf("PutCacheEntry error = %v", err)
	}

	entry, status := cache.lookupPersistent(context.Background(), key, now)
	if entry == nil || status != "STALE" {
		t.Fatalf("persistent lookup status = %s entry=%v, want STALE", status, entry)
	}
}

func TestIndexStoreListPageAndModSearch(t *testing.T) {
	store := newTestIndexStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	preview := models.ModPreview{
		ID:             "ABC123",
		Name:           "RHS Status Quo",
		Author:         "RHS Team",
		ImageURL:       "https://example.test/image.jpg",
		OriginalModURL: "https://reforger.armaplatform.com/workshop/ABC123",
		APIModURL:      "https://api.reforgermods.net/v1/mod/ABC123",
		Size:           "2 GB",
	}
	if err := store.UpsertModPreview(ctx, preview); err != nil {
		t.Fatalf("UpsertModPreview error = %v", err)
	}
	if err := store.PutListPage(ctx, ListPageRecord{
		CacheKey:        "v1:mods:1:rhs:popularity:",
		Page:            1,
		Sort:            "popularity",
		Search:          "rhs",
		ResourceType:    "mods",
		ModIDsJSON:      `["abc123"]`,
		Body:            []byte(`{"status":"success"}`),
		FreshUntil:      now.Add(time.Minute),
		StaleUntil:      now.Add(time.Hour),
		LastRefreshedAt: now,
	}); err != nil {
		t.Fatalf("PutListPage error = %v", err)
	}
	page, ok, err := store.GetListPage(ctx, "v1:mods:1:rhs:popularity:")
	if err != nil || !ok {
		t.Fatalf("GetListPage ok=%v err=%v", ok, err)
	}
	if page.ModIDsJSON != `["abc123"]` {
		t.Fatalf("mod ids = %s", page.ModIDsJSON)
	}
	results, err := store.SearchMods(ctx, "rhs", 16, 0)
	if err != nil {
		t.Fatalf("SearchMods error = %v", err)
	}
	if len(results) != 1 || results[0].ID != "ABC123" {
		t.Fatalf("results = %+v", results)
	}
}

func TestIndexStoreModDetailReadWrite(t *testing.T) {
	store := newTestIndexStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	if err := store.PutModDetail(ctx, ModDetailRecord{
		ModID:           "ABC123",
		Body:            []byte(`{"status":"success"}`),
		FreshUntil:      now.Add(time.Minute),
		StaleUntil:      now.Add(time.Hour),
		LastRefreshedAt: now,
	}); err != nil {
		t.Fatalf("PutModDetail error = %v", err)
	}
	detail, ok, err := store.GetModDetail(ctx, "abc123")
	if err != nil || !ok {
		t.Fatalf("GetModDetail ok=%v err=%v", ok, err)
	}
	if string(detail.Body) != `{"status":"success"}` {
		t.Fatalf("body = %s", detail.Body)
	}
	if detail.ModID != "ABC123" {
		t.Fatalf("detail mod id = %q, want original casing", detail.ModID)
	}
}

func TestIndexStoreOpenFailsForCorruptDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.db")
	if err := os.WriteFile(path, []byte("not sqlite"), 0600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	store, err := OpenIndexStore(path)
	if err == nil {
		_ = store.Close()
		t.Fatal("OpenIndexStore succeeded for corrupt database")
	}
}

func newTestIndexStore(t *testing.T) *IndexStore {
	t.Helper()
	store, err := OpenIndexStore(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("OpenIndexStore error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
