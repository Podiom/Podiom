package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
)

func TestPatchConfigUpdatesPermissionTimeout(t *testing.T) {
	paths, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewBufferString(`{"permission_timeout":"5m"}`))
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	srv.handleConfig(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got globalConfigDTO
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.PermissionTimeout != "5m" {
		t.Fatalf("response permission_timeout = %q, want 5m", got.PermissionTimeout)
	}
	if live := srv.core.GetGlobal().PermissionTimeout; live != "5m" {
		t.Fatalf("live permission_timeout = %q, want 5m", live)
	}
	cfg, err := config.Load(paths.ConfigYAML)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Global.PermissionTimeout != "5m" {
		t.Fatalf("persisted permission_timeout = %q, want 5m", cfg.Global.PermissionTimeout)
	}
}

func TestPatchConfigRejectsInvalidPermissionTimeout(t *testing.T) {
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewBufferString(`{"permission_timeout":"0s"}`))
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	srv.handleConfig(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}
