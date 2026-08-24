package marketplace

import (
	"testing"
)

func TestKebab(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "already kebab",
			input:    "my-skill",
			expected: "my-skill",
		},
		{
			name:     "mixed case and spaces",
			input:    "  My Skill  ",
			expected: "my-skill",
		},
		{
			name:     "runs of punctuation",
			input:    "a__b--c",
			expected: "a-b-c",
		},
		{
			name:     "leading and trailing non-alphanumerics",
			input:    "-foo-",
			expected: "foo",
		},
		{
			name:     "all punctuation",
			input:    "---___---",
			expected: "",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "unicode characters",
			input:    "Café Skill",
			expected: "caf-skill",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := kebab(tt.input)
			if got != tt.expected {
				t.Errorf("kebab(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}
