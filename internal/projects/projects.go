// Package projects manages Podiom's shared, system-level project ledger
// (§5.3 / D22): a single `projects.yaml` plus one subdirectory per project under
// ~/.podiom/projects/. Projects are agent-independent — any agent can read and
// work on any project. Agents also maintain this file directly, so the ledger is
// the source of truth and v1 accepts last-write-wins (R5.12).
package projects

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

var safeProjectID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Project mirrors one entry in projects.yaml (R5.10). `Path` is relative to the
// projects directory.
type Project struct {
	ID           string    `yaml:"id" json:"id"`
	Name         string    `yaml:"name" json:"name"`
	Description  string    `yaml:"description" json:"description"`
	Color        string    `yaml:"color,omitempty" json:"color"`
	Path         string    `yaml:"path" json:"path"`
	Status       string    `yaml:"status" json:"status"`
	Stack        []string  `yaml:"stack" json:"stack"`
	Repo         *Repo     `yaml:"repo" json:"repo"`
	Git          *Git      `yaml:"git,omitempty" json:"git,omitempty"`
	GitState     *GitState `yaml:"-" json:"git_state,omitempty"`
	Roadmap      []string  `yaml:"roadmap" json:"roadmap"`
	Notes        string    `yaml:"notes" json:"notes"`
	Instructions string    `yaml:"instructions,omitempty" json:"instructions"`
}

// Branching policies.
const (
	// BranchingDirect commits straight to the default branch.
	BranchingDirect = "direct"
	// BranchingPerTask puts each feature or fix on its own branch.
	BranchingPerTask = "branch-per-task"
)

// Commit policies.
const (
	// CommitAsk means the agent commits only when asked to.
	CommitAsk = "ask"
	// CommitAuto lets the agent commit its own completed work.
	CommitAuto = "auto"
)

// Git declares how a project wants to be versioned. It is first-class project
// data rather than a property of a connected remote, because the three postures
// a project can take are distinguishable without one:
//
//	Enabled=false            no source control; the agent performs no git
//	Enabled, Remote==""      a local repository, created in place with git init
//	Enabled, Remote!=""      a repository that lives on a host; cloned
//
// Nil means "not declared" and is treated as disabled, which is what every
// project created before this existed should be until the user opts in.
type Git struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Remote  string `yaml:"remote,omitempty" json:"remote"`
	// DefaultBranch is the branch work returns to and branches off.
	DefaultBranch string `yaml:"default_branch,omitempty" json:"default_branch"`
	// Branching is BranchingDirect or BranchingPerTask. It is executable, not
	// advisory: podiom_start_work applies it so the agent cannot skip it.
	Branching string `yaml:"branching,omitempty" json:"branching"`
	// BranchPrefixes maps a kind of work ("feature", "bugfix") to its branch
	// prefix. Missing kinds fall back to "work/".
	BranchPrefixes map[string]string `yaml:"branch_prefixes,omitempty" json:"branch_prefixes"`
	// Commit is CommitAsk or CommitAuto.
	Commit string `yaml:"commit,omitempty" json:"commit"`
	// PullOnSessionStart updates the default branch before a newly-created
	// session starts. It is deliberately opt-in so existing projects never gain
	// network or branch-changing behaviour on upgrade.
	PullOnSessionStart bool `yaml:"pull_on_session_start,omitempty" json:"pull_on_session_start"`
}

// GitState is the computed, non-persistent view of the repository currently on
// disk. The Git block above is policy; this state lets the UI distinguish that
// policy from a checkout that is missing, ignored, or temporarily unready.
type GitState struct {
	Detected        bool   `json:"detected"`
	Ready           bool   `json:"ready"`
	Root            string `json:"root,omitempty"`
	Branch          string `json:"branch,omitempty"`
	Remote          string `json:"remote,omitempty"`
	RemoteAmbiguous bool   `json:"remote_ambiguous,omitempty"`
	Warning         string `json:"warning,omitempty"`
}

// defaultBranchPrefixes are used when a project does not name its own.
var defaultBranchPrefixes = map[string]string{
	"feature": "feature/",
	"bugfix":  "fix/",
	"chore":   "chore/",
}

// normalizeGit fills in the defaults a partially specified block implies, so
// every consumer can read the values without repeating fallback logic. A
// disabled block keeps its fields: turning git back on should not silently
// discard a remote the user already configured.
func normalizeGit(g *Git) *Git {
	if g == nil {
		return nil
	}
	out := *g
	out.Remote = strings.TrimSpace(out.Remote)
	if strings.TrimSpace(out.DefaultBranch) == "" {
		out.DefaultBranch = "main"
	}
	if out.Branching != BranchingPerTask {
		out.Branching = BranchingDirect
	}
	if out.Commit != CommitAuto {
		out.Commit = CommitAsk
	}
	if len(out.BranchPrefixes) == 0 {
		out.BranchPrefixes = map[string]string{}
		for kind, prefix := range defaultBranchPrefixes {
			out.BranchPrefixes[kind] = prefix
		}
	}
	return &out
}

// BranchFor renders the branch name for a piece of work under this policy.
// Under BranchingDirect the answer is always the default branch — the policy
// decides, not the caller.
func (g *Git) BranchFor(kind, slug string) string {
	if g == nil || !g.Enabled {
		return ""
	}
	normalized := normalizeGit(g)
	if normalized.Branching != BranchingPerTask {
		return normalized.DefaultBranch
	}
	slug = branchSlug(slug)
	if slug == "" {
		return normalized.DefaultBranch
	}
	prefix, ok := normalized.BranchPrefixes[strings.ToLower(strings.TrimSpace(kind))]
	if !ok || prefix == "" {
		prefix = "work/"
	}
	return prefix + slug
}

// branchSlug reduces free text to something git accepts as a branch component.
var branchUnsafe = regexp.MustCompile(`[^a-z0-9._-]+`)

func branchSlug(raw string) string {
	slug := branchUnsafe.ReplaceAllString(strings.ToLower(strings.TrimSpace(raw)), "-")
	slug = strings.Trim(slug, "-._")
	if len(slug) > 60 {
		slug = strings.Trim(slug[:60], "-._")
	}
	return slug
}

// Repo describes an optional external source linked to a Podiom project. v1
// supports GitHub archive snapshots extracted into the project's repo directory.
type Repo struct {
	Provider      string `yaml:"provider" json:"provider"`
	Mode          string `yaml:"mode" json:"mode"`
	Owner         string `yaml:"owner" json:"owner"`
	Name          string `yaml:"name" json:"name"`
	FullName      string `yaml:"full_name" json:"full_name"`
	HTMLURL       string `yaml:"html_url" json:"html_url"`
	DefaultBranch string `yaml:"default_branch" json:"default_branch"`
	Ref           string `yaml:"ref" json:"ref"`
	SyncedAt      string `yaml:"synced_at" json:"synced_at"`
	SourceKind    string `yaml:"source_kind" json:"source_kind"`
}

// ProjectPatch carries the mutable fields a user can edit from the UI. Nil
// pointers are left unchanged so partial updates are safe under the
// last-write-wins ledger.
type ProjectPatch struct {
	Name         *string
	Description  *string
	Color        *string
	Status       *string
	Stack        *[]string
	Repo         *Repo
	ClearRepo    bool
	Git          *Git
	Notes        *string
	Instructions *string
}

// ledgerFile is the on-disk shape of projects.yaml.
type ledgerFile struct {
	Projects []Project `yaml:"projects"`
}

// Ledger reads and writes the shared projects.yaml. It serializes Podiom's own
// writes with a mutex; cross-process writes by agents remain last-write-wins.
type Ledger struct {
	dir  string // the projects directory (holds projects.yaml + project dirs)
	path string // projects.yaml
	mu   sync.Mutex
}

// New returns a Ledger rooted at the projects directory.
func New(projectsDir string) *Ledger {
	return &Ledger{
		dir:  projectsDir,
		path: filepath.Join(projectsDir, "projects.yaml"),
	}
}

// List returns all projects from the ledger. A missing ledger yields an empty
// list rather than an error.
func (l *Ledger) List() ([]Project, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	file, err := l.read()
	if err != nil {
		return nil, err
	}
	return file.Projects, nil
}

// Get returns a single project by id.
func (l *Ledger) Get(id string) (Project, error) {
	projects, err := l.List()
	if err != nil {
		return Project{}, err
	}
	for _, p := range projects {
		if p.ID == id {
			return p, nil
		}
	}
	return Project{}, fmt.Errorf("project %q not found", id)
}

// Create adds a new project: it validates the id, creates the project directory
// under the projects root, and appends an entry to the ledger. It errors if the
// id already exists.
func (l *Ledger) Create(p Project) (Project, error) {
	if !safeProjectID.MatchString(p.ID) {
		return Project{}, fmt.Errorf("invalid project id %q: use letters, numbers, dot, dash, or underscore", p.ID)
	}
	if p.Name == "" {
		p.Name = p.ID
	}
	if p.Path == "" {
		p.Path = p.ID
	}
	if p.Status == "" {
		p.Status = "active"
	}
	if p.Stack == nil {
		p.Stack = []string{}
	}
	if p.Roadmap == nil {
		p.Roadmap = []string{}
	}
	p.Git = normalizeGit(p.Git)

	l.mu.Lock()
	defer l.mu.Unlock()
	file, err := l.read()
	if err != nil {
		return Project{}, err
	}
	for _, existing := range file.Projects {
		if existing.ID == p.ID {
			return Project{}, fmt.Errorf("project %q already exists", p.ID)
		}
	}

	if err := os.MkdirAll(filepath.Join(l.dir, p.Path), 0o755); err != nil {
		return Project{}, fmt.Errorf("create project dir: %w", err)
	}
	file.Projects = append(file.Projects, p)
	if err := l.write(file); err != nil {
		return Project{}, err
	}
	return p, nil
}

// Update applies a partial patch to an existing project and rewrites the
// ledger. It errors if the id does not exist.
func (l *Ledger) Update(id string, patch ProjectPatch) (Project, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	file, err := l.read()
	if err != nil {
		return Project{}, err
	}
	idx := -1
	for i := range file.Projects {
		if file.Projects[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Project{}, fmt.Errorf("project %q not found", id)
	}
	p := &file.Projects[idx]
	if patch.Name != nil {
		p.Name = *patch.Name
	}
	if patch.Description != nil {
		p.Description = *patch.Description
	}
	if patch.Color != nil {
		p.Color = *patch.Color
	}
	if patch.Git != nil {
		p.Git = normalizeGit(patch.Git)
	}
	if patch.Status != nil {
		p.Status = *patch.Status
	}
	if patch.Stack != nil {
		p.Stack = *patch.Stack
	}
	if patch.Repo != nil {
		p.Repo = patch.Repo
	}
	if patch.ClearRepo {
		p.Repo = nil
	}
	if patch.Notes != nil {
		p.Notes = *patch.Notes
	}
	if patch.Instructions != nil {
		p.Instructions = *patch.Instructions
	}
	if err := l.write(file); err != nil {
		return Project{}, err
	}
	return *p, nil
}

// Delete removes a project from the ledger and rewrites it. The on-disk project
// directory is intentionally left untouched — deletion only forgets the record,
// so any code checked out under the project path is preserved. It errors if the
// id does not exist.
func (l *Ledger) Delete(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	file, err := l.read()
	if err != nil {
		return err
	}
	idx := -1
	for i := range file.Projects {
		if file.Projects[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("project %q not found", id)
	}
	file.Projects = append(file.Projects[:idx], file.Projects[idx+1:]...)
	return l.write(file)
}

// SyncRoadmaps replaces each project's derived roadmap with the ordered task
// ids currently known for that project. Unknown project IDs in byProject are
// ignored; projects without tasks get an empty roadmap.
func (l *Ledger) SyncRoadmaps(byProject map[string][]string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	file, err := l.read()
	if err != nil {
		return err
	}
	changed := false
	for i := range file.Projects {
		next := byProject[file.Projects[i].ID]
		if next == nil {
			next = []string{}
		}
		if !slices.Equal(file.Projects[i].Roadmap, next) {
			file.Projects[i].Roadmap = append([]string(nil), next...)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return l.write(file)
}

func (l *Ledger) read() (ledgerFile, error) {
	raw, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return ledgerFile{Projects: []Project{}}, nil
		}
		return ledgerFile{}, fmt.Errorf("read projects ledger: %w", err)
	}
	var file ledgerFile
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return ledgerFile{}, fmt.Errorf("parse projects.yaml: %w", err)
	}
	if file.Projects == nil {
		file.Projects = []Project{}
	}
	return file, nil
}

func (l *Ledger) write(file ledgerFile) error {
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return fmt.Errorf("create projects dir: %w", err)
	}
	raw, err := yaml.Marshal(file)
	if err != nil {
		return fmt.Errorf("marshal projects.yaml: %w", err)
	}
	if err := os.WriteFile(l.path, raw, 0o644); err != nil {
		return fmt.Errorf("write projects.yaml: %w", err)
	}
	return nil
}

func (p *Project) UnmarshalYAML(value *yaml.Node) error {
	type rawProject struct {
		ID           string    `yaml:"id"`
		Name         string    `yaml:"name"`
		Description  string    `yaml:"description"`
		Color        string    `yaml:"color,omitempty"`
		Path         string    `yaml:"path"`
		Status       string    `yaml:"status"`
		Stack        []string  `yaml:"stack"`
		Repo         yaml.Node `yaml:"repo"`
		Git          *Git      `yaml:"git"`
		Roadmap      []string  `yaml:"roadmap"`
		Notes        string    `yaml:"notes"`
		Instructions string    `yaml:"instructions"`
	}
	var raw rawProject
	if err := value.Decode(&raw); err != nil {
		return err
	}
	p.ID = raw.ID
	p.Name = raw.Name
	p.Description = raw.Description
	p.Color = raw.Color
	p.Path = raw.Path
	p.Status = raw.Status
	p.Stack = raw.Stack
	p.Roadmap = raw.Roadmap
	p.Notes = raw.Notes
	p.Instructions = raw.Instructions
	p.Repo = repoFromNode(raw.Repo)
	p.Git = normalizeGit(raw.Git)
	p.GitState = nil
	return nil
}

func repoFromNode(node yaml.Node) *Repo {
	if node.Kind == 0 || node.Tag == "!!null" {
		return nil
	}
	if node.Kind == yaml.ScalarNode {
		return legacyRepo(strings.TrimSpace(node.Value))
	}
	var repo Repo
	if err := node.Decode(&repo); err != nil {
		return nil
	}
	if repo.Provider == "" && repo.FullName == "" && repo.HTMLURL == "" {
		return nil
	}
	normalizeRepo(&repo)
	return &repo
}

func legacyRepo(value string) *Repo {
	if value == "" || value == "null" {
		return nil
	}
	trimmed := strings.TrimSuffix(value, ".git")
	const prefix = "https://github.com/"
	if !strings.HasPrefix(trimmed, prefix) {
		return &Repo{Provider: "github", Mode: "snapshot", HTMLURL: value, SourceKind: "archive"}
	}
	fullName := strings.Trim(strings.TrimPrefix(trimmed, prefix), "/")
	parts := strings.Split(fullName, "/")
	if len(parts) < 2 {
		return &Repo{Provider: "github", Mode: "snapshot", HTMLURL: value, SourceKind: "archive"}
	}
	repo := &Repo{
		Provider:   "github",
		Mode:       "snapshot",
		Owner:      parts[0],
		Name:       parts[1],
		FullName:   parts[0] + "/" + parts[1],
		HTMLURL:    prefix + parts[0] + "/" + parts[1],
		Ref:        "",
		SourceKind: "archive",
	}
	normalizeRepo(repo)
	return repo
}

// SnapshotRepo returns normalized metadata for a GitHub source snapshot.
func SnapshotRepo(owner, name, htmlURL, defaultBranch, ref string) Repo {
	repo := Repo{
		Provider:      "github",
		Mode:          "snapshot",
		Owner:         owner,
		Name:          name,
		FullName:      owner + "/" + name,
		HTMLURL:       htmlURL,
		DefaultBranch: defaultBranch,
		Ref:           ref,
		SyncedAt:      time.Now().UTC().Format(time.RFC3339),
		SourceKind:    "archive",
	}
	normalizeRepo(&repo)
	return repo
}

// CheckoutRepo returns normalized metadata for a real Git checkout obtained
// from GitHub rather than an archive snapshot.
func CheckoutRepo(owner, name, htmlURL, defaultBranch, ref string) Repo {
	repo := SnapshotRepo(owner, name, htmlURL, defaultBranch, ref)
	repo.Mode = "git"
	repo.SourceKind = "clone"
	return repo
}

func normalizeRepo(repo *Repo) {
	if repo.Provider == "" {
		repo.Provider = "github"
	}
	if repo.Mode == "" {
		repo.Mode = "snapshot"
	}
	if repo.FullName == "" && repo.Owner != "" && repo.Name != "" {
		repo.FullName = repo.Owner + "/" + repo.Name
	}
	if repo.Ref == "" {
		repo.Ref = repo.DefaultBranch
	}
	if repo.SourceKind == "" {
		repo.SourceKind = "archive"
	}
}
