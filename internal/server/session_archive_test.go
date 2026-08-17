package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/store"
)

func archiveRequest(sessionID, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/archive", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestSessionArchiveEndpointTogglesTheMarker(t *testing.T) {
	ctx := context.Background()
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude}); err != nil {
		t.Fatal(err)
	}
	session, err := srv.core.CreateSession(ctx, core.CreateSessionRequest{AgentName: "atlas", Origin: store.OriginWeb})
	if err != nil {
		t.Fatal(err)
	}

	// The user filing a conversation away by hand — a web session the daemon
	// would never archive on its own.
	archive := httptest.NewRecorder()
	srv.handleSession(archive, archiveRequest(session.ID, `{"archived":true}`))
	if archive.Code != http.StatusOK {
		t.Fatalf("archive status=%d body=%s", archive.Code, archive.Body.String())
	}
	var archived store.Session
	if err := json.Unmarshal(archive.Body.Bytes(), &archived); err != nil {
		t.Fatal(err)
	}
	if archived.ArchivedAt == "" {
		t.Fatalf("archived session = %+v, want an ArchivedAt stamp", archived)
	}

	unarchive := httptest.NewRecorder()
	srv.handleSession(unarchive, archiveRequest(session.ID, `{"archived":false}`))
	if unarchive.Code != http.StatusOK {
		t.Fatalf("unarchive status=%d body=%s", unarchive.Code, unarchive.Body.String())
	}
	var revived store.Session
	if err := json.Unmarshal(unarchive.Body.Bytes(), &revived); err != nil {
		t.Fatal(err)
	}
	if revived.ArchivedAt != "" {
		t.Fatalf("unarchived session = %+v, want an empty ArchivedAt", revived)
	}
}

func TestSessionArchiveEndpointRejectsBadRequests(t *testing.T) {
	ctx := context.Background()
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude}); err != nil {
		t.Fatal(err)
	}
	session, err := srv.core.CreateSession(ctx, core.CreateSessionRequest{AgentName: "atlas", Origin: store.OriginWeb})
	if err != nil {
		t.Fatal(err)
	}

	// An omitted "archived" must not read as false and silently unarchive.
	missing := httptest.NewRecorder()
	srv.handleSession(missing, archiveRequest(session.ID, `{}`))
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing archived status=%d, want 400", missing.Code)
	}

	wrongMethod := httptest.NewRecorder()
	srv.handleSession(wrongMethod, httptest.NewRequest(http.MethodGet, "/api/sessions/"+session.ID+"/archive", nil))
	if wrongMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET archive status=%d, want 405", wrongMethod.Code)
	}

	unknownSub := httptest.NewRecorder()
	srv.handleSession(unknownSub, httptest.NewRequest(http.MethodGet, "/api/sessions/"+session.ID+"/nope", nil))
	if unknownSub.Code != http.StatusNotFound {
		t.Fatalf("unknown sub-path status=%d, want 404", unknownSub.Code)
	}

	// A session that does not exist is a miss, not a malformed request.
	unknownSession := httptest.NewRecorder()
	srv.handleSession(unknownSession, archiveRequest("no-such-session", `{"archived":true}`))
	if unknownSession.Code != http.StatusNotFound {
		t.Fatalf("unknown session status=%d, want 404", unknownSession.Code)
	}
}

// writeJSON is shared by every REST handler, so the ErrNotFound mapping has to
// hold for reads and deletes too — not just the archive endpoint that exposed it.
func TestMissingRowsAnswer404(t *testing.T) {
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	cases := []struct {
		name string
		req  *http.Request
		call func(http.ResponseWriter, *http.Request)
	}{
		{
			name: "get session",
			req:  httptest.NewRequest(http.MethodGet, "/api/sessions/no-such-session", nil),
			call: srv.handleSession,
		},
		{
			name: "delete session",
			req:  httptest.NewRequest(http.MethodDelete, "/api/sessions/no-such-session", nil),
			call: srv.handleSession,
		},
		{
			name: "get agent",
			req:  httptest.NewRequest(http.MethodGet, "/api/agents/no-such-agent", nil),
			call: srv.handleAgent,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			tc.call(rr, tc.req)
			if rr.Code != http.StatusNotFound {
				t.Fatalf("status=%d, want 404; body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}
