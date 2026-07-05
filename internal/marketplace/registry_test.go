package marketplace

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/skills"
)

// withSources replaces a Service's sources with the given fakes (test-only).
func (s *Service) withSources(srcs ...Source) {
	s.sources = nil
	s.byID = map[RegistryID]Source{}
	for _, src := range srcs {
		s.sources = append(s.sources, src)
		s.byID[src.ID()] = src
	}
}

func newBareService(t *testing.T) *Service {
	t.Helper()
	t.Setenv(skills.EnvHome, t.TempDir())
	svc, err := New(Options{Version: "test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc
}

func TestSearch_MergesAndDedups(t *testing.T) {
	svc := newBareService(t)
	ref := SkillRef{Owner: "acme", Repo: "repo", Path: "skills/x"}
	a := newFakeSource(RegistrySkillsMP)
	a.rows = []SkillSummary{{ID: "acme/repo/skills/x", Registry: RegistrySkillsMP, Name: "X", Ref: ref, Stars: 10}}
	b := newFakeSource(RegistryAnthropics)
	b.rows = []SkillSummary{{ID: "acme/repo/skills/x", Registry: RegistryAnthropics, Name: "X", Ref: ref, Verified: true}}
	svc.withSources(a, b)

	res, err := svc.Search(context.Background(), "x", "", "", 1)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Results) != 1 {
		t.Fatalf("expected 1 deduped result, got %d", len(res.Results))
	}
	row := res.Results[0]
	// Higher-priority source (skillsmp, listed first) wins metadata, but the
	// verified flag from the anthropics duplicate is preserved.
	if row.Registry != RegistrySkillsMP || !row.Verified || row.Stars != 10 {
		t.Fatalf("bad merge: %+v", row)
	}
}

func TestSearch_DegradesGracefully(t *testing.T) {
	svc := newBareService(t)
	good := newFakeSource(RegistrySkillsMP)
	good.rows = []SkillSummary{{ID: "o/r/a", Registry: RegistrySkillsMP, Name: "Good", Ref: SkillRef{Owner: "o", Repo: "r", Path: "a"}}}
	bad := newFakeSource(RegistryAnthropics)
	bad.searchFn = func(q string) ([]SkillSummary, error) { return nil, fmt.Errorf("boom") }
	svc.withSources(good, bad)

	res, err := svc.Search(context.Background(), "good", "", "", 1)
	if err != nil {
		t.Fatalf("search should not hard-fail: %v", err)
	}
	if len(res.Results) != 1 {
		t.Fatalf("expected the good source's result, got %d", len(res.Results))
	}
	if len(res.Warnings) == 0 {
		t.Fatalf("expected a degradation warning")
	}
}

func TestSearch_CuratedOnlyRestrictsSources(t *testing.T) {
	svc := newBareService(t)
	svc.curated = true
	sk := newFakeSource(RegistrySkillsMP)
	sk.rows = []SkillSummary{{ID: "o/r/a", Registry: RegistrySkillsMP, Name: "Uncurated", Ref: SkillRef{Owner: "o", Repo: "r", Path: "a"}}}
	an := newFakeSource(RegistryAnthropics)
	an.rows = []SkillSummary{{ID: "o/r/b", Registry: RegistryAnthropics, Name: "Curated", Verified: true, Ref: SkillRef{Owner: "o", Repo: "r", Path: "b"}}}
	svc.withSources(sk, an)

	res, _ := svc.Search(context.Background(), "c", "", "", 1)
	if len(res.Results) != 1 || res.Results[0].Name != "Curated" {
		t.Fatalf("curated-only should return only anthropics, got %+v", res.Results)
	}
}

func TestSkillsMP_RateLimitWarning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Daily-Remaining", "5")
		_, _ = w.Write([]byte(`{"results":[{"name":"n","owner":"o","repo":"r","path":"p"}]}`))
	}))
	defer srv.Close()

	src := newSkillsMPSource(nil, "", nil)
	src.base = srv.URL
	rows, err := src.Search(context.Background(), "hello", 1)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	warnings := src.Warnings()
	if len(warnings) == 0 || !strings.Contains(warnings[0], "quota is low") {
		t.Fatalf("expected low-quota warning, got %v", warnings)
	}
}

func TestSkillsMP_LiveResponseShapeAndBearerAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk_test" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		_, _ = w.Write([]byte(`{
			"success": true,
			"data": {
				"skills": [{
					"id": "nexu-io-open-design-skills-pdf-skill-md",
					"name": "pdf",
					"author": "nexu-io",
					"description": "Extract text, create PDFs, and handle forms.",
					"githubUrl": "https://github.com/nexu-io/open-design/tree/main/skills/pdf",
					"stars": 73412,
					"updatedAt": "1780545507"
				}]
			}
		}`))
	}))
	defer srv.Close()

	src := newSkillsMPSource(nil, "sk_test", nil)
	src.base = srv.URL
	rows, err := src.Search(context.Background(), "pdf", 1)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0]
	if row.ID != "nexu-io/open-design/skills/pdf" || row.Owner != "nexu-io" || row.Ref.Path != "skills/pdf" {
		t.Fatalf("bad github mapping: %+v", row)
	}
	if row.UpdatedAt != "2026-06-04T03:58:27Z" {
		t.Fatalf("updated_at = %q", row.UpdatedAt)
	}
}

func TestSkillsMP_EmptyQueryReturnsNothing(t *testing.T) {
	src := newSkillsMPSource(nil, "", nil)
	rows, err := src.Search(context.Background(), "  ", 1)
	if err != nil || rows != nil {
		t.Fatalf("empty query should return nil,nil; got %v %v", rows, err)
	}
}

func TestGitHubFetcher_FallsBackToAnonymousOnBadToken(t *testing.T) {
	gh := newMockGitHub(t, helloRepo())
	svc, err := New(Options{
		GitHubAPIBase: gh.URL,
		GitHubRawBase: gh.URL,
		GitHubToken:   func() string { return "bad-token" },
		Version:       "test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rows, err := svc.ResolveURL(context.Background(), "https://github.com/acme/skills/tree/main/skills/hello")
	if err != nil {
		t.Fatalf("resolve should retry anonymously after bad token: %v", err)
	}
	if len(rows) != 1 || rows[0].Ref.SHA != "deadbeef" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}
