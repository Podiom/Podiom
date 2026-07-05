// Package marketplace implements Podiom's skill search & install experience
// (Spec 07). It searches public skill registries, resolves every skill to a
// canonical GitHub location, lets the dashboard inspect every file before it
// runs anywhere near an agent, and installs skills — pinned to a commit SHA —
// into the shared pool at ~/.agents/skills.
//
// Third-party skills are untrusted code AND untrusted prompt content, so
// pre-install inspection, SHA pinning, path-traversal rejection, and a static
// heuristic scan are first-class here, not hardening-later.
//
// Naming: the marketplace "registry" (RegistryID) is a distinct concept from the
// skills package's local Source root ("agents"/"claude"/"codex"). Never conflate
// them.
package marketplace

import "time"

// RegistryID identifies one search source.
type RegistryID string

const (
	// RegistrySkillsMP is skillsmp.com — the primary REST registry.
	RegistrySkillsMP RegistryID = "skillsmp"
	// RegistryAnthropics is the curated anthropics/skills GitHub repo (Verified).
	RegistryAnthropics RegistryID = "anthropics"
	// RegistryGitHub is a direct GitHub URL install (escape hatch).
	RegistryGitHub RegistryID = "github"
)

// SkillRef is a skill's canonical GitHub identity. The dedup key (SRC-2) is
// owner/repo/path lowercased; SHA pins the exact commit installs resolve to.
type SkillRef struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	Path  string `json:"path"` // subdirectory holding SKILL.md ("" = repo root)
	SHA   string `json:"sha,omitempty"`
}

// SkillSummary is one search-result row.
type SkillSummary struct {
	// ID is a registry-scoped opaque handle the detail/install endpoints echo
	// back to the source. For GitHub-backed sources it is "owner/repo/path".
	ID          string     `json:"id"`
	Registry    RegistryID `json:"registry"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Owner       string     `json:"owner"`
	Ref         SkillRef   `json:"ref"`
	Stars       int        `json:"stars,omitempty"`
	Installs    int        `json:"installs,omitempty"`
	UpdatedAt   string     `json:"updated_at,omitempty"`
	HasScripts  bool       `json:"has_scripts"`
	Verified    bool       `json:"verified"`
	// Installed / UpdateAvailable are filled by cross-referencing the lockfile.
	Installed       bool `json:"installed"`
	UpdateAvailable bool `json:"update_available"`
}

// FrontmatterField is one SKILL.md frontmatter key/value, kept as an ordered
// slice so the detail view renders a stable metadata table.
type FrontmatterField struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// FileNode is one entry in a skill's file tree.
type FileNode struct {
	Path       string `json:"path"` // skill-relative (SKILL.md, scripts/run.sh, …)
	IsDir      bool   `json:"is_dir"`
	Size       int64  `json:"size"`
	Executable bool   `json:"executable"`
}

// ScanFinding is one static-heuristic warning (SEC-6). Findings inform; the user
// decides.
type ScanFinding struct {
	File     string `json:"file"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"` // "info" | "warn"
	Message  string `json:"message"`
}

// SkillDetail is the full pre-install inspection payload (FR-7..10).
type SkillDetail struct {
	SkillSummary
	Frontmatter   []FrontmatterField `json:"frontmatter"`
	SkillMD       string             `json:"skill_md"`
	Tree          []FileNode         `json:"tree"`
	License       string             `json:"license,omitempty"`
	HasExecutable bool               `json:"has_executable"`
	Size          int64              `json:"size"`
	ScanFindings  []ScanFinding      `json:"scan_findings"`
}

// InstalledSkill is one row of the Installed list (FR-17): the on-disk catalogue
// left-joined with lockfile provenance.
type InstalledSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Managed     bool   `json:"managed"`
	// Provenance (managed skills only).
	Registry    RegistryID `json:"registry,omitempty"`
	Owner       string     `json:"owner,omitempty"`
	Repo        string     `json:"repo,omitempty"`
	Path        string     `json:"path,omitempty"`
	SHA         string     `json:"sha,omitempty"`
	InstalledAt string     `json:"installed_at,omitempty"`
	RepoURL     string     `json:"repo_url,omitempty"`
	// Roots lists which local skill roots the skill was found in ("agents", etc.).
	Roots           []string `json:"roots"`
	UpdateAvailable bool     `json:"update_available"`
}

// cacheEntry backs the in-memory TTL cache (SRC-4).
type cacheEntry struct {
	value   any
	expires time.Time
}
