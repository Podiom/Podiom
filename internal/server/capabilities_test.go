package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mar-schmidt/Podium/internal/capabilities"
)

func TestProviderCapabilitiesEndpoint(t *testing.T) {
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/provider-capabilities?provider=codex", nil)
	rr := httptest.NewRecorder()
	srv.handleProviderCapabilities(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got capabilities.ProviderCapabilities
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Provider != "codex" || len(got.Models) == 0 || len(got.Efforts) == 0 {
		t.Fatalf("bad capabilities: %+v", got)
	}
}
