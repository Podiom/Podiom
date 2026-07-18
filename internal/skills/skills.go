// Package skills discovers SKILL.md capability folders on the machine and
// presents them as a single deduplicated catalogue. Podiom owns no skills: it
// reads what already exists under three fixed, home-relative roots and unifies
// them under ~/.agents/skills via per-skill symlinks (the "union").
//
// The roots are deliberately independent of PODIOM_HOME — they are the same
// directories the standalone claude/codex CLIs use, so skill exposure stays
// decoupled from Podiom's storage root and from provider auth/profiles (S1/S7).
package skills

import (
	"os"
	"path/filepath"
	"strings"
)

// Source identifies which root a skill was found in.
type Source string

const (
	// SourceAgents is ~/.agents/skills — the canonical union, labelled "shared".
	SourceAgents Source = "agents"
	// SourceClaude is ~/.claude/skills — Claude's native personal skills.
	SourceClaude Source = "claude"
	// SourceCodex is ~/.codex/skills — Codex's native personal skills.
	SourceCodex Source = "codex"
)

// nativeRoots lists each provider's native personal-skill root as a home-relative
// directory, in stable output order. Adding a provider with native skills means
// one entry here (plus its Source constant above).
var nativeRoots = []struct {
	Src Source
	Dir string // home-relative, e.g. ".claude"
}{
	{SourceClaude, ".claude"},
	{SourceCodex, ".codex"},
}

// order is the stable source ordering used everywhere output is produced
// (canonical union first, then the provider roots).
var order = func() []Source {
	out := []Source{SourceAgents}
	for _, n := range nativeRoots {
		out = append(out, n.Src)
	}
	return out
}()

// NativeSources returns the provider-native skill sources in stable order.
func NativeSources() []Source {
	out := make([]Source, 0, len(nativeRoots))
	for _, n := range nativeRoots {
		out = append(out, n.Src)
	}
	return out
}

// EnvHome overrides the home directory the three skill roots are derived from.
// It exists so tests can point discovery at a scratch directory; production
// resolves the real user home. It is intentionally NOT PODIOM_HOME.
const EnvHome = "PODIOM_SKILLS_HOME"

// Roots holds the skill directories discovery reads: the canonical union plus
// one native root per provider.
type Roots struct {
	Agents string            // ~/.agents/skills  (canonical union, labelled "shared")
	Native map[Source]string // provider-native roots, e.g. ~/.claude/skills
}

// DefaultRoots resolves the skill roots from the user home (or EnvHome).
func DefaultRoots() (Roots, error) {
	home := strings.TrimSpace(os.Getenv(EnvHome))
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return Roots{}, err
		}
		home = h
	}
	roots := Roots{
		Agents: filepath.Join(home, ".agents", "skills"),
		Native: map[Source]string{},
	}
	for _, n := range nativeRoots {
		roots.Native[n.Src] = filepath.Join(home, n.Dir, "skills")
	}
	return roots, nil
}

// dir returns the root path for a given source ("" for an unknown source).
func (r Roots) dir(src Source) string {
	if src == SourceAgents {
		return r.Agents
	}
	return r.Native[src]
}

// Location is one place a skill name was found, with the user-facing path to its
// SKILL.md within that source root (not the resolved symlink target).
type Location struct {
	Source Source `json:"source"`
	Path   string `json:"path"`
}

// Content is a SKILL.md body. Source is empty for a unified skill (a single
// body) and set per-source when a conflict surfaces differing bodies.
type Content struct {
	Source Source `json:"source,omitempty"`
	Body   string `json:"body"`
}

// Skill is one deduplicated entry in the catalogue.
type Skill struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Sources     []Source   `json:"sources"`
	Conflict    bool       `json:"conflict"`
	Locations   []Location `json:"locations"`
	Contents    []Content  `json:"contents"`
}
