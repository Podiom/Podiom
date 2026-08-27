package marketplace

import "testing"

func TestJoinRel(t *testing.T) {
	tests := []struct {
		name      string
		base, sub string
		want      string
	}{
		{name: "both non-empty", base: "a", sub: "b", want: "a/b"},
		{name: "empty base", sub: "b", want: "b"},
		{name: "empty sub", base: "a", want: "a"},
		{name: "both empty"},
		{name: "trim slashes", base: "/a/", sub: "/b/", want: "a/b"},
		{name: "both empty after trim", base: "/", sub: "/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := joinRel(tt.base, tt.sub); got != tt.want {
				t.Errorf("joinRel(%q, %q) = %q, want %q", tt.base, tt.sub, got, tt.want)
			}
		})
	}
}

func TestRefFromID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want SkillRef
	}{
		{name: "plain id", id: "hello", want: SkillRef{Owner: "owner", Repo: "repo", Path: "hello"}},
		{name: "trim slashes", id: "/a/b/", want: SkillRef{Owner: "owner", Repo: "repo", Path: "a/b"}},
		{name: "nested path", id: "skills/hello/SKILL.md", want: SkillRef{Owner: "owner", Repo: "repo", Path: "skills/hello/SKILL.md"}},
		{name: "empty id", want: SkillRef{Owner: "owner", Repo: "repo"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := refFromID("owner", "repo", tt.id); got != tt.want {
				t.Errorf("refFromID(%q, %q, %q) = %+v, want %+v", "owner", "repo", tt.id, got, tt.want)
			}
		})
	}
}
