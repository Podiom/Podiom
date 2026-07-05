package marketplace

import (
	"context"
	"strings"
)

// Source is one search backend (SRC-1). Every source resolves a skill to a
// canonical GitHub location; the actual tree/file/download work is shared through
// the ghFetcher, so metadata comes from the registry but files always come from
// GitHub at a pinned SHA.
type Source interface {
	// ID identifies the registry this source represents.
	ID() RegistryID
	// Search returns result rows for a free-text query. An empty query is a
	// source's discretion (SkillsMP has no wildcard; anthropics serves Featured).
	Search(ctx context.Context, q string, page int) ([]SkillSummary, error)
	// Fetch resolves the canonical GitHub location for a registry-scoped id and
	// builds the full inspection detail (through buildDetail).
	Fetch(ctx context.Context, id string) (SkillDetail, error)
}

// dedupKey is the SRC-2 identity: owner/repo/path lowercased.
func dedupKey(ref SkillRef) string {
	return strings.ToLower(ref.Owner) + "/" + strings.ToLower(ref.Repo) + "/" + strings.ToLower(normalizeSubPath(ref.Path))
}

// buildSummary reads a skill's SKILL.md frontmatter (at the pinned SHA in ref)
// and assembles a search-row summary. Name falls back to the directory basename
// when frontmatter has no name. Errors reading SKILL.md are non-fatal — the row
// still renders with a derived name.
func buildSummary(ctx context.Context, gh *ghFetcher, ref SkillRef, registry RegistryID, verified bool) SkillSummary {
	s := SkillSummary{
		ID:       strings.Trim(ref.Path, "/"),
		Registry: registry,
		Name:     kebab(lastSegment(ref.Path)),
		Owner:    ref.Owner,
		Ref:      ref,
		Verified: verified,
	}
	if s.ID == "" {
		s.ID = ref.Owner + "/" + ref.Repo
	}
	if raw, err := gh.file(ctx, ref, "SKILL.md"); err == nil {
		_, name, desc := parseFrontmatterFields(string(raw))
		if name != "" {
			s.Name = name
		}
		s.Description = desc
	}
	return s
}
