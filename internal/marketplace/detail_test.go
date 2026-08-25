package marketplace

import (
	"os"
	"testing"
)

func TestIsExecutable(t *testing.T) {
	tests := []struct {
		name string
		rel  string
		mode os.FileMode
		want bool
	}{
		{"plain markdown", "README.md", 0o644, false},
		{"plain text", "docs/notes.txt", 0o644, false},
		{"json data", "data.json", 0o644, false},
		{"executable bit set", "bin/tool", 0o755, true},
		{"scripts prefix", "scripts/build.mk", 0o644, true},
		{"nested scripts dir", "skill/scripts/run.mk", 0o644, true},
		{"python extension", "helper.py", 0o644, true},
		{"shell extension", "install.sh", 0o644, true},
		{"powershell extension", "setup.ps1", 0o644, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isExecutable(tt.rel, tt.mode); got != tt.want {
				t.Fatalf("isExecutable(%q, %v) = %v, want %v", tt.rel, tt.mode, got, tt.want)
			}
		})
	}
}

func TestLicenseName(t *testing.T) {
	tests := []struct {
		rel  string
		want string
	}{
		{"LICENSE", "See LICENSE"},
		{"path/to/LICENSE.md", "See LICENSE.md"},
		{"COPYING", "See COPYING"},
		{"license.txt", "See license.txt"},
		{"README.md", ""},
		{"src/main.go", ""},
	}
	for _, tt := range tests {
		t.Run(tt.rel, func(t *testing.T) {
			if got := licenseName(tt.rel); got != tt.want {
				t.Fatalf("licenseName(%q) = %q, want %q", tt.rel, got, tt.want)
			}
		})
	}
}

func TestParseFrontmatterFields(t *testing.T) {
	body := "---\nname: hello\ndescription: A test skill\n---\n# Body\n"
	fields, name, desc := parseFrontmatterFields(body)
	if name != "hello" {
		t.Fatalf("name = %q, want hello", name)
	}
	if desc != "A test skill" {
		t.Fatalf("desc = %q, want %q", desc, "A test skill")
	}
	if len(fields) != 2 {
		t.Fatalf("len(fields) = %d, want 2", len(fields))
	}
	if fields[0].Key != "name" || fields[0].Value != "hello" {
		t.Fatalf("fields[0] = %+v, want name/hello", fields[0])
	}
	if fields[1].Key != "description" || fields[1].Value != "A test skill" {
		t.Fatalf("fields[1] = %+v, want description/A test skill", fields[1])
	}
}

func TestParseFrontmatterFieldsNoFrontmatter(t *testing.T) {
	fields, name, desc := parseFrontmatterFields("# Just a heading\n\nno frontmatter here")
	if fields != nil {
		t.Fatalf("fields = %+v, want nil when there is no frontmatter block", fields)
	}
	if name != "" || desc != "" {
		t.Fatalf("name/desc = %q/%q, want empty", name, desc)
	}
}
