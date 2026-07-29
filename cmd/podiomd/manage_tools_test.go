package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/gateway"
)

// recordingServer captures the last request a tool made so tests can assert the
// method, path, and body it produced.
type recordingServer struct {
	ts       *httptest.Server
	method   string
	path     string
	rawQuery string
	body     map[string]json.RawMessage
	respond  func(w http.ResponseWriter, r *http.Request)
}

func newRecordingServer(t *testing.T) (*recordingServer, *manageClient) {
	t.Helper()
	rec := &recordingServer{}
	rec.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		rec.rawQuery = r.URL.RawQuery
		rec.body = nil
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			_ = json.Unmarshal(raw, &rec.body)
		}
		if rec.respond != nil {
			rec.respond(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(rec.ts.Close)
	addr := strings.TrimPrefix(rec.ts.URL, "http://")
	return rec, newManageClient(addr)
}

func toolByName(c *manageClient, name string) (mcpTool, bool) {
	for _, tl := range manageTools(c, "sess-1", "atlas") {
		if tl.Name == name {
			return tl, true
		}
	}
	return mcpTool{}, false
}

func callTool(t *testing.T, c *manageClient, name string, args map[string]any) (string, error) {
	t.Helper()
	tl, ok := toolByName(c, name)
	if !ok {
		t.Fatalf("tool %q not found", name)
	}
	raw, _ := json.Marshal(args)
	return tl.Handler(context.Background(), raw)
}

func TestManageToolRegistryInvariants(t *testing.T) {
	tools := manageTools(newManageClient("127.0.0.1:8787"), "", "")
	// tasks 6 + projects 5 + schedules 4 + skills 4 + mcp 5 + goals 10 + platform 5.
	if len(tools) != 39 {
		t.Fatalf("expected 39 tools, got %d", len(tools))
	}
	seen := map[string]bool{}
	destructive := map[string]bool{
		"podiom_delete_task": true, "podiom_delete_project": true,
		"podiom_delete_schedule": true, "podiom_uninstall_skill": true,
		"podiom_remove_mcp_server": true,
	}
	for _, tl := range tools {
		if !strings.HasPrefix(tl.Name, "podiom_") {
			t.Errorf("tool %q missing podiom_ prefix", tl.Name)
		}
		if seen[tl.Name] {
			t.Errorf("duplicate tool name %q", tl.Name)
		}
		seen[tl.Name] = true
		if strings.TrimSpace(tl.Description) == "" {
			t.Errorf("tool %q missing description", tl.Name)
		}
		if tl.InputSchema["type"] != "object" {
			t.Errorf("tool %q schema type = %v", tl.Name, tl.InputSchema["type"])
		}
		if tl.Handler == nil {
			t.Errorf("tool %q missing handler", tl.Name)
		}
		if destructive[tl.Name] {
			req, _ := tl.InputSchema["required"].([]string)
			if !contains(req, "confirm") {
				t.Errorf("destructive tool %q must require confirm, got required=%v", tl.Name, req)
			}
		}
	}
	for name := range destructive {
		if !seen[name] {
			t.Errorf("expected destructive tool %q to exist", name)
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestCreateTaskPostsBody(t *testing.T) {
	rec, c := newRecordingServer(t)
	if _, err := callTool(t, c, "podiom_create_task", map[string]any{
		"project_id": "proj", "title": "Do it", "plan_required": true,
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if rec.method != http.MethodPost || rec.path != "/api/tasks" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
	if _, ok := rec.body["title"]; !ok {
		t.Fatalf("body missing title: %v", rec.body)
	}
	if _, ok := rec.body["assigned_agent"]; ok {
		t.Fatalf("body should omit unspecified assigned_agent: %v", rec.body)
	}
}

// TestStartTaskPostsUnattended pins the flag that makes an agent-initiated start
// actually run: an MCP caller has no browser to send the first turn, so the tool
// must always ask the server to run it.
func TestStartTaskPostsUnattended(t *testing.T) {
	rec, c := newRecordingServer(t)
	if _, err := callTool(t, c, "podiom_start_task", map[string]any{"id": "t1"}); err != nil {
		t.Fatalf("start task: %v", err)
	}
	if rec.method != http.MethodPost || rec.path != "/api/tasks/t1/start" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
	var unattended bool
	if err := json.Unmarshal(rec.body["unattended"], &unattended); err != nil {
		t.Fatalf("decode unattended from %v: %v", rec.body, err)
	}
	if !unattended {
		t.Fatalf("body should set unattended=true: %v", rec.body)
	}
}

func TestManageClientSendsGatewayToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	if err := os.WriteFile(config.NewPaths(home).GatewayToken, []byte("manage-token\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get(gateway.Header)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := newManageClient(strings.TrimPrefix(srv.URL, "http://"))
	if _, err := c.get(context.Background(), "/api/config"); err != nil {
		t.Fatalf("manage client get: %v", err)
	}
	if gotToken != "manage-token" {
		t.Fatalf("gateway token header = %q, want manage-token", gotToken)
	}
}

func TestUpdateTaskPatchOmitsAbsentKeys(t *testing.T) {
	rec, c := newRecordingServer(t)
	if _, err := callTool(t, c, "podiom_update_task", map[string]any{
		"id": "t1", "status": "done",
	}); err != nil {
		t.Fatalf("update task: %v", err)
	}
	if rec.method != http.MethodPatch || rec.path != "/api/tasks/t1" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
	if len(rec.body) != 1 {
		t.Fatalf("expected only status in body, got %v", rec.body)
	}
	if _, ok := rec.body["status"]; !ok {
		t.Fatalf("body missing status: %v", rec.body)
	}
}

func TestDeleteTaskRequiresConfirmBeforeHTTP(t *testing.T) {
	rec, c := newRecordingServer(t)
	_, err := callTool(t, c, "podiom_delete_task", map[string]any{"id": "t1"})
	if err == nil {
		t.Fatal("expected error when confirm omitted")
	}
	if rec.method != "" {
		t.Fatalf("server should not have been called, saw %s %s", rec.method, rec.path)
	}
	// With confirm=true it proceeds to DELETE.
	if _, err := callTool(t, c, "podiom_delete_task", map[string]any{"id": "t1", "confirm": true}); err != nil {
		t.Fatalf("confirmed delete: %v", err)
	}
	if rec.method != http.MethodDelete || rec.path != "/api/tasks/t1" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
}

func TestReadLogsBuildsLinesQuery(t *testing.T) {
	rec, c := newRecordingServer(t)
	if _, err := callTool(t, c, "podiom_read_logs", map[string]any{"lines": 50}); err != nil {
		t.Fatalf("read logs: %v", err)
	}
	if rec.path != "/api/logs" || rec.rawQuery != "lines=50" {
		t.Fatalf("got path=%s query=%s", rec.path, rec.rawQuery)
	}
}

func TestSearchSkillsMapsQueryParam(t *testing.T) {
	rec, c := newRecordingServer(t)
	if _, err := callTool(t, c, "podiom_search_skills", map[string]any{"query": "vim", "page": 2}); err != nil {
		t.Fatalf("search: %v", err)
	}
	if rec.path != "/api/skills/search" {
		t.Fatalf("path = %s", rec.path)
	}
	if !strings.Contains(rec.rawQuery, "q=vim") || !strings.Contains(rec.rawQuery, "page=2") {
		t.Fatalf("query = %s", rec.rawQuery)
	}
}

func TestAssignMCPServerPutsBody(t *testing.T) {
	rec, c := newRecordingServer(t)
	if _, err := callTool(t, c, "podiom_assign_mcp_server", map[string]any{
		"agent_name": "ada", "server_name": "home", "assigned": false,
	}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if rec.method != http.MethodPut || rec.path != "/api/mcp/assignments" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
	if _, ok := rec.body["assigned"]; !ok {
		t.Fatalf("body missing assigned: %v", rec.body)
	}
}

func TestPatchConfigRejectsEmpty(t *testing.T) {
	rec, c := newRecordingServer(t)
	_, err := callTool(t, c, "podiom_patch_config", map[string]any{})
	if err == nil {
		t.Fatal("expected error for empty config patch")
	}
	if rec.method != "" {
		t.Fatalf("server should not have been called")
	}
}

func TestListTasksFiltersClientSide(t *testing.T) {
	rec, c := newRecordingServer(t)
	rec.respond = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// PascalCase keys mirror the real /api/tasks contract: store.Task has
		// no JSON tags, so Go's default marshaling emits exported field names.
		_, _ = w.Write([]byte(`[
			{"ID":"a","Status":"backlog","ProjectID":"p1"},
			{"ID":"b","Status":"done","ProjectID":"p1"},
			{"ID":"c","Status":"backlog","ProjectID":"p2"}
		]`))
	}
	out, err := callTool(t, c, "podiom_list_tasks", map[string]any{"status": "backlog", "project_id": "p1"})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode filtered output: %v", err)
	}
	if len(got) != 1 || got[0]["ID"] != "a" {
		t.Fatalf("expected only task a, got %v", got)
	}
}

func TestNon2xxSurfacesErrorBody(t *testing.T) {
	rec, c := newRecordingServer(t)
	rec.respond = func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "task id is required", http.StatusBadRequest)
	}
	_, err := callTool(t, c, "podiom_get_task", map[string]any{"id": "x"})
	if err == nil || !strings.Contains(err.Error(), "task id is required") {
		t.Fatalf("expected surfaced error body, got %v", err)
	}
}
