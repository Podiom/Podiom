package marketplace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrUnmanagedCollision is returned when an install would overwrite a directory
// Podiom does not manage (FR-12/16 — hand-placed skills are never touched).
var ErrUnmanagedCollision = errors.New("a skill directory with this name already exists and is not managed by Podiom")

// ErrAckRequired is returned when an executable skill is installed without the
// explicit acknowledgment (SEC-2 defense-in-depth on the server side).
var ErrAckRequired = errors.New("this skill contains executable scripts; acknowledgment is required to install")

// InstallRequest identifies what to install: a registry skill (Registry+ID) or a
// direct GitHub URL. Acknowledge must be true to install a skill with executable
// content (SEC-2).
type InstallRequest struct {
	Registry    RegistryID `json:"registry"`
	ID          string     `json:"id"`
	URL         string     `json:"url"`
	Acknowledge bool       `json:"acknowledge"`
}

// Install performs the ordered, atomic install (FR-11..16, SEC-3..5):
//  1. resolve to a concrete commit SHA (never a moving ref),
//  2. download the subtree into a temp dir inside the skills root,
//  3. validate statically (SKILL.md + frontmatter, size cap, path traversal —
//     the extractor already rejects "..", absolute paths, and symlinks),
//  4. resolve the install dir name and collision policy,
//  5. atomically move into ~/.agents/skills/<name>/ (0700/0600, never executed),
//  6. record the lockfile entry.
//
// Any failure before the move leaves nothing behind.
func (s *Service) Install(ctx context.Context, req InstallRequest) (InstalledSkill, error) {
	ref, registry, err := s.resolveInstallRef(ctx, req)
	if err != nil {
		return InstalledSkill{}, err
	}
	if s.curated && registry != RegistryAnthropics {
		return InstalledSkill{}, fmt.Errorf("curated-only mode: installs are restricted to Verified sources")
	}

	sha, err := s.gh.resolveSHA(ctx, ref.Owner, ref.Repo, firstNonEmpty(ref.SHA, "HEAD"))
	if err != nil {
		return InstalledSkill{}, err
	}
	ref.SHA = sha

	root := s.lock.agentsRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return InstalledSkill{}, err
	}
	staging, err := os.MkdirTemp(root, ".podiom-install-*")
	if err != nil {
		return InstalledSkill{}, err
	}
	defer os.RemoveAll(staging) // removed on error; on success the dir is emptied by the move

	zip, err := s.gh.downloadZip(ctx, ref)
	if err != nil {
		return InstalledSkill{}, err
	}
	rd, size := s.gh.zipReader(zip)
	if err := extractSubtree(rd, size, ref.Path, staging, s.gh.maxSize); err != nil {
		return InstalledSkill{}, err
	}

	// Static validation (FR-14): SKILL.md at root with name + description.
	detail, err := assembleDetail(staging, SkillSummary{Registry: registry, Owner: ref.Owner, Ref: ref})
	if err != nil {
		return InstalledSkill{}, err
	}
	name := ""
	desc := ""
	for _, f := range detail.Frontmatter {
		switch strings.ToLower(f.Key) {
		case "name":
			name = f.Value
		case "description":
			desc = f.Value
		}
	}
	if strings.TrimSpace(name) == "" || strings.TrimSpace(desc) == "" {
		return InstalledSkill{}, fmt.Errorf("invalid skill: SKILL.md frontmatter must define name and description")
	}
	if detail.HasExecutable && !req.Acknowledge {
		return InstalledSkill{}, ErrAckRequired
	}

	dirName, err := s.resolveInstallDir(kebab(name), ref)
	if err != nil {
		return InstalledSkill{}, err
	}
	target := filepath.Join(root, dirName)
	if err := replaceDir(target, staging); err != nil {
		return InstalledSkill{}, err
	}

	entry := LockEntry{
		Name:          dirName,
		Registry:      registry,
		Owner:         ref.Owner,
		Repo:          ref.Repo,
		Path:          strings.Trim(ref.Path, "/"),
		SHA:           ref.SHA,
		InstalledAt:   time.Now().UTC().Format(time.RFC3339),
		PodiomVersion: s.version,
	}
	if err := s.lock.Put(entry); err != nil {
		return InstalledSkill{}, err
	}
	s.log.Info("skill installed", "event", "marketplace", "name", dirName, "registry", registry,
		"owner", ref.Owner, "repo", ref.Repo, "sha", ref.SHA)
	return installedFromEntry(entry, desc, []string{string(rootLabelAgents)}), nil
}

// resolveInstallRef turns a request into a concrete SkillRef + registry. A URL
// that resolves to multiple skills is an error — the caller must resolve and pick
// one first (FR-23).
func (s *Service) resolveInstallRef(ctx context.Context, req InstallRequest) (SkillRef, RegistryID, error) {
	if strings.TrimSpace(req.URL) != "" {
		rows, err := s.ghURL.Resolve(ctx, req.URL)
		if err != nil {
			return SkillRef{}, "", err
		}
		if len(rows) == 0 {
			return SkillRef{}, "", fmt.Errorf("no skill found at %s", req.URL)
		}
		if len(rows) > 1 {
			return SkillRef{}, "", fmt.Errorf("%d skills found at that URL; choose one to install", len(rows))
		}
		return rows[0].Ref, RegistryGitHub, nil
	}
	if req.Registry == "" || req.ID == "" {
		return SkillRef{}, "", fmt.Errorf("install requires either a url or a registry and id")
	}
	ref, err := s.refFor(string(req.Registry), req.ID)
	if err != nil {
		return SkillRef{}, "", err
	}
	return ref, req.Registry, nil
}

// resolveInstallDir applies the collision policy (FR-12): a Podiom-managed dir of
// the same name is an in-place update; an unmanaged dir triggers the owner-suffix
// fallback, and if that also collides with an unmanaged dir, the install aborts.
func (s *Service) resolveInstallDir(base string, ref SkillRef) (string, error) {
	if base == "" {
		return "", fmt.Errorf("invalid skill name")
	}
	root := s.lock.agentsRoot()
	entries, err := s.lock.Entries()
	if err != nil {
		return "", err
	}
	managed := func(name string) bool { _, ok := entries[name]; return ok }
	exists := func(name string) bool {
		info, err := os.Stat(filepath.Join(root, name))
		return err == nil && info.IsDir()
	}

	if managed(base) || !exists(base) {
		return base, nil
	}
	// Unmanaged collision → try the owner-suffixed name.
	suffixed := kebab(ref.Owner + "-" + base)
	if managed(suffixed) || !exists(suffixed) {
		return suffixed, nil
	}
	return "", ErrUnmanagedCollision
}

// replaceDir atomically installs staged content at target. When target already
// exists (a managed reinstall/update), the old copy is moved aside and removed
// only after the new one is in place, so a mid-op failure is recoverable.
func replaceDir(target, staged string) error {
	_, statErr := os.Stat(target)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return os.Rename(staged, target)
		}
		return statErr
	}
	backup := target + ".podiom-old-" + itoa(int(time.Now().UnixNano()%1_000_000))
	if err := os.Rename(target, backup); err != nil {
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		_ = os.Rename(backup, target) // best-effort restore
		return err
	}
	return os.RemoveAll(backup)
}

// rootLabelAgents is the local skills-root label installs land in.
const rootLabelAgents = "agents"

func installedFromEntry(e LockEntry, desc string, roots []string) InstalledSkill {
	return InstalledSkill{
		Name:        e.Name,
		Description: desc,
		Managed:     true,
		Registry:    e.Registry,
		Owner:       e.Owner,
		Repo:        e.Repo,
		Path:        e.Path,
		SHA:         e.SHA,
		InstalledAt: e.InstalledAt,
		RepoURL:     repoURL(e.Owner, e.Repo, e.Path, e.SHA),
		Roots:       roots,
	}
}

func repoURL(owner, repo, path, sha string) string {
	if owner == "" || repo == "" {
		return ""
	}
	u := "https://github.com/" + owner + "/" + repo
	if sha != "" && path != "" {
		return u + "/tree/" + sha + "/" + strings.Trim(path, "/")
	}
	if sha != "" {
		return u + "/tree/" + sha
	}
	return u
}
