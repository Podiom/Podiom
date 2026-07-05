package marketplace

import (
	"context"
	"strings"
	"sync"
	"time"
)

const (
	anthropicsOwner = "anthropics"
	anthropicsRepo  = "skills"
)

// anthropicsSource is a GitHub-backed source over the curated anthropics/skills
// repo. Every skill it returns is flagged Verified. It powers the Featured view
// on an empty query (FR-5). The repo tree is small, so it lists every directory
// containing a SKILL.md and reads each manifest's frontmatter for name/desc.
type anthropicsSource struct {
	gh *ghFetcher

	mu       sync.Mutex
	cached   []SkillSummary
	cachedAt time.Time
	ttl      time.Duration
}

func newAnthropicsSource(gh *ghFetcher) *anthropicsSource {
	return &anthropicsSource{gh: gh, ttl: 30 * time.Minute}
}

func (a *anthropicsSource) ID() RegistryID { return RegistryAnthropics }

func (a *anthropicsSource) Search(ctx context.Context, q string, page int) ([]SkillSummary, error) {
	all, err := a.list(ctx)
	if err != nil {
		return nil, err
	}
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return all, nil // Featured
	}
	var out []SkillSummary
	for _, s := range all {
		if strings.Contains(strings.ToLower(s.Name), q) || strings.Contains(strings.ToLower(s.Description), q) {
			out = append(out, s)
		}
	}
	return out, nil
}

func (a *anthropicsSource) Fetch(ctx context.Context, id string) (SkillDetail, error) {
	ref := refFromID(anthropicsOwner, anthropicsRepo, id)
	base := SkillSummary{
		ID:       id,
		Registry: RegistryAnthropics,
		Owner:    anthropicsOwner,
		Ref:      ref,
		Verified: true,
	}
	// Enrich from the cached list where available (stars/updated/name).
	if all, err := a.list(ctx); err == nil {
		for _, s := range all {
			if s.ID == id {
				base = s
				break
			}
		}
	}
	return buildDetail(ctx, a.gh, ref, base)
}

// list discovers every skill directory in anthropics/skills and reads each
// SKILL.md's frontmatter. Results are memoized for ttl to respect rate limits.
func (a *anthropicsSource) list(ctx context.Context) ([]SkillSummary, error) {
	a.mu.Lock()
	if a.cached != nil && time.Since(a.cachedAt) < a.ttl {
		out := a.cached
		a.mu.Unlock()
		return out, nil
	}
	a.mu.Unlock()

	sha, err := a.gh.resolveSHA(ctx, anthropicsOwner, anthropicsRepo, "HEAD")
	if err != nil {
		return nil, err
	}
	nodes, err := a.gh.tree(ctx, SkillRef{Owner: anthropicsOwner, Repo: anthropicsRepo, SHA: sha})
	if err != nil {
		return nil, err
	}
	dirs := skillDirsFromTree(nodes)
	summaries := a.enrich(ctx, sha, dirs)

	a.mu.Lock()
	a.cached = summaries
	a.cachedAt = time.Now()
	a.mu.Unlock()
	return summaries, nil
}

// enrich fetches each skill's SKILL.md frontmatter concurrently (bounded).
func (a *anthropicsSource) enrich(ctx context.Context, sha string, dirs []string) []SkillSummary {
	out := make([]SkillSummary, len(dirs))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for i, dir := range dirs {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, dir string) {
			defer wg.Done()
			defer func() { <-sem }()
			ref := SkillRef{Owner: anthropicsOwner, Repo: anthropicsRepo, Path: dir, SHA: sha}
			s := SkillSummary{
				ID:       dir,
				Registry: RegistryAnthropics,
				Name:     kebab(lastSegment(dir)),
				Owner:    anthropicsOwner,
				Ref:      ref,
				Verified: true,
			}
			if raw, err := a.gh.file(ctx, ref, "SKILL.md"); err == nil {
				_, name, desc := parseFrontmatterFields(string(raw))
				if name != "" {
					s.Name = name
				}
				s.Description = desc
			}
			out[i] = s
		}(i, dir)
	}
	wg.Wait()
	return out
}

// skillDirsFromTree returns the set of directories that directly contain a
// SKILL.md, from a re-rooted (repo-relative) tree listing.
func skillDirsFromTree(nodes []FileNode) []string {
	seen := map[string]bool{}
	var dirs []string
	for _, n := range nodes {
		if n.IsDir || lastSegment(n.Path) != "SKILL.md" {
			continue
		}
		dir := parentDir(n.Path)
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		dirs = append(dirs, dir)
	}
	return dirs
}

func lastSegment(p string) string {
	p = strings.Trim(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func parentDir(p string) string {
	p = strings.Trim(p, "/")
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return "" // SKILL.md at repo root
	}
	return p[:i]
}

// refFromID turns an "owner/repo/path" or "path" id into a SkillRef for a known
// owner/repo.
func refFromID(owner, repo, id string) SkillRef {
	return SkillRef{Owner: owner, Repo: repo, Path: strings.Trim(id, "/")}
}
