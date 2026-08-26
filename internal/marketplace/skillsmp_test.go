package marketplace

import "testing"

func TestNormalizeSkillsMPUpdatedAt(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"unix timestamp", "1700000000", "2023-11-14T22:13:20Z"},
		{"negative timestamp", "-1", "1969-12-31T23:59:59Z"},
		{"already formatted date", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"},
		{"out of int64 range", "99999999999999999999999", "99999999999999999999999"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeSkillsMPUpdatedAt(tt.in); got != tt.want {
				t.Fatalf("normalizeSkillsMPUpdatedAt(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
