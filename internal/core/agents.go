package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/Podiom/Podiom/internal/config"
	podiomlog "github.com/Podiom/Podiom/internal/logging"
	"github.com/Podiom/Podiom/internal/skills"
	"github.com/Podiom/Podiom/internal/store"
)

// CreateAgentRequest describes a new agent and its defaults. Empty provider,
// effort, and permission fields inherit from the global config defaults.
type CreateAgentRequest struct {
	Name           string
	Provider       config.Provider
	Profile        string
	Model          string
	Effort         string
	PermissionMode config.PermissionMode
	Fallback       []string
	MCPServers     []string
	MCPConfig      string
}

// DeleteAgentResult describes any on-disk archive created while deleting an
// agent. ArchivePath is empty when the agent had no sessions to archive.
type DeleteAgentResult struct {
	ArchivePath      string `json:"archive_path,omitempty"`
	ArchivedSessions int    `json:"archived_sessions"`
}

// CreateAgent scaffolds the agent directory and stores its durable definition.
func (c *Core) CreateAgent(ctx context.Context, req CreateAgentRequest) (store.Agent, error) {
	if err := validateAgentName(req.Name); err != nil {
		return store.Agent{}, err
	}
	agent := store.Agent{
		Name:           req.Name,
		Provider:       req.Provider,
		Profile:        req.Profile,
		Model:          req.Model,
		Effort:         req.Effort,
		PermissionMode: req.PermissionMode,
		Fallback:       append([]string(nil), req.Fallback...),
		MCPServers:     append([]string(nil), req.MCPServers...),
		MCPConfig:      req.MCPConfig,
	}
	c.applyAgentDefaults(&agent)
	if err := c.validateAgentTargets(agent); err != nil {
		return store.Agent{}, err
	}
	paths := c.AgentPaths(agent.Name)
	if err := scaffoldAgent(paths, agent.Name); err != nil {
		return store.Agent{}, err
	}
	// Expose the skills union to this agent's Claude turns via a workspace
	// .claude/skills link (S6/S10). Non-fatal: a missing link must not block
	// agent creation (e.g. symlinks unavailable on Windows without perms).
	if err := skills.EnsureClaudeWorkspaceLink(paths.Workspace); err != nil {
		c.log.Warn("could not link skills into agent workspace", "agent", agent.Name, "error", err)
	}
	created, err := c.store.CreateAgent(ctx, agent)
	if err != nil {
		return store.Agent{}, err
	}
	c.log.Info("agent created",
		"event", "agent",
		"agent", created.Name,
		"provider", string(created.Provider),
		"profile", created.Profile,
		"model_set", created.Model != "",
		"effort", created.Effort,
		"permission", string(created.PermissionMode),
		"fallback_count", len(created.Fallback),
		"mcp_servers", len(created.MCPServers),
	)
	return created, nil
}

// GetAgent fetches one agent.
func (c *Core) GetAgent(ctx context.Context, name string) (store.Agent, error) {
	return c.store.GetAgent(ctx, name)
}

// ListAgents returns all agents ordered by name.
func (c *Core) ListAgents(ctx context.Context) ([]store.Agent, error) {
	return c.store.ListAgents(ctx)
}

// UpdateAgent updates an agent's durable defaults. It also ensures the agent's
// expected directory layout exists.
func (c *Core) UpdateAgent(ctx context.Context, agent store.Agent) (store.Agent, error) {
	if err := validateAgentName(agent.Name); err != nil {
		return store.Agent{}, err
	}
	before, _ := c.store.GetAgent(ctx, agent.Name)
	c.applyAgentDefaults(&agent)
	if err := c.validateAgentTargets(agent); err != nil {
		return store.Agent{}, err
	}
	if err := scaffoldAgent(c.AgentPaths(agent.Name), agent.Name); err != nil {
		return store.Agent{}, err
	}
	updated, err := c.store.UpdateAgent(ctx, agent)
	if err != nil {
		return store.Agent{}, err
	}
	c.log.Info("agent updated",
		"event", "agent",
		"agent", updated.Name,
		"provider", string(updated.Provider),
		"profile", updated.Profile,
		"changed", podiomlog.ChangedFields(agentLogFields(before), agentLogFields(updated)),
		"fallback_count", len(updated.Fallback),
		"mcp_servers", len(updated.MCPServers),
	)
	return updated, nil
}

// validateAgentTargets checks an agent's provider, profile, and fallback chain
// against the configured profiles — the same referential rules config.Validate
// enforces at load time, applied here so create/update over the API/UI can't
// persist an unresolvable target. The agent's own profile must match its
// provider; fallback entries may be a profile name, a bare provider token, or
// "default" (and a profile entry may target a different provider).
func (c *Core) validateAgentTargets(agent store.Agent) error {
	if agent.Provider != config.ProviderClaude && agent.Provider != config.ProviderCodex {
		return fmt.Errorf("unknown provider %q (want claude|codex)", agent.Provider)
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if agent.Profile != "" {
		p, ok := c.profiles[agent.Profile]
		if !ok {
			return fmt.Errorf("unknown profile %q", agent.Profile)
		}
		if p.Provider != agent.Provider {
			return fmt.Errorf("profile %q belongs to provider %q, not %q", agent.Profile, p.Provider, agent.Provider)
		}
	}
	for _, entry := range agent.Fallback {
		if entry == "" {
			return fmt.Errorf("fallback entry is required")
		}
		if entry == "default" || entry == string(config.ProviderClaude) || entry == string(config.ProviderCodex) {
			continue
		}
		if _, ok := c.profiles[entry]; !ok {
			return fmt.Errorf("unknown fallback profile %q", entry)
		}
	}
	seenMCP := map[string]bool{}
	for _, server := range agent.MCPServers {
		server = strings.TrimSpace(server)
		if server == "" {
			return fmt.Errorf("mcp server name is required")
		}
		if seenMCP[server] {
			return fmt.Errorf("duplicate mcp server %q", server)
		}
		seenMCP[server] = true
	}
	return nil
}

// DeleteAgent removes the durable agent definition. Files on disk are left in
// place so user-authored identity/instructions are never deleted implicitly.
func (c *Core) DeleteAgent(ctx context.Context, name string) (DeleteAgentResult, error) {
	if err := validateAgentName(name); err != nil {
		return DeleteAgentResult{}, err
	}
	agent, err := c.store.GetAgent(ctx, name)
	if err != nil {
		return DeleteAgentResult{}, err
	}
	sessions, err := c.store.ListSessionsByAgent(ctx, name)
	if err != nil {
		return DeleteAgentResult{}, err
	}
	archivePath, err := c.archiveAgentSessions(ctx, agent, sessions, time.Now().UTC())
	if err != nil {
		return DeleteAgentResult{}, err
	}
	if err := c.store.DeleteSessionsByAgent(ctx, name); err != nil {
		return DeleteAgentResult{}, err
	}
	if err := c.store.UnassignTasksByAgent(ctx, name); err != nil {
		return DeleteAgentResult{}, err
	}
	if err := c.store.DeleteAgent(ctx, name); err != nil {
		return DeleteAgentResult{}, err
	}
	if err := config.RemoveAgent(c.paths.ConfigYAML, name); err != nil {
		return DeleteAgentResult{}, err
	}
	c.log.Info("agent deleted",
		"event", "agent",
		"agent", name,
		"provider", string(agent.Provider),
		"profile", agent.Profile,
		"archived_sessions", len(sessions),
		"archive_path_set", archivePath != "",
	)
	return DeleteAgentResult{ArchivePath: archivePath, ArchivedSessions: len(sessions)}, nil
}

func agentLogFields(a store.Agent) map[string]string {
	return map[string]string{
		"provider":       string(a.Provider),
		"profile":        a.Profile,
		"model":          a.Model,
		"effort":         a.Effort,
		"permission":     string(a.PermissionMode),
		"fallback_count": fmt.Sprintf("%d", len(a.Fallback)),
		"mcp_count":      fmt.Sprintf("%d", len(a.MCPServers)),
	}
}

type sessionArchive struct {
	ExportedAt string          `json:"exported_at"`
	Agent      store.Agent     `json:"agent"`
	Session    archivedSession `json:"session"`
	Messages   []store.Message `json:"messages"`
}

type archivedSession struct {
	ID             string                `json:"id"`
	AgentName      string                `json:"agent_name"`
	Name           string                `json:"name"`
	Description    string                `json:"description"`
	AutoNamed      bool                  `json:"auto_named"`
	Provider       config.Provider       `json:"provider"`
	Profile        string                `json:"profile"`
	Model          string                `json:"model"`
	Effort         string                `json:"effort"`
	PermissionMode config.PermissionMode `json:"permission_mode"`
	Origin         store.SessionOrigin   `json:"origin"`
	ScheduleID     string                `json:"schedule_id,omitempty"`
	RunID          string                `json:"run_id,omitempty"`
	TaskID         string                `json:"task_id,omitempty"`
	RollingSummary string                `json:"rolling_summary,omitempty"`
	CreatedAt      string                `json:"created_at"`
	UpdatedAt      string                `json:"updated_at"`
}

func (c *Core) archiveAgentSessions(ctx context.Context, agent store.Agent, sessions []store.Session, deletedAt time.Time) (string, error) {
	if len(sessions) == 0 {
		return "", nil
	}
	archiveRoot := filepath.Join(c.AgentPaths(agent.Name).Workspace, "session-archive")
	dirName := deletedAt.Format("20060102T150405.000000000Z")
	tmpDir := filepath.Join(archiveRoot, ".tmp-"+dirName)
	finalDir := filepath.Join(archiveRoot, dirName)

	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return "", fmt.Errorf("create session archive dir: %w", err)
	}
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	exportedAt := deletedAt.Format(time.RFC3339Nano)
	for _, sess := range sessions {
		messages, err := c.store.ListMessages(ctx, sess.ID)
		if err != nil {
			return "", err
		}
		payload := sessionArchive{
			ExportedAt: exportedAt,
			Agent:      agent,
			Session:    archiveSession(sess),
			Messages:   messages,
		}
		raw, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return "", fmt.Errorf("encode session archive %q: %w", sess.ID, err)
		}
		raw = append(raw, '\n')
		if err := os.WriteFile(filepath.Join(tmpDir, archiveSessionFilename(sess)), raw, 0o600); err != nil {
			return "", fmt.Errorf("write session archive %q: %w", sess.ID, err)
		}
	}
	if err := os.Rename(tmpDir, finalDir); err != nil {
		return "", fmt.Errorf("finalize session archive: %w", err)
	}
	removeTmp = false
	return finalDir, nil
}

func archiveSession(sess store.Session) archivedSession {
	return archivedSession{
		ID:             sess.ID,
		AgentName:      sess.AgentName,
		Name:           sess.Name,
		Description:    sess.Description,
		AutoNamed:      sess.AutoNamed,
		Provider:       sess.Provider,
		Profile:        sess.Profile,
		Model:          sess.Model,
		Effort:         sess.Effort,
		PermissionMode: sess.PermissionMode,
		Origin:         sess.Origin,
		ScheduleID:     sess.ScheduleID,
		RunID:          sess.RunID,
		TaskID:         sess.TaskID,
		RollingSummary: sess.RollingSummary,
		CreatedAt:      sess.CreatedAt,
		UpdatedAt:      sess.UpdatedAt,
	}
}

func archiveSessionFilename(sess store.Session) string {
	stamp := firstNonEmpty(sess.CreatedAt, sess.UpdatedAt, "unknown-time")
	return sanitizeArchiveName(stamp) + "_" + sanitizeArchiveName(sess.ID) + ".json"
}

func sanitizeArchiveName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lastDash := false
	for _, r := range s {
		ok := r <= unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unnamed"
	}
	return out
}

// ReadAgentSoul returns the contents of an agent's SOUL.md, or empty string if
// the file does not exist yet.
func (c *Core) ReadAgentSoul(name string) (string, error) {
	if err := validateAgentName(name); err != nil {
		return "", err
	}
	data, err := os.ReadFile(c.AgentPaths(name).Soul)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read SOUL.md for agent %q: %w", name, err)
	}
	return string(data), nil
}

// WriteAgentSoul overwrites an agent's SOUL.md with the given content, creating
// the agent directory if needed.
func (c *Core) WriteAgentSoul(name, content string) error {
	if err := validateAgentName(name); err != nil {
		return err
	}
	paths := c.AgentPaths(name)
	if err := os.MkdirAll(paths.Root, 0o755); err != nil {
		return fmt.Errorf("create agent dir: %w", err)
	}
	if err := os.WriteFile(paths.Soul, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write SOUL.md for agent %q: %w", name, err)
	}
	return nil
}

// ReadAgentMemory returns the contents of an agent's MEMORY.md, or empty string
// if the file does not exist yet. Memory content is sensitive (MEM22): callers
// must never log it in the clear.
func (c *Core) ReadAgentMemory(name string) (string, error) {
	if err := validateAgentName(name); err != nil {
		return "", err
	}
	data, err := os.ReadFile(c.AgentPaths(name).Memory)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read MEMORY.md for agent %q: %w", name, err)
	}
	return string(data), nil
}

// WriteAgentMemory overwrites an agent's MEMORY.md with the given content. The
// write is atomic (temp file + rename) because the dream job may write this same
// file concurrently with a user edit.
func (c *Core) WriteAgentMemory(name, content string) error {
	if err := validateAgentName(name); err != nil {
		return err
	}
	paths := c.AgentPaths(name)
	if err := os.MkdirAll(paths.Root, 0o755); err != nil {
		return fmt.Errorf("create agent dir: %w", err)
	}
	return writeFileAtomic(paths.Memory, []byte(content), 0o644)
}

// ClearAgentMemory empties an agent's MEMORY.md. The user owns the memory and may
// wipe it at any time (MEM15/MEM17); the dream treats the resulting empty file as
// its authoritative starting point.
func (c *Core) ClearAgentMemory(name string) error {
	return c.WriteAgentMemory(name, "")
}

// ReadAgentAvatar returns the bytes of an agent's uploaded profile picture, or
// os.ErrNotExist if the agent has no picture. Callers should map the not-exist
// case to a 404.
func (c *Core) ReadAgentAvatar(name string) ([]byte, error) {
	if err := validateAgentName(name); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(c.AgentPaths(name).Avatar)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("read avatar for agent %q: %w", name, err)
	}
	return data, nil
}

// SetAgentAvatar writes (normalized PNG) bytes as the agent's profile picture
// and stamps the version so clients cache-bust. The write is atomic.
func (c *Core) SetAgentAvatar(ctx context.Context, name string, data []byte) error {
	if err := validateAgentName(name); err != nil {
		return err
	}
	paths := c.AgentPaths(name)
	if err := os.MkdirAll(paths.Root, 0o755); err != nil {
		return fmt.Errorf("create agent dir: %w", err)
	}
	if err := writeFileAtomic(paths.Avatar, data, 0o644); err != nil {
		return err
	}
	return c.store.SetAgentAvatarUpdatedAt(ctx, name, time.Now().UTC().Format(time.RFC3339Nano))
}

// DeleteAgentAvatar removes an agent's uploaded profile picture and clears the
// version stamp, reverting the UI to the derived gradient monogram. Removing a
// picture that does not exist is not an error.
func (c *Core) DeleteAgentAvatar(ctx context.Context, name string) error {
	if err := validateAgentName(name); err != nil {
		return err
	}
	if err := os.Remove(c.AgentPaths(name).Avatar); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove avatar for agent %q: %w", name, err)
	}
	return c.store.SetAgentAvatarUpdatedAt(ctx, name, "")
}

// writeFileAtomic writes data to a temp file in the same directory and renames it
// into place, so a concurrent reader sees either the old or the new file whole.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp for %s: %w", path, err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("chmod temp for %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp into %s: %w", path, err)
	}
	return nil
}

func (c *Core) applyAgentDefaults(agent *store.Agent) {
	g := c.GetGlobal()
	if agent.Provider == "" {
		agent.Provider = g.Provider
	}
	// The default profile is tied to the default provider, so only inherit it
	// when this agent runs on that same provider.
	if agent.Profile == "" && agent.Provider == g.Provider {
		agent.Profile = g.Profile
	}
	if agent.Model == "" {
		agent.Model = g.Model
	}
	if agent.Effort == "" {
		agent.Effort = g.Effort
	}
	if agent.PermissionMode == "" {
		agent.PermissionMode = g.PermissionMode
	}
	if len(agent.Fallback) == 0 {
		agent.Fallback = g.Fallback
	}
}

func scaffoldAgent(paths AgentPaths, name string) error {
	for _, dir := range []string{paths.Root, paths.Workspace} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
	}
	template := strings.ReplaceAll(string(config.AgentSoulTemplate()), "{{agent_name}}", name)
	if _, err := writeIfAbsent(paths.Soul, []byte(template), 0o644); err != nil {
		return err
	}
	// MEMORY.md is created empty beside SOUL.md (MEM2). An empty memory is valid
	// and contributes nothing to context until the first dream. Because scaffold
	// also runs on agent update and daemon start, agents created before this
	// feature are backfilled here.
	if _, err := writeIfAbsent(paths.Memory, []byte{}, 0o644); err != nil {
		return err
	}
	return nil
}

func writeIfAbsent(path string, data []byte, perm os.FileMode) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create parent of %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, perm); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}
