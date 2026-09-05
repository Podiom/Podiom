package client

import (
	"context"
	"errors"
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

func TestSharedHelpersMapDaemonUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := strings.TrimPrefix(srv.URL, "http://")
	srv.Close()

	c := New(addr)

	t.Run("GET", func(t *testing.T) {
		_, err := c.ListProfiles(context.Background())
		if !errors.Is(err, ErrDaemonUnreachable) {
			t.Fatalf("ListProfiles error = %v, want ErrDaemonUnreachable", err)
		}
	})

	t.Run("POST", func(t *testing.T) {
		_, err := c.CreateProfile(context.Background(), ProfileRequest{})
		if !errors.Is(err, ErrDaemonUnreachable) {
			t.Fatalf("CreateProfile error = %v, want ErrDaemonUnreachable", err)
		}
	})
}

func TestRemainingDaemonRequestsMapDaemonUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := strings.TrimPrefix(srv.URL, "http://")
	srv.Close()

	c := New(addr)

	assertUnreachable := func(t *testing.T, err error) {
		t.Helper()
		if !errors.Is(err, ErrDaemonUnreachable) {
			t.Fatalf("error = %v, want ErrDaemonUnreachable", err)
		}
	}

	t.Run("PUT", func(t *testing.T) {
		_, err := c.UpdateProfile(context.Background(), "test", ProfileRequest{})
		assertUnreachable(t, err)
	})

	t.Run("DELETE", func(t *testing.T) {
		err := c.DeleteProfile(context.Background(), "test")
		assertUnreachable(t, err)
	})

	t.Run("RUN_SCHEDULE", func(t *testing.T) {
		_, err := c.RunSchedule(context.Background(), "test")
		assertUnreachable(t, err)
	})

	t.Run("USAGE_REFRESH", func(t *testing.T) {
		_, err := c.Usage(context.Background(), true)
		assertUnreachable(t, err)
	})

	t.Run("CHAT", func(t *testing.T) {
		_, errs := c.Chat(context.Background(), ChatRequest{})
		err := <-errs
		assertUnreachable(t, err)
	})
}
