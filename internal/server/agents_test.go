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

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/store"
)

func TestDeleteAgentRejectsConfirmationMismatch(t *testing.T) {
	ctx := context.Background()
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/agents/atlas", bytes.NewBufferString(`{"confirmation":"wrong"}`))
	rr := httptest.NewRecorder()
	srv.handleAgent(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if _, err := srv.core.GetAgent(ctx, "atlas"); err != nil {
		t.Fatalf("agent should remain after mismatch: %v", err)
	}
}

func TestDeleteAgentRemovesDatabaseRowAndConfigEntry(t *testing.T) {
	ctx := context.Background()
	paths, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create atlas: %v", err)
	}
	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{Name: "builder", Provider: config.ProviderCodex}); err != nil {
		t.Fatalf("create builder: %v", err)
	}
	writeConfig(t, paths.ConfigYAML, `global:
  provider: claude
  permission_mode: approve
agents:
  - name: atlas
    provider: claude
  - name: builder
    provider: codex
server:
  bind: 127.0.0.1
  port: 8787
`)

	req := httptest.NewRequest(http.MethodDelete, "/api/agents/atlas", bytes.NewBufferString(`{"confirmation":"atlas"}`))
	rr := httptest.NewRecorder()
	srv.handleAgent(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if _, err := srv.core.GetAgent(ctx, "atlas"); err == nil {
		t.Fatal("expected atlas to be deleted from store")
	}
	if _, err := srv.core.GetAgent(ctx, "builder"); err != nil {
		t.Fatalf("builder should remain in store: %v", err)
	}
	cfg, err := config.Load(paths.ConfigYAML)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.Agents) != 1 || cfg.Agents[0].Name != "builder" {
		t.Fatalf("config agents = %+v", cfg.Agents)
	}
}

func TestDeleteAgentSucceedsWhenConfigEntryIsAbsent(t *testing.T) {
	ctx := context.Background()
	paths, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	before, err := os.ReadFile(paths.ConfigYAML)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/agents/atlas", bytes.NewBufferString(`{"confirmation":"atlas"}`))
	rr := httptest.NewRecorder()
	srv.handleAgent(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	after, err := os.ReadFile(paths.ConfigYAML)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("config without matching entry should not be rewritten")
	}
}

func TestDeleteAgentArchivesSessionsBeforeDeletingAgent(t *testing.T) {
	ctx := context.Background()
	paths, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	agent, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := srv.core.CreateSession(ctx, core.CreateSessionRequest{AgentName: agent.Name, Origin: store.OriginCLI}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	writeConfig(t, paths.ConfigYAML, `global:
  provider: claude
  permission_mode: approve
agents:
  - name: atlas
    provider: claude
server:
  bind: 127.0.0.1
  port: 8787
`)

	req := httptest.NewRequest(http.MethodDelete, "/api/agents/atlas", bytes.NewBufferString(`{"confirmation":"atlas"}`))
	rr := httptest.NewRecorder()
	srv.handleAgent(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if _, err := srv.core.GetAgent(ctx, "atlas"); err == nil {
		t.Fatal("expected agent to be deleted after session archive")
	}
	archiveRoot := filepath.Join(paths.AgentsDir, "atlas", "workspace", "session-archive")
	matches, err := filepath.Glob(filepath.Join(archiveRoot, "*", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("archive files = %v, want one JSON file", matches)
	}
	cfg, err := config.Load(paths.ConfigYAML)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.Agents) != 0 {
		t.Fatalf("config should remove deleted agent, got %+v", cfg.Agents)
	}
}

func TestProfilesCreatePersistsToConfigAndRefreshesCore(t *testing.T) {
	paths, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	req := httptest.NewRequest(http.MethodPost, "/api/profiles", bytes.NewBufferString(`{"name":"work","provider":"claude"}`))
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	srv.handleProfiles(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	cfg, err := config.Load(paths.ConfigYAML)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.Profiles) != 1 || cfg.Profiles[0].Name != "work" || cfg.Profiles[0].Provider != config.ProviderClaude {
		t.Fatalf("profiles = %+v", cfg.Profiles)
	}
	if len(srv.core.ListProfiles()) != 1 {
		t.Fatalf("core profiles were not refreshed")
	}
	if _, err := os.Stat(cfg.Profiles[0].ConfigDir); err != nil {
		t.Fatalf("profile directory not created: %v", err)
	}
}

func TestAgentGenerateDraftDoesNotSaveByDefault(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	paths := config.NewPaths(home)
	if _, err := config.Scaffold(paths); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if err := os.WriteFile(paths.BaseAgents, []byte("base layer\n"), 0o644); err != nil {
		t.Fatalf("write base agents: %v", err)
	}
	db, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	fake := adapter.NewFake()
	fake.Responses = []string{validGeneratedSoul("atlas", "Generated draft.")}
	coreSvc, err := core.New(core.Options{Paths: paths, Store: db, Adapter: fake, DisableBackgroundWork: true})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	srv := New(Options{Bind: "127.0.0.1", Port: 0, Core: coreSvc, Paths: paths})
	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	before, err := srv.core.ReadAgentSoul("atlas")
	if err != nil {
		t.Fatalf("read soul: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/agents/atlas/generate", bytes.NewBufferString(`{"notes":"make direct"}`))
	rr := httptest.NewRecorder()
	srv.handleAgent(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var result core.SoulGenerateResult
	if err := json.NewDecoder(rr.Body).Decode(&result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Agent != "atlas" || result.Saved {
		t.Fatalf("bad result: %+v", result)
	}
	if !bytes.Contains([]byte(result.Soul), []byte("Generated draft")) {
		t.Fatalf("missing generated soul: %+v", result)
	}
	after, err := srv.core.ReadAgentSoul("atlas")
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if after != before {
		t.Fatalf("draft-only generate should not save\nbefore=%s\nafter=%s", before, after)
	}
}

func TestAgentGenerateCanSave(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	paths := config.NewPaths(home)
	if _, err := config.Scaffold(paths); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if err := os.WriteFile(paths.BaseAgents, []byte("base layer\n"), 0o644); err != nil {
		t.Fatalf("write base agents: %v", err)
	}
	db, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	fake := adapter.NewFake()
	fake.Responses = []string{"```markdown\n" + validGeneratedSoul("atlas", "Saved draft.") + "\n```"}
	coreSvc, err := core.New(core.Options{Paths: paths, Store: db, Adapter: fake, DisableBackgroundWork: true})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	srv := New(Options{Bind: "127.0.0.1", Port: 0, Core: coreSvc, Paths: paths})
	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/agents/atlas/generate", bytes.NewBufferString(`{"save":true}`))
	rr := httptest.NewRecorder()
	srv.handleAgent(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var result core.SoulGenerateResult
	if err := json.NewDecoder(rr.Body).Decode(&result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if !result.Saved {
		t.Fatalf("expected saved result: %+v", result)
	}
	soul, err := srv.core.ReadAgentSoul("atlas")
	if err != nil {
		t.Fatalf("read soul: %v", err)
	}
	if soul != result.Soul || !bytes.Contains([]byte(soul), []byte("Saved draft")) {
		t.Fatalf("saved soul mismatch\nresult=%s\nfile=%s", result.Soul, soul)
	}
}

func validGeneratedSoul(name, body string) string {
	return `# Identity

Name: ` + name + `

` + body + `

## Purpose

- Persist the agent's purpose.

## Worldview

- Specific behavior matters.

## Working style

- Collaborate clearly.

## Voice

- Direct and warm.

## Strengths

- Careful implementation.

## Boundaries

- Ask before risky work.

## Calibration notes

- The agent feels specific.
`
}

func TestProfilesGetReflectsConfigYAMLEdits(t *testing.T) {
	paths, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	writeConfig(t, paths.ConfigYAML, `global:
  provider: claude
  permission_mode: approve
profiles:
  - name: codex-main
    provider: codex
    home_dir: /tmp/codex-main
server:
  bind: 127.0.0.1
  port: 8787
`)

	req := httptest.NewRequest(http.MethodGet, "/api/profiles", nil)
	rr := httptest.NewRecorder()
	srv.handleProfiles(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	profiles := srv.core.ListProfileDetails()
	if len(profiles) != 1 || profiles[0].Name != "codex-main" {
		t.Fatalf("profiles = %+v", profiles)
	}
}

func TestProfilesDeleteRejectsReferencedProfile(t *testing.T) {
	ctx := context.Background()
	paths, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	writeConfig(t, paths.ConfigYAML, `global:
  provider: claude
  permission_mode: approve
profiles:
  - name: work
    provider: claude
    config_dir: /tmp/claude-work
agents:
  - name: atlas
    provider: claude
    profile: work
server:
  bind: 127.0.0.1
  port: 8787
`)
	if err := srv.refreshProfilesFromConfig(); err != nil {
		t.Fatalf("refresh profiles: %v", err)
	}
	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude, Profile: "work"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/profiles/work", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	srv.handleProfile(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	cfg, err := config.Load(paths.ConfigYAML)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.Profiles) != 1 || cfg.Profiles[0].Name != "work" {
		t.Fatalf("profile should remain, got %+v", cfg.Profiles)
	}
}

func newAgentAPITestServer(t *testing.T) (config.Paths, *Server, func()) {
	t.Helper()
	home := t.TempDir()
	paths := config.NewPaths(home)
	if _, err := config.Scaffold(paths); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if err := os.WriteFile(paths.BaseAgents, []byte("base layer\n"), 0o644); err != nil {
		t.Fatalf("write base agents: %v", err)
	}
	db, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	coreSvc, err := core.New(core.Options{Paths: paths, Store: db, Adapter: adapter.NewFake()})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	srv := New(Options{Bind: "127.0.0.1", Port: 0, Core: coreSvc, Paths: paths})
	return paths, srv, func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	}
}

func writeConfig(t *testing.T, path, raw string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
