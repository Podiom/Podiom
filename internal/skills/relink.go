package skills

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// LinkAction records one change (or attempted change) a Relink run made.
type LinkAction struct {
	Name   string `json:"name"`
	Source Source `json:"source"`
	Target string `json:"target"` // real folder linked into the union
	Link   string `json:"link"`   // path of the union entry
	Status string `json:"status"` // linked | exists | conflict | error
	Detail string `json:"detail,omitempty"`
}

// Report summarizes a Relink run.
type Report struct {
	Canonical string       `json:"canonical"` // ~/.agents/skills
	Actions   []LinkAction `json:"actions"`
}

// Provision ensures every skill root exists so the feature works regardless
// of install order (S24). It never clobbers existing real dirs.
func Provision() error {
	roots, err := DefaultRoots()
	if err != nil {
		return err
	}
	for _, src := range order {
		if err := os.MkdirAll(roots.dir(src), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", roots.dir(src), err)
		}
	}
	return nil
}

// Relink (re)builds the union under ~/.agents/skills: every skill found in
// ~/.claude/skills or ~/.codex/skills that is not already present by name in the
// union gets a per-skill symlink pointing at its real folder (S9). It is
// idempotent (S26), never overwrites a real folder (S25), and records a conflict
// rather than relinking when a same-named union entry has differing content
// (S11). It does not depend on a running daemon.
func Relink() (Report, error) {
	roots, err := DefaultRoots()
	if err != nil {
		return Report{}, err
	}
	return RelinkRoots(roots)
}

// RelinkRoots is Relink against an explicit set of roots (used by tests).
func RelinkRoots(roots Roots) (Report, error) {
	rep := Report{Canonical: roots.Agents}
	if err := os.MkdirAll(roots.Agents, 0o755); err != nil {
		return rep, fmt.Errorf("create union dir %s: %w", roots.Agents, err)
	}
	for _, n := range nativeRoots {
		src := n.Src
		ents, err := readSkillDir(src, roots.dir(src))
		if err != nil {
			return rep, err
		}
		for _, e := range ents {
			rep.Actions = append(rep.Actions, linkOne(roots.Agents, e))
		}
	}
	return rep, nil
}

// linkOne reconciles a single provider skill into the union directory.
func linkOne(unionDir string, e entry) LinkAction {
	link := filepath.Join(unionDir, e.name)
	act := LinkAction{Name: e.name, Source: e.source, Target: e.realPath, Link: link}

	info, err := os.Lstat(link)
	if err != nil {
		if !os.IsNotExist(err) {
			act.Status = "error"
			act.Detail = err.Error()
			return act
		}
		// Nothing there yet — create the per-skill link.
		if err := makeLink(e.realPath, link); err != nil {
			act.Status = "error"
			act.Detail = err.Error()
			return act
		}
		act.Status = "linked"
		return act
	}

	// An entry already exists at the union path.
	if info.Mode()&os.ModeSymlink != 0 {
		if cur, _ := filepath.EvalSymlinks(link); cur == e.realPath {
			act.Status = "exists" // already points at this skill
		} else {
			act.Status = "exists"
			act.Detail = "union entry already links elsewhere; left untouched"
		}
		return act
	}

	// A real folder lives directly in the union (a genuinely shared skill, S3).
	// Never overwrite it; surface a conflict if its content differs (S11/S25).
	if existing, ok := readSkillBody(link); ok && normalizeBody(existing) != normalizeBody(e.body) {
		act.Status = "conflict"
		act.Detail = "same-named skill with differing content already in union"
	} else {
		act.Status = "exists"
	}
	return act
}

// makeLink creates a directory symlink, falling back to a Windows junction when
// symlink creation is unavailable (developer mode / elevation, S27).
func makeLink(target, link string) error {
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			if jerr := mklinkJunction(target, link); jerr != nil {
				return fmt.Errorf("symlink failed (%v) and junction fallback failed (%w)", err, jerr)
			}
			return nil
		}
		return err
	}
	return nil
}

func mklinkJunction(target, link string) error {
	return exec.Command("cmd", "/c", "mklink", "/J", link, target).Run()
}

func readSkillBody(dir string) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return "", false
	}
	return string(raw), true
}
