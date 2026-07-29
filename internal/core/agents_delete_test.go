package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
)

func TestDeleteAgentArchivesSessionsAndRemovesActiveRows(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create atlas: %v", err)
	}
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "builder", Provider: config.ProviderCodex}); err != nil {
		t.Fatalf("create builder: %v", err)
	}
	if err := os.WriteFile(c.paths.ConfigYAML, []byte(`global:
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
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "atlas", Origin: store.OriginCLI})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	attachment, err := c.CreateAttachment(ctx, CreateAttachmentInput{
		SessionID: session.ID,
		Name:      "memory.png",
		MIMEType:  "image/png",
		Original:  []byte("original"),
		Visual:    []byte("visual"),
		Width:     2,
		Height:    2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.AppendUserMessage(ctx, session.ID, "remember alpha", []string{attachment.ID}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if _, err := c.store.AppendMessages(ctx, session.ID, []store.Message{{Role: store.RoleAssistant, Content: "alpha noted"}}); err != nil {
		t.Fatalf("append messages: %v", err)
	}

	result, err := c.DeleteAgent(ctx, "atlas")
	if err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	if result.ArchivedSessions != 1 || result.ArchivePath == "" {
		t.Fatalf("bad delete result: %+v", result)
	}
	if _, err := os.Stat(c.AgentPaths("atlas").Root); err != nil {
		t.Fatalf("agent directory should be preserved: %v", err)
	}
	files, err := filepath.Glob(filepath.Join(result.ArchivePath, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("archive files = %v, want one JSON file", files)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "provider_handle") {
		t.Fatalf("archive leaked provider handle:\n%s", raw)
	}
	var archived struct {
		Agent   store.Agent `json:"agent"`
		Session struct {
			ID        string `json:"id"`
			AgentName string `json:"agent_name"`
		} `json:"session"`
		Messages []store.Message `json:"messages"`
	}
	if err := json.Unmarshal(raw, &archived); err != nil {
		t.Fatalf("decode archive: %v", err)
	}
	if archived.Agent.Name != "atlas" || archived.Session.ID != session.ID || archived.Session.AgentName != "atlas" {
		t.Fatalf("bad archive payload: %+v", archived)
	}
	if len(archived.Messages) != 2 || archived.Messages[0].Content != "remember alpha" || archived.Messages[1].Content != "alpha noted" {
		t.Fatalf("bad archived messages: %+v", archived.Messages)
	}
	archivedVisual := filepath.Join(result.ArchivePath, "attachments", session.ID, attachment.ID, "visual.jpg")
	if raw, err := os.ReadFile(archivedVisual); err != nil || string(raw) != "visual" {
		t.Fatalf("archived attachment visual = %q, err=%v", raw, err)
	}
	if _, err := c.store.GetAgent(ctx, "atlas"); err == nil {
		t.Fatal("expected atlas agent row to be deleted")
	}
	if _, err := c.store.GetAgent(ctx, "builder"); err != nil {
		t.Fatalf("builder should remain: %v", err)
	}
	sessions, err := c.store.ListSessionsByAgent(ctx, "atlas")
	if err != nil {
		t.Fatalf("list sessions by agent: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions should be removed from active DB: %+v", sessions)
	}
	messages, err := c.store.ListMessages(ctx, session.ID)
	if err != nil {
		t.Fatalf("list messages after delete: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("messages should be cascade-deleted from active DB: %+v", messages)
	}
	cfg, err := config.Load(c.paths.ConfigYAML)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.Agents) != 1 || cfg.Agents[0].Name != "builder" {
		t.Fatalf("config agents = %+v", cfg.Agents)
	}
}

func TestDeleteAgentUnassignsRoadmapTasks(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create atlas: %v", err)
	}
	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "builder", Provider: config.ProviderCodex}); err != nil {
		t.Fatalf("create builder: %v", err)
	}
	atlasTask, err := c.CreateTask(ctx, store.Task{Title: "atlas work", AssignedAgent: "atlas"})
	if err != nil {
		t.Fatalf("create atlas task: %v", err)
	}
	builderTask, err := c.CreateTask(ctx, store.Task{Title: "builder work", AssignedAgent: "builder"})
	if err != nil {
		t.Fatalf("create builder task: %v", err)
	}

	if _, err := c.DeleteAgent(ctx, "atlas"); err != nil {
		t.Fatalf("delete agent: %v", err)
	}

	gotAtlas, err := c.GetTask(ctx, atlasTask.ID)
	if err != nil {
		t.Fatalf("get atlas task: %v", err)
	}
	if gotAtlas.AssignedAgent != "" {
		t.Fatalf("deleted agent assignment should be cleared, got %q", gotAtlas.AssignedAgent)
	}
	gotBuilder, err := c.GetTask(ctx, builderTask.ID)
	if err != nil {
		t.Fatalf("get builder task: %v", err)
	}
	if gotBuilder.AssignedAgent != "builder" {
		t.Fatalf("other task assignment should remain, got %q", gotBuilder.AssignedAgent)
	}
}

func TestDeleteAgentArchiveFailureLeavesDatabaseUnchanged(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{AgentName: "atlas", Origin: store.OriginCLI})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := c.store.AppendMessages(ctx, session.ID, []store.Message{{Role: store.RoleUser, Content: "keep me"}}); err != nil {
		t.Fatalf("append message: %v", err)
	}
	if err := os.WriteFile(filepath.Join(c.AgentPaths("atlas").Workspace, "session-archive"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("block archive dir: %v", err)
	}

	if _, err := c.DeleteAgent(ctx, "atlas"); err == nil {
		t.Fatal("expected archive failure")
	}
	if _, err := c.store.GetAgent(ctx, "atlas"); err != nil {
		t.Fatalf("agent should remain after archive failure: %v", err)
	}
	sessions, err := c.store.ListSessionsByAgent(ctx, "atlas")
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != session.ID {
		t.Fatalf("session should remain after archive failure: %+v", sessions)
	}
	messages, err := c.store.ListMessages(ctx, session.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 1 || messages[0].Content != "keep me" {
		t.Fatalf("message should remain after archive failure: %+v", messages)
	}
}
