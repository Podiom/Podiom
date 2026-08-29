package marketplace

import "testing"

func TestLastSegment(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "plain", path: "SKILL.md", want: "SKILL.md"},
		{name: "trailing slash", path: "foo/", want: "foo"},
		{name: "nested", path: "a/b/SKILL.md", want: "SKILL.md"},
		{name: "leading slash", path: "/a/SKILL.md", want: "SKILL.md"},
		{name: "empty", path: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lastSegment(tt.path); got != tt.want {
				t.Fatalf("lastSegment(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestParentDir(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "root", path: "SKILL.md", want: ""},
		{name: "one level", path: "a/SKILL.md", want: "a"},
		{name: "nested", path: "a/b/c/SKILL.md", want: "a/b/c"},
		{name: "trimmed slashes", path: "/a/b/SKILL.md/", want: "a/b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parentDir(tt.path); got != tt.want {
				t.Fatalf("parentDir(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestSkillDirsFromTree(t *testing.T) {
	tests := []struct {
		name  string
		nodes []FileNode
		want  []string
	}{
		{name: "directory named skill file is skipped", nodes: []FileNode{{Path: "foo/SKILL.md", IsDir: true}}, want: nil},
		{name: "non skill file is skipped", nodes: []FileNode{{Path: "foo/README.md"}}, want: nil},
		{name: "root skill file is skipped", nodes: []FileNode{{Path: "SKILL.md"}}, want: nil},
		{name: "same directory is deduplicated", nodes: []FileNode{{Path: "a/SKILL.md"}, {Path: "a/SKILL.md"}}, want: []string{"a"}},
		{name: "distinct directories preserve input order", nodes: []FileNode{{Path: "z/SKILL.md"}, {Path: "a/SKILL.md"}, {Path: "z/other/SKILL.md"}}, want: []string{"z", "a", "z/other"}},
		{name: "empty", nodes: []FileNode{}, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := skillDirsFromTree(tt.nodes)
			if len(got) != len(tt.want) {
				t.Fatalf("skillDirsFromTree() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("skillDirsFromTree()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
