package marketplace

import (
	"context"
	"fmt"
	"strings"
)

// fakeSource is a deterministic in-memory Source for server/handler tests
// (mirrors internal/adapter/fake.go). It never touches the network.
type fakeSource struct {
	id       RegistryID
	rows     []SkillSummary
	details  map[string]SkillDetail
	searchFn func(q string) ([]SkillSummary, error) // optional override
	warnings []string
}

func newFakeSource(id RegistryID) *fakeSource {
	return &fakeSource{id: id, details: map[string]SkillDetail{}}
}

func (f *fakeSource) ID() RegistryID { return f.id }

func (f *fakeSource) Search(ctx context.Context, q string, page int) ([]SkillSummary, error) {
	if f.searchFn != nil {
		return f.searchFn(q)
	}
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return f.rows, nil
	}
	var out []SkillSummary
	for _, r := range f.rows {
		if strings.Contains(strings.ToLower(r.Name), q) || strings.Contains(strings.ToLower(r.Description), q) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeSource) Fetch(ctx context.Context, id string) (SkillDetail, error) {
	d, ok := f.details[id]
	if !ok {
		return SkillDetail{}, fmt.Errorf("fake: no detail for %q", id)
	}
	return d, nil
}

// Warnings satisfies the aggregator's optional warning collector.
func (f *fakeSource) Warnings() []string {
	w := f.warnings
	f.warnings = nil
	return w
}

func (f *fakeSource) add(row SkillSummary, detail SkillDetail) {
	row.Registry = f.id
	f.rows = append(f.rows, row)
	detail.SkillSummary = row
	f.details[row.ID] = detail
}
