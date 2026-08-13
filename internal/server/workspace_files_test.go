package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/gateway"
	"github.com/Podiom/Podiom/internal/store"
)

func TestWorkspaceFileCreateAndAuthenticatedGet(t *testing.T) {
	ctx := context.Background()
	paths, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude}); err != nil {
		t.Fatal(err)
	}
	session, err := srv.core.CreateSession(ctx, core.CreateSessionRequest{AgentName: "atlas", Origin: store.OriginWeb})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.AgentsDir, "atlas", "workspace", "post.md"), []byte("Read me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"session_id": session.ID, "path": "post.md", "label": "Reddit post"})
	create := httptest.NewRecorder()
	srv.handleWorkspaceFiles(create, httptest.NewRequest(http.MethodPost, "/api/workspace-files", bytes.NewReader(body)))
	if create.Code != http.StatusOK {
		t.Fatalf("create status = %d; body=%s", create.Code, create.Body.String())
	}
	var result core.WorkspaceFileSnapshotResult
	if err := json.Unmarshal(create.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Snapshot.CreatorSessionID != session.ID || result.Snapshot.CreatorAgent != "atlas" || result.MarkdownLink != "[Reddit post](api/workspace-files/"+result.Snapshot.ID+")" {
		t.Fatalf("create result = %+v", result)
	}

	keeper, _, err := gateway.LoadOrCreate(paths.GatewayToken)
	if err != nil {
		t.Fatal(err)
	}
	authedServer := New(Options{Core: srv.core, Paths: paths, Tokens: keeper})
	path := "/api/workspace-files/" + result.Snapshot.ID
	if got := serve(authedServer, httptest.NewRequest(http.MethodGet, path, nil)); got.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated get status = %d, want 401", got.Code)
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set(gateway.Header, keeper.Current())
	got := serve(authedServer, req)
	if got.Code != http.StatusOK {
		t.Fatalf("authenticated get status = %d; body=%s", got.Code, got.Body.String())
	}
	var snapshot store.WorkspaceFileSnapshot
	if err := json.Unmarshal(got.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Content != "Read me\n" || snapshot.SourcePath != "post.md" || got.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("get snapshot = %+v; headers=%v", snapshot, got.Header())
	}

	missingReq := httptest.NewRequest(http.MethodGet, "/api/workspace-files/missing", nil)
	missingReq.Header.Set(gateway.Header, keeper.Current())
	if missing := serve(authedServer, missingReq); missing.Code != http.StatusNotFound {
		t.Fatalf("missing get status = %d, want 404; body=%s", missing.Code, missing.Body.String())
	}
}
