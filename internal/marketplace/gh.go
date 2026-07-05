package marketplace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// tokenSource reports the current GitHub token, if any, so the fetcher can raise
// rate limits (API-3). It returns "" for anonymous access, which must keep
// working. It is a function so a freshly connected device-flow token is picked up
// without reconstructing the fetcher.
type tokenSource func() string

// ghFetcher performs the GitHub read operations every source shares: resolve a
// ref to a commit SHA (SEC-3), list a skill subtree, read a single file for
// inspection (FR-8), and download a subtree as a temp dir for install. It mirrors
// internal/github conventions (own client, X-GitHub-Api-Version header,
// io.LimitReader caps) but is anonymous-capable and subpath-aware.
type ghFetcher struct {
	apiBase string
	rawBase string
	client  *http.Client
	token   tokenSource
	maxSize int64 // download cap in bytes
}

func newGHFetcher(client *http.Client, token tokenSource, maxSize int64) *ghFetcher {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	if token == nil {
		token = func() string { return "" }
	}
	if maxSize <= 0 {
		maxSize = defaultMaxSkillBytes
	}
	return &ghFetcher{
		apiBase: "https://api.github.com",
		rawBase: "https://raw.githubusercontent.com",
		client:  client,
		token:   token,
		maxSize: maxSize,
	}
}

func (g *ghFetcher) do(ctx context.Context, method, endpoint string, out any) error {
	tok := g.token()
	res, err := g.githubRequest(ctx, method, endpoint, tok)
	if err != nil {
		return err
	}
	if res.StatusCode == http.StatusUnauthorized && tok != "" {
		res.Body.Close()
		res, err = g.githubRequest(ctx, method, endpoint, "")
		if err != nil {
			return err
		}
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return fmt.Errorf("github: not found (%s)", endpoint)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return fmt.Errorf("github request failed: %s: %s", res.Status, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func (g *ghFetcher) githubRequest(ctx context.Context, method, endpoint, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return g.client.Do(req)
}

// resolveSHA pins a ref (branch/tag/SHA) to a concrete commit SHA (SEC-3).
func (g *ghFetcher) resolveSHA(ctx context.Context, owner, repo, ref string) (string, error) {
	if ref == "" {
		ref = "HEAD"
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	u := fmt.Sprintf("%s/repos/%s/%s/commits/%s", g.apiBase,
		url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(ref))
	if err := g.do(ctx, http.MethodGet, u, &commit); err != nil {
		return "", err
	}
	if commit.SHA == "" {
		return "", fmt.Errorf("github: empty sha for %s/%s@%s", owner, repo, ref)
	}
	return commit.SHA, nil
}

// defaultBranch returns a repo's default branch, used when a URL omits a ref.
func (g *ghFetcher) defaultBranch(ctx context.Context, owner, repo string) (string, error) {
	var r struct {
		DefaultBranch string `json:"default_branch"`
	}
	u := fmt.Sprintf("%s/repos/%s/%s", g.apiBase, url.PathEscape(owner), url.PathEscape(repo))
	if err := g.do(ctx, http.MethodGet, u, &r); err != nil {
		return "", err
	}
	if r.DefaultBranch == "" {
		return "main", nil
	}
	return r.DefaultBranch, nil
}

type ghTreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"` // "blob" | "tree"
	Size int64  `json:"size"`
	Mode string `json:"mode"` // e.g. "100644", "100755", "120000" (symlink)
}

// tree lists the skill subtree (recursive), re-rooted so paths are relative to
// ref.Path. It builds the FileNode list for the detail view (FR-7) without
// downloading blobs. Uses the git trees API at the pinned SHA.
func (g *ghFetcher) tree(ctx context.Context, ref SkillRef) ([]FileNode, error) {
	sha := ref.SHA
	if sha == "" {
		s, err := g.resolveSHA(ctx, ref.Owner, ref.Repo, "HEAD")
		if err != nil {
			return nil, err
		}
		sha = s
	}
	var resp struct {
		Tree      []ghTreeEntry `json:"tree"`
		Truncated bool          `json:"truncated"`
	}
	u := fmt.Sprintf("%s/repos/%s/%s/git/trees/%s?recursive=1", g.apiBase,
		url.PathEscape(ref.Owner), url.PathEscape(ref.Repo), url.PathEscape(sha))
	if err := g.do(ctx, http.MethodGet, u, &resp); err != nil {
		return nil, err
	}
	sub := normalizeSubPath(ref.Path)
	var nodes []FileNode
	for _, e := range resp.Tree {
		rooted, ok := reRoot(e.Path, sub)
		if !ok || rooted == "" {
			continue
		}
		nodes = append(nodes, FileNode{
			Path:       rooted,
			IsDir:      e.Type == "tree",
			Size:       e.Size,
			Executable: e.Mode == "100755",
		})
	}
	return nodes, nil
}

// file fetches raw content of one skill-relative file for pre-install inspection
// (FR-8), capped by maxSize. It reads from raw.githubusercontent.com at the
// pinned SHA.
func (g *ghFetcher) file(ctx context.Context, ref SkillRef, relPath string) ([]byte, error) {
	sha := ref.SHA
	if sha == "" {
		return nil, fmt.Errorf("marketplace: file fetch requires a pinned SHA")
	}
	full := joinSkillPath(ref.Path, relPath)
	if full == "" {
		return nil, fmt.Errorf("marketplace: empty file path")
	}
	u := fmt.Sprintf("%s/%s/%s/%s/%s", g.rawBase,
		url.PathEscape(ref.Owner), url.PathEscape(ref.Repo), url.PathEscape(sha), encodePath(full))
	tok := g.token()
	res, err := g.rawRequest(ctx, u, tok)
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusUnauthorized && tok != "" {
		res.Body.Close()
		res, err = g.rawRequest(ctx, u, "")
		if err != nil {
			return nil, err
		}
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("github raw fetch failed: %s", res.Status)
	}
	return io.ReadAll(io.LimitReader(res.Body, g.maxSize))
}

func (g *ghFetcher) rawRequest(ctx context.Context, endpoint, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return g.client.Do(req)
}

// downloadZip fetches the repo zipball at the pinned SHA (SEC-3), capped by
// maxSize + a little headroom so the extractor can detect an oversize skill.
func (g *ghFetcher) downloadZip(ctx context.Context, ref SkillRef) ([]byte, error) {
	sha := ref.SHA
	if sha == "" {
		return nil, fmt.Errorf("marketplace: download requires a pinned SHA")
	}
	u := fmt.Sprintf("%s/repos/%s/%s/zipball/%s", g.apiBase,
		url.PathEscape(ref.Owner), url.PathEscape(ref.Repo), url.PathEscape(sha))
	tok := g.token()
	res, err := g.githubRequest(ctx, http.MethodGet, u, tok)
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusUnauthorized && tok != "" {
		res.Body.Close()
		res, err = g.githubRequest(ctx, http.MethodGet, u, "")
		if err != nil {
			return nil, err
		}
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("github zipball download failed: %s", res.Status)
	}
	// Whole-repo zipballs can exceed a single skill's cap; give generous headroom
	// (the extractor enforces the real per-skill cap on the subtree).
	return io.ReadAll(io.LimitReader(res.Body, 200*1024*1024))
}

func (g *ghFetcher) zipReader(b []byte) (*bytes.Reader, int64) {
	return bytes.NewReader(b), int64(len(b))
}

// joinSkillPath composes a repo-root-relative path from the skill's Path prefix
// and a skill-relative file path, cleaning traversal.
func joinSkillPath(skillPath, rel string) string {
	sub := normalizeSubPath(skillPath)
	rel = strings.Trim(strings.TrimSpace(rel), "/")
	joined := path.Clean(path.Join(sub, rel))
	if joined == "." || joined == ".." || strings.HasPrefix(joined, "../") {
		return ""
	}
	return joined
}

// encodePath percent-encodes each path segment while keeping the slashes.
func encodePath(p string) string {
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		parts[i] = url.PathEscape(seg)
	}
	return strings.Join(parts, "/")
}
