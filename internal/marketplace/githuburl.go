package marketplace

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// githubURLSource is the direct-URL escape hatch (FR-22/23). It does not
// participate in keyword search — Resolve turns a pasted GitHub URL into one or
// more inspectable skill summaries, which then follow the same detail → validate
// → install flow as any registry skill. Nothing here skips inspection.
type githubURLSource struct {
	gh *ghFetcher
}

func newGitHubURLSource(gh *ghFetcher) *githubURLSource {
	return &githubURLSource{gh: gh}
}

func (g *githubURLSource) ID() RegistryID { return RegistryGitHub }

// Search is intentionally empty: the direct-URL source is not part of merged
// keyword search (it resolves explicit URLs via Resolve).
func (g *githubURLSource) Search(ctx context.Context, q string, page int) ([]SkillSummary, error) {
	return nil, nil
}

func (g *githubURLSource) Fetch(ctx context.Context, id string) (SkillDetail, error) {
	owner, repo, path, err := splitGitHubID(id)
	if err != nil {
		return SkillDetail{}, err
	}
	ref := SkillRef{Owner: owner, Repo: repo, Path: path}
	base := SkillSummary{ID: id, Registry: RegistryGitHub, Owner: owner, Ref: ref}
	return buildDetail(ctx, g.gh, ref, base)
}

// Resolve parses a GitHub URL and returns the skill(s) it points at. A repo/URL
// with a single skill returns one row; a monorepo (a skills/ tree with several
// SKILL.md dirs) returns the list for the user to pick (FR-23).
func (g *githubURLSource) Resolve(ctx context.Context, raw string) ([]SkillSummary, error) {
	owner, repo, ref, sub, err := parseGitHubURL(raw)
	if err != nil {
		return nil, err
	}
	if ref == "" {
		ref = "HEAD"
	}
	sha, err := g.gh.resolveSHA(ctx, owner, repo, ref)
	if err != nil {
		return nil, fmt.Errorf("resolve %s/%s@%s: %w", owner, repo, ref, err)
	}
	// URL pointed straight at a SKILL.md → that directory is the skill root.
	if strings.EqualFold(lastSegment(sub), "SKILL.md") {
		sub = parentDir(sub)
		return []SkillSummary{g.summaryAt(ctx, owner, repo, sub, sha)}, nil
	}
	// List the subtree (or whole repo) and find every skill directory.
	nodes, err := g.gh.tree(ctx, SkillRef{Owner: owner, Repo: repo, Path: sub, SHA: sha})
	if err != nil {
		return nil, err
	}
	dirs := skillDirsFromTree(nodes)
	if len(dirs) == 0 {
		return nil, fmt.Errorf("no SKILL.md found at %s", raw)
	}
	var out []SkillSummary
	for _, d := range dirs {
		full := joinRel(sub, d)
		out = append(out, g.summaryAt(ctx, owner, repo, full, sha))
	}
	return out, nil
}

func (g *githubURLSource) summaryAt(ctx context.Context, owner, repo, sub, sha string) SkillSummary {
	ref := SkillRef{Owner: owner, Repo: repo, Path: strings.Trim(sub, "/"), SHA: sha}
	s := buildSummary(ctx, g.gh, ref, RegistryGitHub, false)
	// Encode owner/repo into the id so Fetch can locate it later.
	s.ID = owner + "/" + repo
	if p := strings.Trim(sub, "/"); p != "" {
		s.ID += "/" + p
	}
	return s
}

// parseGitHubURL accepts repo-root, /tree/<ref>/<subdir>, and /blob/<ref>/.../SKILL.md
// forms, plus bare "owner/repo" and "owner/repo/sub". It returns owner, repo,
// ref (may be ""), and the subpath (may be "").
func parseGitHubURL(raw string) (owner, repo, ref, sub string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", "", "", fmt.Errorf("empty GitHub URL")
	}
	var segs []string
	if strings.Contains(raw, "://") || strings.HasPrefix(raw, "github.com") {
		u, perr := url.Parse(normalizeScheme(raw))
		if perr != nil {
			return "", "", "", "", fmt.Errorf("invalid URL: %w", perr)
		}
		host := strings.ToLower(u.Host)
		if host != "github.com" && host != "www.github.com" {
			return "", "", "", "", fmt.Errorf("only github.com URLs are supported (got %q)", u.Host)
		}
		segs = splitPath(u.Path)
	} else {
		segs = splitPath(raw) // bare owner/repo[/sub]
	}
	if len(segs) < 2 {
		return "", "", "", "", fmt.Errorf("URL must include owner and repo")
	}
	owner, repo = segs[0], strings.TrimSuffix(segs[1], ".git")
	rest := segs[2:]
	if len(rest) == 0 {
		return owner, repo, "", "", nil
	}
	switch rest[0] {
	case "tree", "blob":
		if len(rest) < 2 {
			return owner, repo, "", "", nil
		}
		ref = rest[1]
		sub = strings.Join(rest[2:], "/")
	default:
		// bare owner/repo/sub form (no /tree/ref)
		sub = strings.Join(rest, "/")
	}
	return owner, repo, ref, sub, nil
}

// splitGitHubID splits an "owner/repo[/path...]" id.
func splitGitHubID(id string) (owner, repo, path string, err error) {
	segs := splitPath(id)
	if len(segs) < 2 {
		return "", "", "", fmt.Errorf("invalid github id %q (want owner/repo[/path])", id)
	}
	return segs[0], segs[1], strings.Join(segs[2:], "/"), nil
}

func normalizeScheme(raw string) string {
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	return "https://" + raw
}

func splitPath(p string) []string {
	var out []string
	for _, s := range strings.Split(strings.Trim(p, "/"), "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func joinRel(base, sub string) string {
	base = strings.Trim(base, "/")
	sub = strings.Trim(sub, "/")
	switch {
	case base == "":
		return sub
	case sub == "":
		return base
	default:
		return base + "/" + sub
	}
}
