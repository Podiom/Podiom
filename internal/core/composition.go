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
// instructions, and SOUL.md in the fixed order required by the spec.
type InstructionComposer interface {
	Compose(context.Context, store.Agent, DeliveryMode) (InstructionPayload, error)
}

// FileComposer composes instruction payloads from the Podiom home directory.
type FileComposer struct {
	paths config.Paths
}

// NewFileComposer returns a filesystem-backed instruction composer.
func NewFileComposer(paths config.Paths) *FileComposer {
	return &FileComposer{paths: paths}
}

// Compose produces and writes the provider-ready instruction payload.
func (c *FileComposer) Compose(ctx context.Context, agent store.Agent, mode DeliveryMode) (InstructionPayload, error) {
	if err := ctx.Err(); err != nil {
		return InstructionPayload{}, err
	}
	agentPaths := agentPaths(c.paths, agent.Name)
	sources, err := c.sources(agentPaths)
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

func (c *FileComposer) sources(paths AgentPaths) ([]InstructionSource, error) {
	sources := []InstructionSource{{Label: "base AGENTS.md", Path: c.paths.BaseAgents}}
	if _, err := os.Stat(paths.Agents); err == nil {
		sources = append(sources, InstructionSource{Label: "agent AGENTS.md", Path: paths.Agents})
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat %s: %w", paths.Agents, err)
	}
	sources = append(sources, InstructionSource{Label: "SOUL.md", Path: paths.Soul})
	// MEMORY.md is the fourth composition layer (MEM3): base AGENTS.md → agent
	// AGENTS.md → SOUL.md → MEMORY.md, memory last so the agent's learned context
	// sits closest to the live turn. It is optional and budget-capped.
	memory := InstructionSource{Label: "MEMORY.md", Path: paths.Memory, MaxLines: memoryBudgetLines, Optional: true}
	if useMemory(memory.Path) {
		sources = append(sources, memory)
	}
	for _, src := range sources {
		if src.Optional {
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

func (c *FileComposer) composeCodex(agent store.Agent, paths AgentPaths, sources []InstructionSource) (InstructionPayload, error) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# Podiom generated Codex instructions for %s\n\n", agent.Name)
	for i, src := range sources {
		raw, err := os.ReadFile(src.Path)
		if err != nil {
			if src.Optional && os.IsNotExist(err) {
				continue
			}
			return InstructionPayload{}, fmt.Errorf("read instruction source %s: %w", src.Path, err)
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
