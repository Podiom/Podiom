package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/store"
	"nhooyr.io/websocket"
)

func TestRunAutoArchiveBroadcastsChangedSession(t *testing.T) {
	ctx := context.Background()
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	defer srv.autoArchiveCancel()
	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude}); err != nil {
		t.Fatal(err)
	}
	session, err := srv.core.CreateSession(ctx, core.CreateSessionRequest{AgentName: "atlas", Origin: store.OriginWeb})
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv.httpSrv.Handler)
	defer ts.Close()
	conn := dialWSTest(t, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/ws")
	defer conn.Close(websocket.StatusNormalClosure, "")
	readWSTestUntil(t, conn, "initial state", func(msg ServerMessage) bool { return msg.Type == "state" })

	srv.runAutoArchive(time.Now().UTC().Add(8 * 24 * time.Hour))
	msg := readWSTestUntil(t, conn, "auto-archive session broadcast", func(msg ServerMessage) bool {
		return msg.Type == "session" && msg.Session != nil && msg.Session.ID == session.ID
	})
	if msg.Session.ArchivedAt == "" {
		t.Fatalf("broadcast session = %+v, want archive marker", msg.Session)
	}
}
