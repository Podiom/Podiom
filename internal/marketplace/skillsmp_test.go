package marketplace

import (
	"testing"
)

func TestNormalizeSkillsMPUpdatedAt(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "whitespace only",
			input:    "   ",
			expected: "",
		},
		{
			name:     "valid unix timestamp",
			input:    "1700000000",
			expected: "2023-11-14T22:13:20Z",
		},
		{
			name:     "already formatted RFC3339 date",
			input:    "2026-01-01T00:00:00Z",
			expected: "2026-01-01T00:00:00Z",
		},
		{
			name:     "non-numeric string",
			input:    "invalid-date-string",
			expected: "invalid-date-string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeSkillsMPUpdatedAt(tt.input)
			if got != tt.expected {
				t.Errorf("normalizeSkillsMPUpdatedAt(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}
