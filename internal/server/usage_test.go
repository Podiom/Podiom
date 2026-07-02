package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mar-schmidt/Podium/internal/config"
	"github.com/mar-schmidt/Podium/internal/usage"
)

func TestHandleUsageUnavailable(t *testing.T) {
	s := New(Options{})
	req := httptest.NewRequest(http.MethodGet, "/api/usage", nil)
	rr := httptest.NewRecorder()
	s.handleUsage(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

func TestHandleUsageReturnsSnapshots(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	tr := usage.New(usage.Options{Profiles: func() []config.Profile { return nil }})
	// Populate the cache without launching the poll loop.
	tr.Refresh(context.Background(), true)
	s := New(Options{Usage: tr})

	req := httptest.NewRequest(http.MethodGet, "/api/usage", nil)
	rr := httptest.NewRecorder()
	s.handleUsage(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var snaps []usage.Snapshot
	if err := json.Unmarshal(rr.Body.Bytes(), &snaps); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Two implicit defaults, both with empty temp dirs -> no_credentials.
	if len(snaps) != 2 {
		t.Fatalf("snapshots = %d", len(snaps))
	}
	for _, snap := range snaps {
		if snap.Status != usage.StatusNoCredentials {
			t.Errorf("%s status = %q", snap.Profile, snap.Status)
		}
	}
}

func TestHandleUsageMethodNotAllowed(t *testing.T) {
	tr := usage.New(usage.Options{Profiles: func() []config.Profile { return nil }})
	s := New(Options{Usage: tr})
	req := httptest.NewRequest(http.MethodPost, "/api/usage", nil)
	rr := httptest.NewRecorder()
	s.handleUsage(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
}
