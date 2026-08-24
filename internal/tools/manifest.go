package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// ManifestEntry records one tool Podiom installed into the toolset, with
// provenance back to the agent and session that asked for it. Podiom never
// touches the toolset directory without updating the manifest and vice versa.
type ManifestEntry struct {
	Tool      string `json:"tool"`
	Installer string `json:"installer"`
	Package   string `json:"package,omitempty"`
	Version   string `json:"version,omitempty"`
	URL       string `json:"url,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	// Path names the executable inside an archive, kept so a reinstall
	// reproduces the original install exactly.
	Path string `json:"path,omitempty"`
	// InstalledBy and SessionID are the self-service provenance: who asked
	// for this and in which session. Nothing in the toolset is anonymous.
	InstalledBy string `json:"installed_by,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	// RequestID and GoalID survive from the approval-gated per-agent era so
	// migrated entries keep their history (see Migrate).
	RequestID     string `json:"request_id,omitempty"`
	GoalID        string `json:"goal_id,omitempty"`
	InstalledAt   string `json:"installed_at"`
	VersionOutput string `json:"version_output,omitempty"`
	// NeedsReinstall marks an entry Podiom knows about but has no files for —
	// a per-agent install folded in by Migrate. The spec is intact, so one
	// install call restores it.
	NeedsReinstall bool `json:"needs_reinstall,omitempty"`
}

// Spec reconstructs the install description from a manifest entry, so a
// reinstall runs exactly what the original install ran.
func (e ManifestEntry) Spec() Spec {
	return Spec{
		Tool:      e.Tool,
		Installer: Installer(e.Installer),
		Package:   e.Package,
		Version:   e.Version,
		URL:       e.URL,
		SHA256:    e.SHA256,
		Path:      e.Path,
	}
}

// ToolStatus is a manifest entry plus its live on-disk health: Broken means
// the manifest claims the tool but its executable is gone (removed
// out-of-band). Reported, never silently dropped.
type ToolStatus struct {
	ManifestEntry
	Broken bool `json:"broken"`
}

func loadManifest(d Dirs) ([]ManifestEntry, error) {
	raw, err := os.ReadFile(d.Manifest)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read tool manifest: %w", err)
	}
	var entries []ManifestEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("parse tool manifest %s: %w", d.Manifest, err)
	}
	return entries, nil
}

func saveManifest(d Dirs, entries []ManifestEntry) error {
	if err := os.MkdirAll(d.Root, 0o755); err != nil {
		return fmt.Errorf("create tools dir: %w", err)
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("encode tool manifest: %w", err)
	}
	tmp := d.Manifest + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return fmt.Errorf("write tool manifest: %w", err)
	}
	if err := os.Rename(tmp, d.Manifest); err != nil {
		return fmt.Errorf("commit tool manifest: %w", err)
	}
	return nil
}

// recordInstall upserts the entry (a re-approved retry replaces the old row).
func recordInstall(d Dirs, entry ManifestEntry) error {
	entries, err := loadManifest(d)
	if err != nil {
		return err
	}
	out := entries[:0]
	for _, e := range entries {
		if e.Tool != entry.Tool {
			out = append(out, e)
		}
	}
	if entry.InstalledAt == "" {
		entry.InstalledAt = time.Now().UTC().Format(time.RFC3339)
	}
	return saveManifest(d, append(out, entry))
}

func removeManifestEntry(d Dirs, tool string) (ManifestEntry, bool, error) {
	entries, err := loadManifest(d)
	if err != nil {
		return ManifestEntry{}, false, err
	}
	var removed ManifestEntry
	found := false
	out := entries[:0]
	for _, e := range entries {
		if e.Tool == tool {
			removed = e
			found = true
			continue
		}
		out = append(out, e)
	}
	if !found {
		return ManifestEntry{}, false, nil
	}
	return removed, true, saveManifest(d, out)
}

// List returns the manifest with per-entry health.
func List(root string) ([]ToolStatus, error) {
	d := DirsFor(root)
	entries, err := loadManifest(d)
	if err != nil {
		return nil, err
	}
	out := make([]ToolStatus, 0, len(entries))
	for _, e := range entries {
		_, found := findExecutable(root, e.Tool)
		// A migrated entry has no files here yet, which is expected rather
		// than broken — NeedsReinstall already says so, and reporting both
		// would read as two problems instead of one pending action.
		out = append(out, ToolStatus{ManifestEntry: e, Broken: !found && !e.NeedsReinstall})
	}
	return out, nil
}

// findExecutable locates a tool inside the agent's PATH dirs (and only
// there — verification must never be satisfied by a host binary).
func findExecutable(root, tool string) (string, bool) {
	for _, dir := range PathDirs(root) {
		p := filepath.Join(dir, tool)
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Mode().Perm()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}
