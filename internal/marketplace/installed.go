package marketplace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Podiom/Podiom/internal/skills"
)

// ErrNotManaged is returned when uninstall/update is attempted on a skill Podiom
// does not manage (FR-16 — hand-placed skills are never modified or deleted).
var ErrNotManaged = errors.New("skill is not managed by Podiom")

// Installed returns the full Installed list (FR-17): the on-disk catalogue from
// skills.Scan() left-joined with lockfile provenance so each row is marked
// Managed (with SHA/registry/date) or Unmanaged. Works fully offline (NFR-4).
func (s *Service) Installed() ([]InstalledSkill, error) {
	catalogue, err := skills.Scan()
	if err != nil {
		return nil, err
	}
	entries, err := s.lock.Entries()
	if err != nil {
		return nil, err
	}
	out := make([]InstalledSkill, 0, len(catalogue))
	for _, sk := range catalogue {
		row := InstalledSkill{
			Name:        sk.Name,
			Description: sk.Description,
			Roots:       rootLabels(sk.Sources),
		}
		if e, ok := entries[sk.Name]; ok {
			row.Managed = true
			row.Registry = e.Registry
			row.Owner = e.Owner
			row.Repo = e.Repo
			row.Path = e.Path
			row.SHA = e.SHA
			row.InstalledAt = e.InstalledAt
			row.RepoURL = repoURL(e.Owner, e.Repo, e.Path, e.SHA)
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Uninstall removes a managed skill's directory and its lockfile entry (FR-18).
// Unmanaged skills are refused (FR-16). The directory is confirmed to live under
// the skills root before removal.
func (s *Service) Uninstall(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("skill name is required")
	}
	entry, ok, err := s.lock.Get(name)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotManaged
	}
	root := s.lock.agentsRoot()
	target := filepath.Join(root, entry.Name)
	// Defense in depth: never delete outside the skills root.
	if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(root)+string(os.PathSeparator)) {
		return fmt.Errorf("refusing to remove path outside skills root")
	}
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	if err := s.lock.Delete(entry.Name); err != nil {
		return err
	}
	s.log.Info("skill uninstalled", "event", "marketplace", "name", entry.Name)
	return nil
}

// rootLabels maps skills.Source roots to their string labels for the UI banner.
func rootLabels(srcs []skills.Source) []string {
	out := make([]string, 0, len(srcs))
	for _, src := range srcs {
		out = append(out, string(src))
	}
	return out
}
