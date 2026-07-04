package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/gateway"
	"github.com/Podiom/Podiom/internal/store"
	"nhooyr.io/websocket"
)

func newWSAuthServer(t *testing.T) (*httptest.Server, *gateway.Keeper, *Server) {
	t.Helper()
	home := t.TempDir()
	paths := config.NewPaths(home)
	if _, err := config.Scaffold(paths); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	db, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	coreSvc, err := core.New(core.Options{Paths: paths, Store: db, Adapter: adapter.NewFake()})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	keeper, _, err := gateway.LoadOrCreate(paths.GatewayToken)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Options{Bind: "127.0.0.1", Port: 0, Core: coreSvc, Paths: paths, Tokens: keeper})
	ts := httptest.NewServer(srv.httpSrv.Handler)
	t.Cleanup(ts.Close)
	return ts, keeper, srv
}

func TestWebSocketHandshakeRequiresToken(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ts, keeper, _ := newWSAuthServer(t)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws"

	// No token: rejected at the handshake (HA7).
	if _, resp, err := websocket.Dial(ctx, wsURL, nil); err == nil {
		t.Fatal("dial without token succeeded, want rejection")
	} else if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("handshake response = %v, want 401", resp)
	}

	// Token via subprotocol (the browser carrier): accepted, and the server
	// echoes only the non-secret protocol.
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		Subprotocols: []string{gateway.WSProtocol, gateway.WSProtocolEntry(keeper.Current())},
	})
	if err != nil {
		t.Fatalf("dial with subprotocol token: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if got := conn.Subprotocol(); got != gateway.WSProtocol {
		t.Fatalf("negotiated subprotocol = %q, want %q", got, gateway.WSProtocol)
	}
}

func TestTokenRotationClosesLiveSocketsWith4401(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ts, keeper, _ := newWSAuthServer(t)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws"

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		Subprotocols: []string{gateway.WSProtocol, gateway.WSProtocolEntry(keeper.Current())},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/token/rotate", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(gateway.Header, keeper.Current())
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rotate status = %d", resp.StatusCode)
	}

	// The live socket must be force-closed with the app close code so open
	// tabs re-prompt for the token instead of retrying forever (HA12).
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, _, err := conn.Read(ctx)
		if err != nil {
			if code := websocket.CloseStatus(err); code != wsCloseTokenRotated {
				t.Fatalf("close status = %d, want %d (err=%v)", code, wsCloseTokenRotated, err)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("socket not closed after rotation")
		}
	}
}
