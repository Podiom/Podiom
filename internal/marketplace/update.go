package marketplace

import (
	"context"
	"sort"
	"strings"
)

// UpdateStatus is the result of an update check (FR-19). When Available, Changed
// lists the files that differ between the installed SHA and the current HEAD so
// the UI can show a diff summary before the user applies.
type UpdateStatus struct {
	Name            string          `json:"name"`
	Available       bool            `json:"available"`
	CurrentSHA      string          `json:"current_sha"`
	LatestSHA       string          `json:"latest_sha"`
	Changed         []string        `json:"changed,omitempty"`
	Installed       bool            `json:"installed"`
	InstalledDetail *InstalledSkill `json:"installed_detail,omitempty"`
}

// CheckUpdate compares a managed skill's pinned SHA against the source's current
// HEAD for its path (FR-19). It never writes anything; updates are user-initiated.
func (s *Service) CheckUpdate(ctx context.Context, name string) (UpdateStatus, error) {
	entry, ok, err := s.lock.Get(strings.TrimSpace(name))
	if err != nil {
		return UpdateStatus{}, err
	}
	if !ok {
		return UpdateStatus{}, ErrNotManaged
	}
	latest, err := s.gh.resolveSHA(ctx, entry.Owner, entry.Repo, "HEAD")
	if err != nil {
		return UpdateStatus{}, err
	}
	st := UpdateStatus{
		Name:       entry.Name,
		CurrentSHA: entry.SHA,
		LatestSHA:  latest,
		Available:  latest != "" && latest != entry.SHA,
	}
	if st.Available {
		st.Changed = s.changedFiles(ctx, entry, latest)
	}
	return st, nil
}

// ApplyUpdate re-installs a managed skill at the source's current HEAD (FR-19).
// It reuses the atomic install path, so a failed update never corrupts the
// existing install.
func (s *Service) ApplyUpdate(ctx context.Context, name string, acknowledge bool) (InstalledSkill, error) {
	entry, ok, err := s.lock.Get(strings.TrimSpace(name))
	if err != nil {
		return InstalledSkill{}, err
	}
	if !ok {
		return InstalledSkill{}, ErrNotManaged
	}
	id := entry.Owner + "/" + entry.Repo
	if entry.Path != "" {
		id += "/" + entry.Path
	}
	return s.Install(ctx, InstallRequest{
		Registry:    entry.Registry,
		ID:          id,
		Acknowledge: acknowledge,
	})
}

// changedFiles produces a best-effort changed-files summary by comparing the file
// trees at the two SHAs. It is a summary, not a byte diff — enough for the UI to
// show what an update would touch. Errors degrade to an empty list.
func (s *Service) changedFiles(ctx context.Context, entry LockEntry, latestSHA string) []string {
	oldRef := SkillRef{Owner: entry.Owner, Repo: entry.Repo, Path: entry.Path, SHA: entry.SHA}
	newRef := SkillRef{Owner: entry.Owner, Repo: entry.Repo, Path: entry.Path, SHA: latestSHA}
	oldTree, err1 := s.gh.tree(ctx, oldRef)
	newTree, err2 := s.gh.tree(ctx, newRef)
	if err1 != nil || err2 != nil {
		return nil
	}
	oldByPath := map[string]int64{}
	for _, n := range oldTree {
		if !n.IsDir {
			oldByPath[n.Path] = n.Size
		}
	}
	changed := map[string]struct{}{}
	for _, n := range newTree {
		if n.IsDir {
			continue
		}
		if sz, ok := oldByPath[n.Path]; !ok {
			changed[n.Path+" (added)"] = struct{}{}
		} else if sz != n.Size {
			changed[n.Path+" (modified)"] = struct{}{}
		}
		delete(oldByPath, n.Path)
	}
	for p := range oldByPath {
		changed[p+" (removed)"] = struct{}{}
	}
	out := make([]string, 0, len(changed))
	for c := range changed {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
