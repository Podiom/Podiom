package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
)

var nativeNamePart = regexp.MustCompile(`[^a-z0-9]+`)

// nativeAgentsForProvider projects Podiom agents into provider-native agent
// definitions. The projection is best-effort and disposable: callers should log
// errors and continue without it.
func (c *Core) nativeAgentsForProvider(ctx context.Context, provider config.Provider, activePodiomName string) (string, []adapter.NativeAgent, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	agents, err := c.store.ListAgents(ctx)
	if err != nil {
		return "", nil, err
	}
	out := make([]adapter.NativeAgent, 0, len(agents))
	active := ""
	for _, agent := range agents {
		native, err := c.nativeAgentForProvider(provider, agent)
		if err != nil {
			return "", nil, err
		}
		out = append(out, native)
		if agent.Name == activePodiomName {
			active = native.Name
		}
	}
	return active, out, nil
}

func (c *Core) nativeAgentForProvider(provider config.Provider, agent store.Agent) (adapter.NativeAgent, error) {
	name := nativeAgentName(provider, agent.Name)
	instructions, err := c.nativeAgentInstructions(agent)
	if err != nil {
		return adapter.NativeAgent{}, err
	}
	native := adapter.NativeAgent{
		PodiomName:   agent.Name,
		Name:         name,
		Description:  fmt.Sprintf("Podiom agent %s. Use when Podiom asks for or delegates work to %s.", agent.Name, agent.Name),
		Instructions: instructions,
	}
	if agent.Provider == provider {
		native.Model = agent.Model
		native.Effort = agent.Effort
	}
	if provider == config.ProviderCodex {
		native.ConfigPath = filepath.Join(c.AgentPaths(agent.Name).Workspace, ".podiom", "native-agents", "codex", name+".toml")
	}
	return native, nil
}

func nativeAgentName(provider config.Provider, podiomName string) string {
	separator := "-"
	prefix := "podiom-"
	if provider == config.ProviderCodex {
		separator = "_"
		prefix = "podiom_"
	}
	stem := strings.ToLower(strings.TrimSpace(podiomName))
	stem = nativeNamePart.ReplaceAllString(stem, separator)
	stem = strings.Trim(stem, separator)
	if stem == "" {
		stem = "agent"
	}
	if len(stem) > 32 {
		stem = strings.Trim(stem[:32], separator)
	}
	sum := sha256.Sum256([]byte(podiomName))
	hash := hex.EncodeToString(sum[:])[:8]
	return prefix + stem + separator + hash
}

func (c *Core) nativeAgentInstructions(agent store.Agent) (string, error) {
	paths := c.AgentPaths(agent.Name)
	sources, err := (&FileComposer{paths: c.paths}).sources(paths, "")
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# Podiom native agent projection: %s\n\n", agent.Name)
	buf.WriteString("This provider-native agent is a best-effort projection of a Podiom agent. Podiom remains the source of truth for identity, memory, workspace, permissions, tools, and run settings.\n")
	for _, src := range sources {
		raw, err := os.ReadFile(src.Path)
		if err != nil {
			if src.Optional && os.IsNotExist(err) {
				continue
			}
			return "", fmt.Errorf("read native agent source %s: %w", src.Path, err)
		}
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			continue
		}
		if src.MaxLines > 0 {
			raw = bytes.TrimSpace(truncateLines(raw, src.MaxLines))
		}
		fmt.Fprintf(&buf, "\n\n## %s\n\n", src.Label)
		buf.Write(raw)
	}
	buf.WriteByte('\n')
	return buf.String(), nil
}
