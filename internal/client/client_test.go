package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/gateway"
)

// TestPostLongJSONSendsGatewayToken guards against a regression where
// postLongJSON built a bare *http.Client that bypassed the token transport,
// causing GenerateAgentSoul (its only caller) to 401 during onboarding.
func TestPostLongJSONSendsGatewayToken(t *testing.T) {
	const token = "test-gateway-token"

	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get(gateway.Header)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	c := New(addr, WithToken(token))

	if _, err := c.GenerateAgentSoul(context.Background(), "jared", AgentGenerateRequest{}); err != nil {
		t.Fatalf("GenerateAgentSoul: %v", err)
	}
	if gotToken != token {
		t.Fatalf("gateway token header = %q, want %q", gotToken, token)
	}
}
