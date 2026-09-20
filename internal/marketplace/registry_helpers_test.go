package marketplace

import (
	"reflect"
	"testing"
)

func TestSortSummaries(t *testing.T) {
	tests := []struct {
		mode string
		rows []SkillSummary
		want []string
	}{
		{"popularity", []SkillSummary{{ID: "installs", Installs: 100}, {ID: "stars", Stars: 90}, {ID: "tie", Stars: 90}}, []string{"installs", "stars", "tie"}},
		{"recency", []SkillSummary{{ID: "old", UpdatedAt: "2025-01-01"}, {ID: "none"}, {ID: "new", UpdatedAt: "2026-01-01"}}, []string{"new", "old", "none"}},
		{"relevance", []SkillSummary{{ID: "a"}, {ID: "b", Verified: true}, {ID: "c"}, {ID: "d", Verified: true}}, []string{"b", "d", "a", "c"}},
	}
	for _, tt := range tests {
		sortSummaries(tt.rows, tt.mode)
		got := make([]string, len(tt.rows))
		for i := range tt.rows {
			got[i] = tt.rows[i].ID
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("sortSummaries(%q) = %v, want %v", tt.mode, got, tt.want)
		}
	}
}

func TestEnabledSet(t *testing.T) {
	if enabledSet(nil) != nil || enabledSet([]string{}) != nil {
		t.Fatal("empty input must disable filtering")
	}
	got := enabledSet([]string{" skillsmp ", "skillsmp", " "})
	if len(got) != 2 || !got["skillsmp"] || !got[""] || got["github"] {
		t.Fatalf("enabledSet() = %#v", got)
	}
}

func TestRegistryLabel(t *testing.T) {
	tests := map[RegistryID]string{RegistrySkillsMP: "SkillsMP", RegistryAnthropics: "anthropics/skills", RegistryGitHub: "GitHub", "acme": "acme", "SkillsMP": "SkillsMP"}
	for id, want := range tests {
		if got := registryLabel(id); got != want {
			t.Errorf("registryLabel(%q) = %q, want %q", id, got, want)
		}
	}
}
