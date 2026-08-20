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
	"github.com/Podiom/Podiom/internal/projects"
	"github.com/Podiom/Podiom/internal/store"
)

// TestSessionContextReportsLinksWithoutHistory pins the two things that make
// this endpoint safe to hand an agent: it answers "where am I and what have I
// made here", and it never replays the transcript.
func TestSessionContextReportsLinksWithoutHistory(t *testing.T) {
	ctx := context.Background()
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := srv.core.CreateSession(ctx, core.CreateSessionRequest{AgentName: "atlas", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := srv.core.CreateTask(ctx, store.Task{
		Title:            "Benchmark the candidates",
		CreatedBySession: session.ID,
		CreatedByAgent:   "atlas",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	// A task the agent did not create must not show up as its work.
	if _, err := srv.core.CreateTask(ctx, store.Task{Title: "Ship the release"}); err != nil {
		t.Fatalf("create unrelated task: %v", err)
	}

	rr := httptest.NewRecorder()
	srv.handleSessionContext(rr, httptest.NewRequest(http.MethodGet, "/api/session-context/"+session.ID, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}

	var got sessionContext
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SessionID != session.ID || got.AgentName != "atlas" {
		t.Fatalf("identity wrong: %+v", got)
	}
	if len(got.CreatedTasks) != 1 || got.CreatedTasks[0] != "Benchmark the candidates" {
		t.Fatalf("created tasks = %v, want only the one this session made", got.CreatedTasks)
	}
	// A web session has someone watching; only schedule/roadmap/goal runs do not.
	if got.Unattended {
		t.Errorf("web session reported as unattended")
	}
	// The transcript must never ride along — that is the whole reason this
	// endpoint exists instead of reusing /api/sessions/<id>.
	for _, banned := range []string{`"history"`, `"rolling_summary"`, `"RollingSummary"`} {
		if strings.Contains(rr.Body.String(), banned) {
			t.Errorf("session context leaked %s", banned)
		}
	}
}

func TestSessionContextRejectsBadRequests(t *testing.T) {
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	rr := httptest.NewRecorder()
	srv.handleSessionContext(rr, httptest.NewRequest(http.MethodGet, "/api/session-context/", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("missing session id: status = %d, want 400", rr.Code)
	}

	rr = httptest.NewRecorder()
	srv.handleSessionContext(rr, httptest.NewRequest(http.MethodPost, "/api/session-context/abc", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status = %d, want 405", rr.Code)
	}
}

func TestSessionContextPatchUpdatesProject(t *testing.T) {
	ctx := context.Background()
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := srv.core.CreateProject(ctx, projects.Project{ID: "demo", Name: "Demo"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := srv.core.CreateSession(ctx, core.CreateSessionRequest{AgentName: "atlas", Origin: store.OriginWeb})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/session-context/"+session.ID, strings.NewReader(`{"project_id":"demo"}`))
	srv.handleSessionContext(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Session store.Session `json:"session"`
		Message string        `json:"message"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Session.ID != session.ID || got.Session.ProjectID != "demo" || got.Session.ProviderHandle != "" {
		t.Fatalf("updated session = %+v", got.Session)
	}
	if !strings.Contains(got.Message, "next turn") {
		t.Fatalf("response message = %q", got.Message)
	}

	rr = httptest.NewRecorder()
	srv.handleSessionContext(rr, httptest.NewRequest(http.MethodPatch, "/api/session-context/"+session.ID, strings.NewReader(`{}`)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing project_id status = %d, want 400", rr.Code)
	}
}
