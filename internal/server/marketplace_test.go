package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/marketplace"
	"github.com/Podiom/Podiom/internal/skills"
)

func newMarketplaceTestServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv(skills.EnvHome, t.TempDir())
	svc, err := marketplace.New(marketplace.Options{Version: "test", Logger: slog.Default()})
	if err != nil {
		t.Fatalf("marketplace.New: %v", err)
	}
	return &Server{marketplace: svc, log: slog.Default()}
}

func TestHandleSkillInstall_Unavailable(t *testing.T) {
	srv := &Server{log: slog.Default()} // nil marketplace
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/skills/install", strings.NewReader("{}"))
	srv.handleSkillInstall(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestHandleSkillSearch_MethodNotAllowed(t *testing.T) {
	srv := newMarketplaceTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/skills/search", nil)
	srv.handleSkillSearch(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestHandleSkillInstall_MissingFields(t *testing.T) {
	srv := newMarketplaceTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/skills/install", strings.NewReader(`{}`))
	srv.handleSkillInstall(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty install request, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleSkillsInstalled_Empty(t *testing.T) {
	srv := newMarketplaceTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/skills/installed", nil)
	srv.handleSkillsInstalled(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("expected empty JSON array, got %q", rec.Body.String())
	}
}

func TestHandleSkillUninstall_NotManaged(t *testing.T) {
	srv := newMarketplaceTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/skills/installed/ghost", nil)
	srv.handleSkillInstalledItem(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unmanaged skill, got %d", rec.Code)
	}
}

func TestHandleSkillUpdate_MethodRouting(t *testing.T) {
	srv := newMarketplaceTestServer(t)
	// GET .../{name}/update on a non-managed skill → 404 (ErrNotManaged).
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/skills/installed/ghost/update", nil)
	srv.handleSkillInstalledItem(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
