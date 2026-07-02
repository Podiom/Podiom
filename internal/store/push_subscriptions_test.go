package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPushSubscriptionUpsertListDelete(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "podiom.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	sub := PushSubscription{
		Kind:     "webpush",
		Endpoint: "https://push.example/abc",
		Payload:  `{"endpoint":"https://push.example/abc","keys":{"p256dh":"pk","auth":"ak"}}`,
	}
	saved, err := db.UpsertPushSubscription(ctx, sub)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if saved.ID == "" {
		t.Fatal("expected generated ID")
	}
	if saved.CreatedAt == "" {
		t.Fatal("expected created_at")
	}

	// Re-subscribing the same endpoint updates in place (no duplicate row).
	sub.Payload = `{"endpoint":"https://push.example/abc","keys":{"p256dh":"pk2","auth":"ak2"}}`
	if _, err := db.UpsertPushSubscription(ctx, sub); err != nil {
		t.Fatalf("upsert again: %v", err)
	}

	list, err := db.ListPushSubscriptions(ctx, "webpush")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 subscription after upsert, got %d", len(list))
	}
	if list[0].Payload != sub.Payload {
		t.Fatalf("payload not updated on upsert: %q", list[0].Payload)
	}

	// A different kind is filtered out.
	if _, err := db.UpsertPushSubscription(ctx, PushSubscription{Kind: "apns", Endpoint: "device-token-1", Payload: "{}"}); err != nil {
		t.Fatalf("upsert apns: %v", err)
	}
	webpushOnly, err := db.ListPushSubscriptions(ctx, "webpush")
	if err != nil {
		t.Fatalf("list webpush: %v", err)
	}
	if len(webpushOnly) != 1 {
		t.Fatalf("kind filter failed: got %d webpush rows", len(webpushOnly))
	}
	all, err := db.ListPushSubscriptions(ctx, "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 rows across kinds, got %d", len(all))
	}

	if err := db.DeletePushSubscriptionByEndpoint(ctx, "https://push.example/abc"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Deleting an absent endpoint is not an error (idempotent pruning).
	if err := db.DeletePushSubscriptionByEndpoint(ctx, "https://push.example/abc"); err != nil {
		t.Fatalf("delete idempotent: %v", err)
	}
	remaining, err := db.ListPushSubscriptions(ctx, "webpush")
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected 0 webpush rows after delete, got %d", len(remaining))
	}
}
