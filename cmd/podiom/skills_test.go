package main

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Podiom/Podiom/internal/skills"
)

func TestOneLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "whitespace only", in: " \t\n\u2003", want: ""},
		{name: "short", in: "A short description", want: "A short description"},
		{name: "short unicode", in: "技能 café 🚀", want: "技能 café 🚀"},
		{name: "collapse whitespace", in: " \tfirst  second\nthird\u2003fourth \n", want: "first second third fourth"},
		{name: "at limit", in: strings.Repeat("a", 96), want: strings.Repeat("a", 96)},
		{name: "past limit", in: strings.Repeat("a", 97), want: strings.Repeat("a", 95) + "…"},
		{name: "normalize before limit", in: strings.Repeat("a", 96) + " \t\n", want: strings.Repeat("a", 96)},
		{name: "accented at limit", in: strings.Repeat("é", 96), want: strings.Repeat("é", 96)},
		{name: "emoji at limit", in: strings.Repeat("🚀", 96), want: strings.Repeat("🚀", 96)},
		{name: "accented truncation boundary", in: strings.Repeat("a", 94) + "ééé", want: strings.Repeat("a", 94) + "é…"},
		{name: "emoji truncation boundary", in: strings.Repeat("a", 94) + "🚀🚀🚀", want: strings.Repeat("a", 94) + "🚀…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := oneLine(tt.in)
			if !utf8.ValidString(got) {
				t.Fatalf("oneLine(%q) produced invalid UTF-8: %q", tt.in, got)
			}
			if got != tt.want {
				t.Fatalf("oneLine(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestValidSource(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "empty source allowed",
			src:     "",
			wantErr: false,
		},
		{
			name:    "agents source allowed",
			src:     "agents",
			wantErr: false,
		},
		{
			name:    "claude source allowed",
			src:     "claude",
			wantErr: false,
		},
		{
			name:    "codex source allowed",
			src:     "codex",
			wantErr: false,
		},
		{
			name:    "case sensitive rejection",
			src:     "Claude",
			wantErr: true,
			errMsg:  `invalid --source "Claude": use agents, claude, or codex`,
		},
		{
			name:    "uppercase rejection",
			src:     "AGENTS",
			wantErr: true,
			errMsg:  `invalid --source "AGENTS": use agents, claude, or codex`,
		},
		{
			name:    "unknown source",
			src:     "openai",
			wantErr: true,
			errMsg:  `invalid --source "openai": use agents, claude, or codex`,
		},
		{
			name:    "whitespace rejection",
			src:     " claude ",
			wantErr: true,
			errMsg:  `invalid --source " claude ": use agents, claude, or codex`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validSource(tt.src)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validSource(%q) error = %v, wantErr %v", tt.src, err, tt.wantErr)
			}
			if tt.wantErr && err.Error() != tt.errMsg {
				t.Fatalf("validSource(%q) error message = %q, want %q", tt.src, err.Error(), tt.errMsg)
			}
		})
	}
}

func TestHasSource(t *testing.T) {
	tests := []struct {
		name   string
		skill  skills.Skill
		target skills.Source
		want   bool
	}{
		{
			name:   "empty sources slice",
			skill:  skills.Skill{Sources: nil},
			target: skills.SourceClaude,
			want:   false,
		},
		{
			name:   "empty non-nil sources slice",
			skill:  skills.Skill{Sources: []skills.Source{}},
			target: skills.SourceAgents,
			want:   false,
		},
		{
			name:   "single matching source",
			skill:  skills.Skill{Sources: []skills.Source{skills.SourceClaude}},
			target: skills.SourceClaude,
			want:   true,
		},
		{
			name:   "single non-matching source",
			skill:  skills.Skill{Sources: []skills.Source{skills.SourceClaude}},
			target: skills.SourceCodex,
			want:   false,
		},
		{
			name: "match is the last element in multi-source skill",
			skill: skills.Skill{
				Sources: []skills.Source{skills.SourceAgents, skills.SourceClaude, skills.SourceCodex},
			},
			target: skills.SourceCodex,
			want:   true,
		},
		{
			name: "match is first element in multi-source skill",
			skill: skills.Skill{
				Sources: []skills.Source{skills.SourceAgents, skills.SourceClaude, skills.SourceCodex},
			},
			target: skills.SourceAgents,
			want:   true,
		},
		{
			name: "absent from multi-source skill",
			skill: skills.Skill{
				Sources: []skills.Source{skills.SourceAgents, skills.SourceClaude},
			},
			target: skills.SourceCodex,
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasSource(tt.skill, tt.target)
			if got != tt.want {
				t.Fatalf("hasSource(%v, %q) = %v, want %v", tt.skill.Sources, tt.target, got, tt.want)
			}
		})
	}
}

func TestBadges(t *testing.T) {
	tests := []struct {
		name    string
		sources []skills.Source
		want    string
	}{
		{
			name:    "empty nil slice returns empty string",
			sources: nil,
			want:    "",
		},
		{
			name:    "empty non-nil slice returns empty string",
			sources: []skills.Source{},
			want:    "",
		},
		{
			name:    "single source",
			sources: []skills.Source{skills.SourceAgents},
			want:    "agents",
		},
		{
			name:    "several sources preserves order",
			sources: []skills.Source{skills.SourceAgents, skills.SourceClaude, skills.SourceCodex},
			want:    "agents,claude,codex",
		},
		{
			name:    "different ordering preserved",
			sources: []skills.Source{skills.SourceCodex, skills.SourceClaude},
			want:    "codex,claude",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := badges(tt.sources)
			if got != tt.want {
				t.Fatalf("badges(%v) = %q, want %q", tt.sources, got, tt.want)
			}
		})
	}
}
