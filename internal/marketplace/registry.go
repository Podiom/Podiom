package marketplace

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Service is the marketplace aggregator: it fans search out across enabled
// sources concurrently, merges and deduplicates by canonical GitHub location
// (SRC-2), degrades gracefully when a source errors or times out (SRC-3), caches
// responses (SRC-4), and owns install/uninstall/update against the shared skills
// pool.
type Service struct {
	sources   []Source
	byID      map[RegistryID]Source
	anthro    *anthropicsSource
	ghURL     *githubURLSource
	gh        *ghFetcher
	lock      *lockStore
	cache     *ttlCache
	searchTTL time.Duration
	detailTTL time.Duration
	curated   bool
	version   string
	log       *slog.Logger
}

// Options configures the Service. Everything non-secret comes from config; the
// SkillsMP API key is loaded here from env/file so it never travels through a
// public struct or the frontend (API-2 / NFR-5).
type Options struct {
	Client         *http.Client
	GitHubToken    tokenSource // connected-token accessor (API-3); may be nil
	MarketplaceDir string      // for the SkillsMP key file
	MaxSkillBytes  int64
	SearchTTL      time.Duration
	DetailTTL      time.Duration
	CuratedOnly    bool
	Registries     []string // enabled filter; empty = all
	Version        string
	Logger         *slog.Logger
	// Base-URL overrides. Empty selects the public GitHub / SkillsMP endpoints;
	// tests (and future GH-Enterprise support) point these at a mock server.
	GitHubAPIBase string
	GitHubRawBase string
	SkillsMPBase  string
}

// New constructs the marketplace Service. It is safe to construct even when the
// lockfile root is not yet created; the store is lazily written on first install.
func New(opts Options) (*Service, error) {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	maxBytes := opts.MaxSkillBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxSkillBytes
	}
	gh := newGHFetcher(opts.Client, opts.GitHubToken, maxBytes)
	if opts.GitHubAPIBase != "" {
		gh.apiBase = strings.TrimRight(opts.GitHubAPIBase, "/")
	}
	if opts.GitHubRawBase != "" {
		gh.rawBase = strings.TrimRight(opts.GitHubRawBase, "/")
	}
	lock, err := newLockStore()
	if err != nil {
		return nil, err
	}
	searchTTL := opts.SearchTTL
	if searchTTL <= 0 {
		searchTTL = defaultSearchTTLMinutes * time.Minute
	}
	detailTTL := opts.DetailTTL
	if detailTTL <= 0 {
		detailTTL = defaultDetailTTLHours * time.Hour
	}

	anthro := newAnthropicsSource(gh)
	ghURL := newGitHubURLSource(gh)
	skillsmp := newSkillsMPSource(opts.Client, loadSkillsMPKey(opts.MarketplaceDir), gh)
	if opts.SkillsMPBase != "" {
		skillsmp.base = strings.TrimRight(opts.SkillsMPBase, "/")
	}

	s := &Service{
		anthro:    anthro,
		ghURL:     ghURL,
		gh:        gh,
		lock:      lock,
		cache:     newTTLCache(),
		searchTTL: searchTTL,
		detailTTL: detailTTL,
		curated:   opts.CuratedOnly,
		version:   opts.Version,
		log:       log,
	}
	// Priority order (SRC-2: earlier wins metadata on dedup). SkillsMP is the
	// primary catalog; anthropics is Verified; the direct-URL source is not part
	// of merged search.
	all := []Source{skillsmp, anthro, ghURL}
	enabled := enabledSet(opts.Registries)
	s.byID = map[RegistryID]Source{}
	for _, src := range all {
		if enabled != nil && !enabled[string(src.ID())] {
			continue
		}
		s.byID[src.ID()] = src
		s.sources = append(s.sources, src)
	}
	return s, nil
}

// SearchResult is the merged search payload with per-source warnings (SRC-3).
type SearchResult struct {
	Results  []SkillSummary `json:"results"`
	Warnings []string       `json:"warnings"`
}

// Search fans out to enabled keyword sources concurrently and merges results. A
// slow or failing source never blocks the others (SRC-3): it contributes a
// warning instead. registry (optional) restricts to a single source; sort is one
// of relevance|popularity|recency (FR-4).
func (s *Service) Search(ctx context.Context, q, registry, sort string, page int) (SearchResult, error) {
	cacheKey := "search\x00" + q + "\x00" + registry + "\x00" + sort + "\x00" + itoa(page)
	if v, ok := s.cache.get(cacheKey); ok {
		return s.decorate(v.(SearchResult)), nil
	}

	sources := s.searchSources(registry)
	type outcome struct {
		rows     []SkillSummary
		warnings []string
	}
	results := make([]outcome, len(sources))
	var wg sync.WaitGroup
	for i, src := range sources {
		wg.Add(1)
		go func(i int, src Source) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			rows, err := src.Search(cctx, q, page)
			var warns []string
			if err != nil {
				warns = append(warns, fmt.Sprintf("%s is unavailable right now.", registryLabel(src.ID())))
				s.log.Warn("marketplace source search failed", "registry", src.ID(), "error", err)
			}
			if w, ok := src.(interface{ Warnings() []string }); ok {
				warns = append(warns, w.Warnings()...)
			}
			results[i] = outcome{rows: rows, warnings: warns}
		}(i, src)
	}
	wg.Wait()

	merged := map[string]SkillSummary{}
	var orderKeys []string
	var warnings []string
	for _, o := range results {
		warnings = append(warnings, o.warnings...)
		for _, row := range o.rows {
			key := dedupKey(row.Ref)
			if existing, ok := merged[key]; ok {
				merged[key] = mergeSummary(existing, row)
				continue
			}
			merged[key] = row
			orderKeys = append(orderKeys, key)
		}
	}
	rows := make([]SkillSummary, 0, len(orderKeys))
	for _, k := range orderKeys {
		rows = append(rows, merged[k])
	}
	sortSummaries(rows, sort)

	out := SearchResult{Results: rows, Warnings: warnings}
	s.cache.set(cacheKey, out, s.searchTTL)
	return s.decorate(out), nil
}

// searchSources returns the sources that participate in keyword/Featured search.
// The direct-URL source never participates. Curated-only mode limits to Verified
// sources (anthropics).
func (s *Service) searchSources(registry string) []Source {
	var out []Source
	for _, src := range s.sources {
		if src.ID() == RegistryGitHub {
			continue
		}
		if s.curated && src.ID() != RegistryAnthropics {
			continue
		}
		if registry != "" && registry != "all" && string(src.ID()) != registry {
			continue
		}
		out = append(out, src)
	}
	return out
}

// Detail resolves and caches the full inspection payload for a skill (FR-7).
func (s *Service) Detail(ctx context.Context, registry, id string) (SkillDetail, error) {
	cacheKey := "detail\x00" + registry + "\x00" + id
	if v, ok := s.cache.get(cacheKey); ok {
		return s.decorateDetail(v.(SkillDetail)), nil
	}
	src, err := s.source(registry)
	if err != nil {
		return SkillDetail{}, err
	}
	detail, err := src.Fetch(ctx, id)
	if err != nil {
		return SkillDetail{}, err
	}
	s.cache.set(cacheKey, detail, s.detailTTL)
	return s.decorateDetail(detail), nil
}

// File returns the raw content of one file within a skill for pre-install
// inspection (FR-8). It resolves the pinned SHA first so the bytes match exactly
// what would be installed.
func (s *Service) File(ctx context.Context, registry, id, path string) ([]byte, error) {
	ref, err := s.refFor(registry, id)
	if err != nil {
		return nil, err
	}
	if ref.SHA == "" {
		sha, err := s.gh.resolveSHA(ctx, ref.Owner, ref.Repo, "HEAD")
		if err != nil {
			return nil, err
		}
		ref.SHA = sha
	}
	return s.gh.file(ctx, ref, path)
}

// ResolveURL resolves a direct GitHub URL to its skill(s) (FR-22/23).
func (s *Service) ResolveURL(ctx context.Context, raw string) ([]SkillSummary, error) {
	rows, err := s.ghURL.Resolve(ctx, raw)
	if err != nil {
		return nil, err
	}
	return s.decorateRows(rows), nil
}

// source returns the source for a registry id, or an error.
func (s *Service) source(registry string) (Source, error) {
	if src, ok := s.byID[RegistryID(registry)]; ok {
		return src, nil
	}
	// Direct-URL detail may arrive under the github registry even if search
	// omitted it; fall back to the github source.
	if registry == string(RegistryGitHub) {
		return s.ghURL, nil
	}
	return nil, fmt.Errorf("unknown registry %q", registry)
}

// refFor derives a SkillRef from a registry id. All GitHub-backed sources encode
// owner/repo/path into the id, so this is a parse.
func (s *Service) refFor(registry, id string) (SkillRef, error) {
	switch RegistryID(registry) {
	case RegistryAnthropics:
		return refFromID(anthropicsOwner, anthropicsRepo, id), nil
	default:
		owner, repo, path, err := splitGitHubID(id)
		if err != nil {
			return SkillRef{}, err
		}
		return SkillRef{Owner: owner, Repo: repo, Path: path}, nil
	}
}

// decorate cross-references the lockfile to fill Installed/UpdateAvailable on
// each row (FR-20). Failures to read the lockfile are non-fatal.
func (s *Service) decorate(res SearchResult) SearchResult {
	res.Results = s.decorateRows(res.Results)
	if res.Warnings == nil {
		res.Warnings = []string{}
	}
	return res
}

func (s *Service) decorateRows(rows []SkillSummary) []SkillSummary {
	entries, err := s.lock.Entries()
	if err != nil {
		return rows
	}
	byKey := map[string]LockEntry{}
	for _, e := range entries {
		byKey[dedupKey(SkillRef{Owner: e.Owner, Repo: e.Repo, Path: e.Path})] = e
	}
	for i := range rows {
		if e, ok := byKey[dedupKey(rows[i].Ref)]; ok {
			rows[i].Installed = true
			rows[i].UpdateAvailable = rows[i].Ref.SHA != "" && rows[i].Ref.SHA != e.SHA
		}
	}
	return rows
}

func (s *Service) decorateDetail(d SkillDetail) SkillDetail {
	rows := s.decorateRows([]SkillSummary{d.SkillSummary})
	d.SkillSummary = rows[0]
	return d
}

// mergeSummary combines two rows for the same skill (SRC-2). The higher-priority
// row (already in the map) wins metadata; verified/script flags OR together and
// popularity signals take the max so nothing is lost.
func mergeSummary(keep, other SkillSummary) SkillSummary {
	keep.Verified = keep.Verified || other.Verified
	keep.HasScripts = keep.HasScripts || other.HasScripts
	if other.Stars > keep.Stars {
		keep.Stars = other.Stars
	}
	if other.Installs > keep.Installs {
		keep.Installs = other.Installs
	}
	if keep.Description == "" {
		keep.Description = other.Description
	}
	if keep.UpdatedAt == "" {
		keep.UpdatedAt = other.UpdatedAt
	}
	return keep
}

func sortSummaries(rows []SkillSummary, mode string) {
	switch mode {
	case "popularity":
		sort.SliceStable(rows, func(i, j int) bool {
			return (rows[i].Stars + rows[i].Installs) > (rows[j].Stars + rows[j].Installs)
		})
	case "recency":
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].UpdatedAt > rows[j].UpdatedAt })
	default: // relevance: keep merge order but float Verified up
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Verified && !rows[j].Verified })
	}
}

func enabledSet(list []string) map[string]bool {
	if len(list) == 0 {
		return nil
	}
	m := map[string]bool{}
	for _, v := range list {
		m[strings.TrimSpace(v)] = true
	}
	return m
}

func registryLabel(id RegistryID) string {
	switch id {
	case RegistrySkillsMP:
		return "SkillsMP"
	case RegistryAnthropics:
		return "anthropics/skills"
	case RegistryGitHub:
		return "GitHub"
	}
	return string(id)
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }
