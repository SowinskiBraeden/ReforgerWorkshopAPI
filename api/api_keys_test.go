package api

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateAPIKeyUsesExpectedPrefixAndEntropy(t *testing.T) {
	key, err := GenerateAPIKey("sandbox", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key.Raw, "rfm_test_") {
		t.Fatalf("key prefix = %q, want rfm_test_", key.Raw)
	}
	if len(strings.TrimPrefix(key.Raw, "rfm_test_")) < 40 {
		t.Fatalf("token length too short: %d", len(strings.TrimPrefix(key.Raw, "rfm_test_")))
	}
}

func TestAPIKeyHashAndVerify(t *testing.T) {
	key, err := GenerateAPIKey("test", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if key.Hash == "" || key.Hash == key.Raw {
		t.Fatal("hash should be non-empty and should not equal raw key")
	}
	if !VerifyAPIKeyHash(key.Raw, key.Hash, "secret") {
		t.Fatal("expected raw key to verify against stored hash")
	}
	if VerifyAPIKeyHash(key.Raw+"x", key.Hash, "secret") {
		t.Fatal("modified key verified unexpectedly")
	}
}

func TestBillingStoreDoesNotStoreRawAPIKey(t *testing.T) {
	store, err := OpenBillingStore(filepath.Join(t.TempDir(), "billing.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.UpsertAccount(context.Background(), Account{Plan: PlanDeveloper, SubscriptionStatus: "active"})
	if err != nil {
		t.Fatal(err)
	}
	generated, err := GenerateAPIKey("test", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(context.Background(), APIKeyRecord{AccountID: account.ID, KeyHash: generated.Hash, KeyPrefix: generated.Prefix, Plan: PlanDeveloper, LastFour: generated.LastFour}); err != nil {
		t.Fatal(err)
	}
	record, _, ok, err := store.GetAPIKeyByHash(context.Background(), generated.Hash)
	if err != nil || !ok {
		t.Fatalf("lookup = (%+v, %v, %v)", record, ok, err)
	}
	if record.KeyHash == generated.Raw || record.KeyPrefix == generated.Raw {
		t.Fatal("raw key was stored")
	}
}
