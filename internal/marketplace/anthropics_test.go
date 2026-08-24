package marketplace

import (
	"reflect"
	"testing"
)

func TestLastSegment(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no slash at all",
			input:    "SKILL.md",
			expected: "SKILL.md",
		},
		{
			name:     "single segment with trailing slash",
			input:    "foo/",
			expected: "foo",
		},
		{
			name:     "nested path",
			input:    "a/b/SKILL.md",
			expected: "SKILL.md",
		},
		{
			name:     "leading slash",
			input:    "/a/SKILL.md",
			expected: "SKILL.md",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "multiple trailing and leading slashes",
			input:    "///a/b/c///",
			expected: "c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lastSegment(tt.input)
			if got != tt.expected {
				t.Errorf("lastSegment(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestParentDir(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "root-level file",
			input:    "SKILL.md",
			expected: "",
		},
		{
			name:     "one level deep",
			input:    "a/SKILL.md",
			expected: "a",
		},
		{
			name:     "multiple levels deep",
			input:    "a/b/c/SKILL.md",
			expected: "a/b/c",
		},
		{
			name:     "leading and trailing slashes trimmed",
			input:    "/a/b/SKILL.md/",
			expected: "a",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parentDir(tt.input)
			if got != tt.expected {
				t.Errorf("parentDir(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSkillDirsFromTree(t *testing.T) {
	tests := []struct {
		name     string
		nodes    []FileNode
		expected []string
	}{
		{
			name:     "empty input slice",
			nodes:    []FileNode{},
			expected: nil,
		},
		{
			name: "directory named SKILL.md skipped",
			nodes: []FileNode{
				{Path: "tools/SKILL.md", IsDir: true},
			},
			expected: nil,
		},
		{
			name: "file whose last segment is not SKILL.md skipped",
			nodes: []FileNode{
				{Path: "tools/README.md", IsDir: false},
				{Path: "tools/main.go", IsDir: false},
			},
			expected: nil,
		},
		{
			name: "root-level SKILL.md skipped",
			nodes: []FileNode{
				{Path: "SKILL.md", IsDir: false},
			},
			expected: nil,
		},
		{
			name: "duplicate SKILL.md in same directory deduped",
			nodes: []FileNode{
				{Path: "tools/web/SKILL.md", IsDir: false},
				{Path: "tools/web/SKILL.md", IsDir: false},
			},
			expected: []string{"tools/web"},
		},
		{
			name: "multiple distinct skill directories order preserved",
			nodes: []FileNode{
				{Path: "skills/summarize/SKILL.md", IsDir: false},
				{Path: "skills/code/SKILL.md", IsDir: false},
				{Path: "skills/search/SKILL.md", IsDir: false},
			},
			expected: []string{"skills/summarize", "skills/code", "skills/search"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := skillDirsFromTree(tt.nodes)
			if len(got) == 0 && len(tt.expected) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("skillDirsFromTree(%+v) = %v; want %v", tt.nodes, got, tt.expected)
			}
		})
	}
}
