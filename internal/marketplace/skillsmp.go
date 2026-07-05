package marketplace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// skillsMPSource is the REST client for skillsmp.com — the primary catalog. It
// sends the API key header when configured and falls back to anonymous access.
// It surfaces a non-blocking warning as the daily quota nears zero and degrades
// gracefully on 429 / quota-exhausted (SRC-3). Metadata comes from the registry;
// files always come from GitHub at a pinned SHA (through buildDetail).
//
// NOTE: the exact SkillsMP wire shape may evolve; this maps a documented-style
// response defensively (owner/repo/path or a github_url fallback) so a field
// rename degrades to a skipped row rather than a hard failure.
type skillsMPSource struct {
	base   string
	apiKey string
	client *http.Client
	gh     *ghFetcher

	mu       sync.Mutex
	warnings []string
}

func newSkillsMPSource(client *http.Client, apiKey string, gh *ghFetcher) *skillsMPSource {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &skillsMPSource{base: "https://skillsmp.com", apiKey: apiKey, client: client, gh: gh}
}

func (s *skillsMPSource) ID() RegistryID { return RegistrySkillsMP }

// Warnings returns and clears any non-fatal warnings raised by the last Search
// (rate-limit pressure, quota exhaustion). The aggregator collects these.
func (s *skillsMPSource) Warnings() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.warnings
	s.warnings = nil
	return w
}

func (s *skillsMPSource) warn(msg string) {
	s.mu.Lock()
	s.warnings = append(s.warnings, msg)
	s.mu.Unlock()
}

type skillsMPResult struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	Owner           string `json:"owner"`
	Author          string `json:"author"`
	Repo            string `json:"repo"`
	Path            string `json:"path"`
	GitHubURL       string `json:"github_url"`
	GitHubURLCamel  string `json:"githubUrl"`
	Stars           int    `json:"stars"`
	Installs        int    `json:"installs"`
	UpdatedAt       string `json:"updated_at"`
	UpdatedAtCamel  string `json:"updatedAt"`
	HasScripts      bool   `json:"has_scripts"`
	HasScriptsCamel bool   `json:"hasScripts"`
	Verified        bool   `json:"verified"`
}

func (s *skillsMPSource) Search(ctx context.Context, q string, page int) ([]SkillSummary, error) {
	// SkillsMP has no wildcard search; an empty query returns nothing here (the
	// Featured view is served by anthropics/skills instead — FR-5).
	if strings.TrimSpace(q) == "" {
		return nil, nil
	}
	if page < 1 {
		page = 1
	}
	u := fmt.Sprintf("%s/api/v1/skills/search?q=%s&page=%d", strings.TrimRight(s.base, "/"), url.QueryEscape(q), page)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}
	res, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	s.checkRateLimit(res.Header)
	if res.StatusCode == http.StatusTooManyRequests {
		s.warn("SkillsMP rate limit reached; showing results from other sources.")
		return nil, nil // degrade gracefully, not a hard error
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return nil, fmt.Errorf("skillsmp search failed: %s: %s", res.Status, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Results []skillsMPResult `json:"results"`
		Data    struct {
			Skills []skillsMPResult `json:"skills"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("skillsmp decode: %w", err)
	}
	results := payload.Results
	if len(results) == 0 {
		results = payload.Data.Skills
	}
	var out []SkillSummary
	for _, r := range results {
		if sum, ok := s.toSummary(r); ok {
			out = append(out, sum)
		}
	}
	return out, nil
}

func (s *skillsMPSource) toSummary(r skillsMPResult) (SkillSummary, bool) {
	owner, repo, skillPath := firstNonEmpty(r.Owner, r.Author), r.Repo, strings.Trim(r.Path, "/")
	githubURL := firstNonEmpty(r.GitHubURL, r.GitHubURLCamel)
	if (owner == "" || repo == "" || skillPath == "") && githubURL != "" {
		if o, rp, _, sub, err := parseGitHubURL(githubURL); err == nil {
			owner, repo = o, rp
			if skillPath == "" {
				skillPath = strings.Trim(sub, "/")
			}
		}
	}
	if owner == "" || repo == "" {
		return SkillSummary{}, false // unusable without a canonical GitHub location
	}
	id := owner + "/" + repo
	if skillPath != "" {
		id += "/" + skillPath
	}
	return SkillSummary{
		ID:          id,
		Registry:    RegistrySkillsMP,
		Name:        firstNonEmpty(r.Name, kebab(lastSegment(skillPath)), repo),
		Description: r.Description,
		Owner:       owner,
		Ref:         SkillRef{Owner: owner, Repo: repo, Path: skillPath},
		Stars:       r.Stars,
		Installs:    r.Installs,
		UpdatedAt:   normalizeSkillsMPUpdatedAt(firstNonEmpty(r.UpdatedAt, r.UpdatedAtCamel)),
		HasScripts:  r.HasScripts || r.HasScriptsCamel,
		Verified:    r.Verified,
	}, true
}

func normalizeSkillsMPUpdatedAt(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.Unix(n, 0).UTC().Format(time.RFC3339)
	}
	return v
}

func (s *skillsMPSource) Fetch(ctx context.Context, id string) (SkillDetail, error) {
	owner, repo, path, err := splitGitHubID(id)
	if err != nil {
		return SkillDetail{}, err
	}
	ref := SkillRef{Owner: owner, Repo: repo, Path: path}
	base := SkillSummary{ID: id, Registry: RegistrySkillsMP, Owner: owner, Ref: ref}
	return buildDetail(ctx, s.gh, ref, base)
}

// checkRateLimit inspects the daily-remaining header and warns as quota nears
// zero, so the UI can surface pressure before the source starts failing.
func (s *skillsMPSource) checkRateLimit(h http.Header) {
	v := h.Get("X-RateLimit-Daily-Remaining")
	if v == "" {
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return
	}
	if n <= 0 {
		s.warn("SkillsMP daily quota exhausted; results may be incomplete.")
	} else if n <= 25 {
		s.warn(fmt.Sprintf("SkillsMP daily quota is low (%d requests remaining).", n))
	}
}
