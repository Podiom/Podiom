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

// ManifestEntry records one tool Podiom installed for an agent, with
// provenance back to the approval that authorized it. Podiom never touches
// the tool directory without updating the manifest and vice versa.
type ManifestEntry struct {
	Tool          string `json:"tool"`
	Installer     string `json:"installer"`
	Package       string `json:"package,omitempty"`
	Version       string `json:"version,omitempty"`
	URL           string `json:"url,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
	RequestID     string `json:"request_id,omitempty"`
	GoalID        string `json:"goal_id,omitempty"`
	InstalledAt   string `json:"installed_at"`
	VersionOutput string `json:"version_output,omitempty"`
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
		out = append(out, ToolStatus{ManifestEntry: e, Broken: !found})
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
