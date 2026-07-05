package marketplace

import "testing"

func TestParseGitHubURL(t *testing.T) {
	cases := []struct {
		in                    string
		owner, repo, ref, sub string
		wantErr               bool
	}{
		{in: "https://github.com/anthropics/skills", owner: "anthropics", repo: "skills"},
		{in: "https://github.com/owner/repo/tree/main/skills/hello", owner: "owner", repo: "repo", ref: "main", sub: "skills/hello"},
		{in: "https://github.com/owner/repo/blob/abc123/skills/hello/SKILL.md", owner: "owner", repo: "repo", ref: "abc123", sub: "skills/hello/SKILL.md"},
		{in: "github.com/owner/repo", owner: "owner", repo: "repo"},
		{in: "owner/repo/skills/hello", owner: "owner", repo: "repo", sub: "skills/hello"},
		{in: "https://github.com/owner/repo.git", owner: "owner", repo: "repo"},
		{in: "https://gitlab.com/owner/repo", wantErr: true},
		{in: "https://github.com/owner", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, c := range cases {
		owner, repo, ref, sub, err := parseGitHubURL(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q: expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.in, err)
			continue
		}
		if owner != c.owner || repo != c.repo || ref != c.ref || sub != c.sub {
			t.Errorf("%q: got (%s,%s,%s,%s) want (%s,%s,%s,%s)", c.in, owner, repo, ref, sub, c.owner, c.repo, c.ref, c.sub)
		}
	}
}

func TestSplitGitHubID(t *testing.T) {
	owner, repo, path, err := splitGitHubID("owner/repo/skills/hello")
	if err != nil || owner != "owner" || repo != "repo" || path != "skills/hello" {
		t.Fatalf("got (%s,%s,%s) err=%v", owner, repo, path, err)
	}
	if _, _, _, err := splitGitHubID("owner"); err == nil {
		t.Fatalf("expected error for incomplete id")
	}
}

func TestKebab(t *testing.T) {
	cases := map[string]string{
		"Hello World":   "hello-world",
		"my_skill.name": "my-skill-name",
		"  Trim--Me  ":  "trim-me",
		"Café Skill":    "caf-skill",
	}
	for in, want := range cases {
		if got := kebab(in); got != want {
			t.Errorf("kebab(%q) = %q, want %q", in, got, want)
		}
	}
}
