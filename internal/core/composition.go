package core

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
)

// DeliveryMode selects how composed instructions are delivered to a provider.
type DeliveryMode string

const (
	// DeliveryClaudeImport writes a generated CLAUDE.md made of @ imports.
	DeliveryClaudeImport DeliveryMode = "claude_import"
	// DeliveryCodexBundle writes a generated AGENTS.md with concatenated content.
	DeliveryCodexBundle DeliveryMode = "codex_bundle"
)

// memoryBudgetLines caps how much of MEMORY.md is injected into standing context
// (MEM5), mirroring the proven Claude subagent-memory limit. The file on disk may
// grow larger; only this many lines are composed into a turn. Keeping memory
// within the budget is a goal of dreaming — memory that isn't distilled is logs.
const memoryBudgetLines = 200

// InstructionSource is one physical source included in a composed payload.
type InstructionSource struct {
	Label string
	Path  string
	// Content carries an in-memory source that should be snapshotted into the
	// generated workspace artifact instead of read from a canonical file.
	Content []byte
	// MaxLines caps how many leading lines of this source are composed in
	// (0 = unlimited). Used to enforce MEMORY.md's injection budget.
	MaxLines int
	// Optional marks a source that may be absent or empty and is simply skipped
	// rather than treated as a composition error (e.g. MEMORY.md before an agent's
	// first dream, or for agents created before this feature shipped).
	Optional bool
}

// InstructionPayload is the provider-ready instruction artifact.
type InstructionPayload struct {
	Mode    DeliveryMode
	Path    string
	Bytes   []byte
	Sources []InstructionSource
}

// InstructionComposer composes Podiom's base instructions, optional per-agent
// instructions, SOUL.md, optional project instructions, and memory in the fixed
// order required by the spec.
type InstructionComposer interface {
	Compose(context.Context, store.Agent, DeliveryMode, string) (InstructionPayload, error)
}

// FileComposer composes instruction payloads from the Podiom home directory.
type FileComposer struct {
	paths config.Paths
}

// credentialsInstructions is composed into every run rather than living only in
// the base AGENTS.md, because that file is written once at scaffold time and
// never rewritten — an existing installation would otherwise never learn that
// the credentials store is there, which is the whole point of the rule.
const credentialsInstructions = `# Credentials and secrets

Podiom has one credentials store, and it is the only place a secret belongs.
Everything in it is set as an environment variable in every agent session.

**Look there first.** Before you conclude you are blocked on missing auth, or
ask the user for a token, call podiom_list_credentials. If the variable is
listed, it is already in your environment — read it as $NAME and use it. Do not
ask for something you already have.

**Store what you are given.** Any secret you receive or generate — the user
pastes a token in chat, a CLI mints an API key, a signup returns one — goes into
the store with podiom_store_credential, immediately, before you use it. If a
tool genuinely needs the value in a project file (a .env the build reads, an MCP
server's env_vars), put it there too, but Podiom's store is the durable copy and
the one you check next time.

**Nowhere else, ever.** Never write a secret into a shell profile, your
MEMORY.md, a workspace note, or a project file other than the one tool that
needs it. Never put a value in a task, schedule, progress entry, action item,
access request, or chat reply — Podiom stores and displays those. Name the
variable, never its value.

**Ask when you do not have it.** If you need a credential nobody has given you,
file podiom_request_access with kind=env_var: name the variable and its purpose,
never a value. The user enters it privately and it reaches your environment on
the next run.`

const workspaceFileSharingInstructions = `# Sharing workspace files with the user

Never ask the user to locate or open a file on the local filesystem. When a
workspace text file contains material they need to read, copy, review, or act
on, call podiom_attach_workspace_file and include its returned markdown_link in
your user-visible response or Podiom prose field. Explain the link with enough
context that the user does not need to understand your workspace or plan.`

// NewFileComposer returns a filesystem-backed instruction composer.
func NewFileComposer(paths config.Paths) *FileComposer {
	return &FileComposer{paths: paths}
}

// Compose produces and writes the provider-ready instruction payload.
func (c *FileComposer) Compose(ctx context.Context, agent store.Agent, mode DeliveryMode, projectInstructions string) (InstructionPayload, error) {
	if err := ctx.Err(); err != nil {
		return InstructionPayload{}, err
	}
	agentPaths := agentPaths(c.paths, agent.Name)
	sources, err := c.sources(agentPaths, projectInstructions)
	if err != nil {
		return InstructionPayload{}, err
	}
	switch mode {
	case DeliveryClaudeImport:
		return c.composeClaude(agent, agentPaths, sources)
	case DeliveryCodexBundle:
		return c.composeCodex(agent, agentPaths, sources)
	default:
		return InstructionPayload{}, fmt.Errorf("unknown instruction delivery mode %q", mode)
	}
}

func (c *FileComposer) sources(paths AgentPaths, projectInstructions string) ([]InstructionSource, error) {
	sources := []InstructionSource{{Label: "base AGENTS.md", Path: c.paths.BaseAgents}}
	// USER.md describes the human the agent works with. It sits directly after
	// the base layer so every agent reads it before its own identity. Like
	// MEMORY.md it is skipped entirely when absent or blank, so composeClaude
	// never emits a dangling @-import for a file that does not exist.
	if useMemory(c.paths.UserMD) {
		sources = append(sources, InstructionSource{Label: "USER.md", Path: c.paths.UserMD, Optional: true})
	}
	if _, err := os.Stat(paths.Agents); err == nil {
		sources = append(sources, InstructionSource{Label: "agent AGENTS.md", Path: paths.Agents})
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat %s: %w", paths.Agents, err)
	}
	sources = append(sources, InstructionSource{Label: "SOUL.md", Path: paths.Soul})
	if len(bytes.TrimSpace([]byte(projectInstructions))) > 0 {
		sources = append(sources, InstructionSource{
			Label:    "project instructions",
			Path:     filepath.Join(paths.Workspace, ".podiom-project-instructions.md"),
			Content:  []byte(projectInstructions),
			Optional: true,
		})
	}
	sources = append(sources, InstructionSource{
		Label:   "credentials",
		Path:    filepath.Join(paths.Workspace, ".podiom-credentials.md"),
		Content: []byte(credentialsInstructions),
	})
	sources = append(sources, InstructionSource{
		Label:   "workspace file sharing",
		Path:    filepath.Join(paths.Workspace, ".podiom-workspace-file-sharing.md"),
		Content: []byte(workspaceFileSharingInstructions),
	})
	// MEMORY.md stays last so the agent's learned context sits closest to the
	// live turn. It is optional and budget-capped.
	memory := InstructionSource{Label: "MEMORY.md", Path: paths.Memory, MaxLines: memoryBudgetLines, Optional: true}
	if useMemory(memory.Path) {
		sources = append(sources, memory)
	}
	for _, src := range sources {
		if src.Optional || len(src.Content) > 0 {
			continue
		}
		if _, err := os.Stat(src.Path); err != nil {
			return nil, fmt.Errorf("instruction source %s: %w", src.Path, err)
		}
	}
	return sources, nil
}

// useMemory reports whether MEMORY.md exists and carries real content. An empty
// or whitespace-only memory contributes nothing and is skipped so it does not add
// a blank layer to the composed payload (MEM2: an empty memory is valid).
func useMemory(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return len(bytes.TrimSpace(raw)) > 0
}

// truncateLines returns at most the first n lines of b. When it truncates, it
// appends a marker so the reader (human or model) knows the content was cut to
// fit the injection budget. n <= 0 means no limit.
func truncateLines(b []byte, n int) []byte {
	if n <= 0 {
		return b
	}
	lines := bytes.SplitAfter(b, []byte("\n"))
	if len(lines) <= n {
		return b
	}
	out := bytes.Join(lines[:n], nil)
	out = bytes.TrimRight(out, "\n")
	out = append(out, []byte("\n\n<!-- memory truncated to fit injection budget -->\n")...)
	return out
}

func (c *FileComposer) composeClaude(agent store.Agent, paths AgentPaths, sources []InstructionSource) (InstructionPayload, error) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# Podiom generated Claude context for %s\n\n", agent.Name)
	for _, src := range sources {
		// A budget-capped source (MEMORY.md) can't be @-imported directly — Claude
		// would pull in the whole file. Write a truncated snapshot beside the
		// payload and import that instead. It is regenerated on every compose, so
		// it always reflects the latest file (including user edits) at turn start.
		if src.MaxLines > 0 {
			snapPath, err := c.writeMemorySnapshot(paths, src)
			if err != nil {
				return InstructionPayload{}, err
			}
			if snapPath == "" {
				continue
			}
			fmt.Fprintf(&buf, "@%s\n", filepath.Clean(snapPath))
			continue
		}
		if len(src.Content) > 0 {
			if err := writeSnapshot(src.Path, src.Content); err != nil {
				return InstructionPayload{}, err
			}
			fmt.Fprintf(&buf, "@%s\n", filepath.Clean(src.Path))
			continue
		}
		fmt.Fprintf(&buf, "@%s\n", filepath.Clean(src.Path))
	}
	payloadPath := filepath.Join(paths.Workspace, "CLAUDE.md")
	return writePayload(DeliveryClaudeImport, payloadPath, buf.Bytes(), sources)
}

// writeMemorySnapshot writes the budget-truncated content of a capped source into
// the workspace and returns its path, or "" if the source is absent/empty.
func (c *FileComposer) writeMemorySnapshot(paths AgentPaths, src InstructionSource) (string, error) {
	raw, err := os.ReadFile(src.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read instruction source %s: %w", src.Path, err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", nil
	}
	snapPath := filepath.Join(paths.Workspace, ".podiom-memory.md")
	if err := os.MkdirAll(paths.Workspace, 0o755); err != nil {
		return "", fmt.Errorf("create workspace for memory snapshot: %w", err)
	}
	if err := os.WriteFile(snapPath, truncateLines(raw, src.MaxLines), 0o644); err != nil {
		return "", fmt.Errorf("write memory snapshot %s: %w", snapPath, err)
	}
	return snapPath, nil
}

func writeSnapshot(path string, raw []byte) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent for instruction snapshot: %w", err)
	}
	if err := os.WriteFile(path, bytes.TrimSpace(raw), 0o644); err != nil {
		return fmt.Errorf("write instruction snapshot %s: %w", path, err)
	}
	return nil
}

func (c *FileComposer) composeCodex(agent store.Agent, paths AgentPaths, sources []InstructionSource) (InstructionPayload, error) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# Podiom generated Codex instructions for %s\n\n", agent.Name)
	for i, src := range sources {
		raw := src.Content
		if len(raw) == 0 {
			var err error
			raw, err = os.ReadFile(src.Path)
			if err != nil {
				if src.Optional && os.IsNotExist(err) {
					continue
				}
				return InstructionPayload{}, fmt.Errorf("read instruction source %s: %w", src.Path, err)
			}
		}
		if src.MaxLines > 0 {
			raw = truncateLines(raw, src.MaxLines)
		}
		if i > 0 {
			buf.WriteString("\n\n")
		}
		fmt.Fprintf(&buf, "<!-- Source: %s -->\n\n", src.Path)
		buf.Write(bytes.TrimSpace(raw))
		buf.WriteByte('\n')
	}
	payloadPath := filepath.Join(paths.Workspace, "AGENTS.md")
	return writePayload(DeliveryCodexBundle, payloadPath, buf.Bytes(), sources)
}

func writePayload(mode DeliveryMode, path string, data []byte, sources []InstructionSource) (InstructionPayload, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return InstructionPayload{}, fmt.Errorf("create parent of %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return InstructionPayload{}, fmt.Errorf("write instruction payload %s: %w", path, err)
	}
	return InstructionPayload{
		Mode:    mode,
		Path:    path,
		Bytes:   data,
		Sources: append([]InstructionSource(nil), sources...),
	}, nil
}

func agentPaths(paths config.Paths, name string) AgentPaths {
	dir := filepath.Join(paths.AgentsDir, name)
	return AgentPaths{
		Root:      dir,
		Soul:      filepath.Join(dir, "SOUL.md"),
		Agents:    filepath.Join(dir, "AGENTS.md"),
		Memory:    filepath.Join(dir, "MEMORY.md"),
		Workspace: filepath.Join(dir, "workspace"),
		Tools:     filepath.Join(dir, "tools"),
		Avatar:    filepath.Join(dir, "avatar.png"),
	}
}
