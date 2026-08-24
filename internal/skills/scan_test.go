package skills

import "testing"

func TestParseFrontmatter(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantName string
		wantDesc string
	}{
		{
			name:     "no frontmatter",
			body:     "# Skill\n\nNo metadata here.",
			wantName: "",
			wantDesc: "",
		},
		{
			name:     "well formed",
			body:     "---\nname: example\ndescription: useful skill\n---\n\n# Example",
			wantName: "example",
			wantDesc: "useful skill",
		},
		{
			name:     "utf8 bom",
			body:     "\ufeff---\nname: bom-skill\ndescription: parses after BOM\n---\n",
			wantName: "bom-skill",
			wantDesc: "parses after BOM",
		},
		{
			name:     "leading whitespace",
			body:     "  \n\t---\nname: spaced\ndescription: leading whitespace is ignored\n---\n",
			wantName: "spaced",
			wantDesc: "leading whitespace is ignored",
		},
		{
			name:     "unterminated frontmatter",
			body:     "---\nname: unfinished\ndescription: missing closing delimiter\n",
			wantName: "",
			wantDesc: "",
		},
		{
			name:     "malformed yaml",
			body:     "---\nname: [unterminated\n---\n",
			wantName: "",
			wantDesc: "",
		},
		{
			name:     "name only",
			body:     "---\nname: only-name\n---\n",
			wantName: "only-name",
			wantDesc: "",
		},
		{
			name:     "description only",
			body:     "---\ndescription: only description\n---\n",
			wantName: "",
			wantDesc: "only description",
		},
		{
			name:     "trim values",
			body:     "---\nname: '  padded-name  '\ndescription: '  padded description  '\n---\n",
			wantName: "padded-name",
			wantDesc: "padded description",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotDesc := parseFrontmatter(tt.body)
			if gotName != tt.wantName || gotDesc != tt.wantDesc {
				t.Fatalf("parseFrontmatter() = (%q, %q), want (%q, %q)", gotName, gotDesc, tt.wantName, tt.wantDesc)
			}
		})
	}
}
