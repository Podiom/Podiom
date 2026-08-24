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
	// session 3 + tasks 6 + projects 5 + schedules 6 + skills 4 + mcp 5 + goals 10 + agents 6 + credentials 2 + toolset 3 + platform 4.
	if len(tools) != 54 {
		t.Fatalf("expected 54 tools, got %d", len(tools))
	}
	seen := map[string]bool{}
	destructive := map[string]bool{
		"podiom_delete_task": true, "podiom_delete_project": true,
		"podiom_delete_schedule": true, "podiom_uninstall_skill": true,
		"podiom_remove_mcp_server": true, "podiom_remove_tool": true,
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

func TestAttachWorkspaceFileStampsSessionAndForwardsSchema(t *testing.T) {
	rec, c := newRecordingServer(t)
	tool, ok := toolByName(c, "podiom_attach_workspace_file")
	if !ok {
		t.Fatal("attach workspace file tool not found")
	}
	required, _ := tool.InputSchema["required"].([]string)
	if !contains(required, "path") {
		t.Fatalf("required fields = %v, want path", required)
	}
	properties, _ := tool.InputSchema["properties"].(map[string]any)
	if _, ok := properties["session_id"]; ok {
		t.Fatal("session_id must not be model-controlled")
	}

	if _, err := callTool(t, c, "podiom_attach_workspace_file", map[string]any{
		"path": "copy/reddit.md", "label": "Reddit post", "session_id": "spoofed",
	}); err != nil {
		t.Fatalf("call attach tool: %v", err)
	}
	if rec.method != http.MethodPost || rec.path != "/api/workspace-files" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
	var sessionID string
	if err := json.Unmarshal(rec.body["session_id"], &sessionID); err != nil {
		t.Fatal(err)
	}
	if sessionID != "sess-1" {
		t.Fatalf("forwarded session_id = %q, want stamped sess-1", sessionID)
	}
	if _, ok := rec.body["path"]; !ok {
		t.Fatalf("body missing path: %v", rec.body)
	}
}

func TestUpdateSessionProjectUsesInjectedSessionAndForwardsEmptyProject(t *testing.T) {
	rec, c := newRecordingServer(t)
	tool, ok := toolByName(c, "podiom_update_session_project")
	if !ok {
		t.Fatal("update session project tool not found")
	}
	properties, _ := tool.InputSchema["properties"].(map[string]any)
	if _, ok := properties["session_id"]; ok {
		t.Fatal("session_id must not be model-controlled")
	}
	if _, err := callTool(t, c, "podiom_update_session_project", map[string]any{"project_id": ""}); err != nil {
		t.Fatalf("clear session project: %v", err)
	}
	if rec.method != http.MethodPatch || rec.path != "/api/session-context/sess-1" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
	if got, ok := rec.body["project_id"]; !ok || string(got) != `""` {
		t.Fatalf("project_id body = %s, present %v", got, ok)
	}
	if _, err := callTool(t, c, "podiom_update_session_project", map[string]any{}); err == nil {
		t.Fatal("missing project_id should fail before HTTP")
	}
}

func TestUserVisibleProseToolsPointToWorkspaceFileAttachments(t *testing.T) {
	c := newManageClient("127.0.0.1:8787")
	for _, name := range []string{
		"podiom_create_task", "podiom_update_task", "podiom_create_project", "podiom_update_project",
		"podiom_create_schedule", "podiom_update_schedule", "podiom_update_goal", "podiom_record_goal_progress",
		"podiom_propose_goal_completion", "podiom_request_access", "podiom_ask_user", "podiom_request_user_action",
	} {
		tool, ok := toolByName(c, name)
		if !ok {
			t.Fatalf("tool %q not found", name)
		}
		if !strings.Contains(tool.Description, "podiom_attach_workspace_file") || !strings.Contains(tool.Description, "never refer the user to a local path") {
			t.Errorf("tool %q is missing workspace file guidance: %s", name, tool.Description)
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

// TestStoreCredentialStampsAuthor pins the provenance: the agent and session
// come from the helper's own launch flags, not from arguments the model chose,
// so a stored secret can never be attributed to someone else.
func TestStoreCredentialStampsAuthor(t *testing.T) {
	rec, c := newRecordingServer(t)
	if _, err := callTool(t, c, "podiom_store_credential", map[string]any{
		"name": "STRIPE_KEY", "value": "sk_live_x", "purpose": "billing probe",
	}); err != nil {
		t.Fatalf("store credential: %v", err)
	}
	if rec.method != http.MethodPost || rec.path != "/api/credentials" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
	var agent, session string
	if err := json.Unmarshal(rec.body["created_by_agent"], &agent); err != nil {
		t.Fatalf("decode created_by_agent from %v: %v", rec.body, err)
	}
	if err := json.Unmarshal(rec.body["created_by_session"], &session); err != nil {
		t.Fatalf("decode created_by_session from %v: %v", rec.body, err)
	}
	if agent != "atlas" || session != "sess-1" {
		t.Fatalf("stamped %q/%q, want atlas/sess-1", agent, session)
	}
	// overwrite is a guard, not a default: an unspecified one must not reach the
	// server as false-that-looks-deliberate or, worse, as true.
	if _, ok := rec.body["overwrite"]; ok {
		t.Fatalf("body should omit unspecified overwrite: %v", rec.body)
	}

	if _, err := callTool(t, c, "podiom_store_credential", map[string]any{"name": "STRIPE_KEY"}); err == nil {
		t.Fatal("storing without a value should be refused before the request is sent")
	}
}

// TestCredentialToolsNeverReadValues is the standing check on the shape of this
// surface: agents may see what Podiom holds and add to it, but no tool hands a
// secret back and no tool deletes one.
func TestCredentialToolsNeverReadValues(t *testing.T) {
	c := newManageClient("127.0.0.1:8787")
	list, ok := toolByName(c, "podiom_list_credentials")
	if !ok {
		t.Fatal("podiom_list_credentials missing")
	}
	if !strings.Contains(list.Description, "Never values") {
		t.Errorf("listing tool must say it never returns values: %s", list.Description)
	}
	store, ok := toolByName(c, "podiom_store_credential")
	if !ok {
		t.Fatal("podiom_store_credential missing")
	}
	if !strings.Contains(store.Description, "overwrite=true") {
		t.Errorf("store tool must document the overwrite guard: %s", store.Description)
	}
	for _, tl := range manageTools(c, "sess-1", "atlas") {
		if strings.Contains(tl.Name, "credential") && (strings.Contains(tl.Name, "delete") || strings.Contains(tl.Name, "remove")) {
			t.Errorf("deleting a credential must stay human-only, found tool %q", tl.Name)
		}
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

// TestCreateToolsStampCreator pins that the tools which create durable work
// attribute it to the calling session, using this process's own --session/--agent
// flags rather than anything the model passed. Update tools must not stamp:
// authorship is fixed at creation.
func TestCreateToolsStampCreator(t *testing.T) {
	rec, c := newRecordingServer(t)

	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"podiom_create_task", map[string]any{"title": "Benchmark the candidates"}},
		{"podiom_create_schedule", map[string]any{"name": "nightly", "agent": "jared", "body": "Run the audit.", "cron": "0 1 * * *"}},
	} {
		if _, err := callTool(t, c, tc.tool, tc.args); err != nil {
			t.Fatalf("%s: %v", tc.tool, err)
		}
		if got := string(rec.body["created_by_session"]); got != `"sess-1"` {
			t.Errorf("%s created_by_session = %s, want \"sess-1\"", tc.tool, got)
		}
		if got := string(rec.body["created_by_agent"]); got != `"atlas"` {
			t.Errorf("%s created_by_agent = %s, want \"atlas\"", tc.tool, got)
		}
	}

	if _, err := callTool(t, c, "podiom_update_task", map[string]any{"id": "t1", "title": "Renamed"}); err != nil {
		t.Fatalf("podiom_update_task: %v", err)
	}
	if _, ok := rec.body["created_by_session"]; ok {
		t.Error("podiom_update_task stamped authorship; updates must leave it alone")
	}
}

// TestStampCreatorIgnoresEmptyIdentity keeps the coverage tests (which build the
// tool set with no session) from sending empty attribution to the API.
func TestStampCreatorIgnoresEmptyIdentity(t *testing.T) {
	body := stampCreator(map[string]json.RawMessage{}, "", "")
	if len(body) != 0 {
		t.Fatalf("empty identity should stamp nothing, got %v", body)
	}
}

// TestGenerateAgentSoulRefusesExistingIdentity pins the rule that a tool may
// finish a birth but never overwrite an identity — and that it refuses before
// writing anything.
func TestGenerateAgentSoulRefusesExistingIdentity(t *testing.T) {
	rec, c := newRecordingServer(t)
	rec.respond = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// store.Agent has no JSON tags, so the soul arrives PascalCase.
		_, _ = w.Write([]byte(`{"Name":"atlas","Soul":"I am Atlas."}`))
	}

	_, err := callTool(t, c, "podiom_generate_agent_soul", map[string]any{"name": "atlas"})
	if err == nil {
		t.Fatal("expected generation to be refused for an agent that already has a soul")
	}
	if !strings.Contains(err.Error(), "already has an identity") {
		t.Errorf("unhelpful refusal: %v", err)
	}
	if rec.method != http.MethodGet {
		t.Errorf("refusal should stop after the pre-flight read, saw %s %s", rec.method, rec.path)
	}
}

// TestGenerateAgentSoulSavesForNewAgent uses the real scaffolded skeleton, not
// an empty string: creating an agent always writes that skeleton, so treating
// "non-empty" as "already has an identity" would refuse every legitimate call.
func TestGenerateAgentSoulSavesForNewAgent(t *testing.T) {
	rec, c := newRecordingServer(t)
	skeleton := strings.ReplaceAll(string(config.AgentSoulTemplate()), "{{agent_name}}", "atlas")
	rec.respond = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			body, _ := json.Marshal(map[string]string{"Name": "atlas", "Soul": skeleton})
			_, _ = w.Write(body)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}

	if _, err := callTool(t, c, "podiom_generate_agent_soul", map[string]any{"name": "atlas", "role": "researcher"}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if rec.method != http.MethodPost || rec.path != "/api/agents/atlas/generate" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
	// A draft the caller cannot persist would be useless, so the tool always saves.
	if string(rec.body["save"]) != "true" {
		t.Errorf("expected save=true, got %s", rec.body["save"])
	}
}

// TestUpdateAgentOmitsPrivilegeFields keeps permission_mode, soul, and
// mcp_servers off the tool's write path even when a model passes them.
func TestUpdateAgentOmitsPrivilegeFields(t *testing.T) {
	rec, c := newRecordingServer(t)
	if _, err := callTool(t, c, "podiom_update_agent", map[string]any{
		"name": "atlas", "model": "opus", "permission_mode": "yolo", "soul": "I am someone else.", "mcp_servers": []string{},
	}); err != nil {
		t.Fatalf("update agent: %v", err)
	}
	for _, banned := range []string{"permission_mode", "soul", "mcp_servers"} {
		if _, ok := rec.body[banned]; ok {
			t.Errorf("podiom_update_agent forwarded %q; it must not be settable here", banned)
		}
	}
	if string(rec.body["model"]) != `"opus"` {
		t.Errorf("model did not reach the server: %v", rec.body)
	}
}
