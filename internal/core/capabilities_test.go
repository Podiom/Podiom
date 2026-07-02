package core

import (
	"context"
	"os"
	"testing"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/capabilities"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
)

type countingCapabilitiesAdapter struct {
	*adapter.Fake
	calls int
}

func (a *countingCapabilitiesAdapter) Capabilities(ctx context.Context, req capabilities.Request) (capabilities.ProviderCapabilities, error) {
	a.calls++
	caps := capabilities.Fallback(req.Provider, req.Profile)
	caps.Source = "counting"
	caps.Stale = false
	return caps, ctx.Err()
}

func TestProviderCapabilitiesCachesUntilRefresh(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	if _, err := config.Scaffold(paths); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if err := os.WriteFile(paths.BaseAgents, []byte("base layer\n"), 0o644); err != nil {
		t.Fatalf("write base agents: %v", err)
	}
	db, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	ad := &countingCapabilitiesAdapter{Fake: adapter.NewFake()}
	c, err := New(Options{Paths: paths, Store: db, Adapter: ad})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	if _, err := c.ProviderCapabilities(context.Background(), config.ProviderCodex, "", false); err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	if _, err := c.ProviderCapabilities(context.Background(), config.ProviderCodex, "", false); err != nil {
		t.Fatalf("cached capabilities: %v", err)
	}
	if ad.calls != 1 {
		t.Fatalf("calls = %d, want 1", ad.calls)
	}
	if _, err := c.ProviderCapabilities(context.Background(), config.ProviderCodex, "", true); err != nil {
		t.Fatalf("refresh capabilities: %v", err)
	}
	if ad.calls != 2 {
		t.Fatalf("calls after refresh = %d, want 2", ad.calls)
	}
}
