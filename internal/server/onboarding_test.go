package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/gateway"
	"github.com/Podiom/Podiom/internal/onboardstate"
)

func newHAOnboardingServer(t *testing.T) (*Server, *gateway.Keeper, config.Paths) {
	t.Helper()
	paths := config.NewPaths(t.TempDir())
	keeper, _, err := gateway.LoadOrCreate(paths.GatewayToken)
	if err != nil {
		t.Fatal(err)
	}
	return New(Options{Paths: paths, Tokens: keeper, HAMode: true}), keeper, paths
}

func TestHAOnboardingStateIsBootstrapReadable(t *testing.T) {
	s, _, _ := newHAOnboardingServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/onboarding", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rr := serve(s, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var out onboardingResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Completed {
		t.Fatalf("completed = true, want false")
	}
}

func TestHAOnboardingTokenRequiresCompletedState(t *testing.T) {
	s, keeper, paths := newHAOnboardingServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/onboarding/token", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rr := serve(s, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("before completion: status = %d, want 403", rr.Code)
	}
	if _, err := onboardstate.MarkComplete(paths.Onboarding, time.Now()); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/onboarding/token", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rr = serve(s, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("after completion: status = %d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Token != keeper.Current() {
		t.Fatalf("token = %q, want keeper token", out.Token)
	}
}

func TestOnboardingEndpointsUnavailableOutsideHAMode(t *testing.T) {
	s, keeper := newAuthedServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/onboarding/token", nil)
	rr := serve(s, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("without gateway token: status = %d, want auth gate first", rr.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/onboarding/token", nil)
	req.Header.Set(gateway.Header, keeper.Current())
	rr = serve(s, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("with gateway token: status = %d, want 404", rr.Code)
	}
}
