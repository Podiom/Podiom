package marketplace

import (
	"testing"
)

func TestDedupKey(t *testing.T) {
	tests := []struct {
		name     string
		ref      SkillRef
		expected string
	}{
		{
			name: "standard ref",
			ref: SkillRef{
				Owner: "Anthropic",
				Repo:  "Skills",
				Path:  "tools/web",
			},
			expected: "anthropic/skills/tools/web",
		},
		{
			name: "differing case for owner repo and path",
			ref: SkillRef{
				Owner: "ANTHROPIC",
				Repo:  "SKILLS",
				Path:  "TOOLS/WEB",
			},
			expected: "anthropic/skills/tools/web",
		},
		{
			name: "empty path",
			ref: SkillRef{
				Owner: "user",
				Repo:  "repo",
				Path:  "",
			},
			expected: "user/repo/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dedupKey(tt.ref)
			if got != tt.expected {
				t.Errorf("dedupKey(%+v) = %q; want %q", tt.ref, got, tt.expected)
			}
		})
	}
}
