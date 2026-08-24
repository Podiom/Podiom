package marketplace

import (
	"os"
	"reflect"
	"testing"
)

func TestIsExecutable(t *testing.T) {
	tests := []struct {
		name     string
		rel      string
		mode     os.FileMode
		expected bool
	}{
		{
			name:     "mode has execute bit set",
			rel:      "any/file.txt",
			mode:     0o755,
			expected: true,
		},
		{
			name:     "root scripts directory prefix",
			rel:      "scripts/run.txt",
			mode:     0o644,
			expected: true,
		},
		{
			name:     "nested scripts directory",
			rel:      "pkg/scripts/setup.data",
			mode:     0o644,
			expected: true,
		},
		{
			name:     "matching python script extension",
			rel:      "tools/helper.py",
			mode:     0o644,
			expected: true,
		},
		{
			name:     "mixed case shell extension",
			rel:      "install.SH",
			mode:     0o644,
			expected: true,
		},
		{
			name:     "powershell extension",
			rel:      "build.ps1",
			mode:     0o644,
			expected: true,
		},
		{
			name:     "plain non-executable text file",
			rel:      "README.md",
			mode:     0o644,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isExecutable(tt.rel, tt.mode)
			if got != tt.expected {
				t.Errorf("isExecutable(%q, %v) = %v; want %v", tt.rel, tt.mode, got, tt.expected)
			}
		})
	}
}

func TestLicenseName(t *testing.T) {
	tests := []struct {
		name     string
		rel      string
		expected string
	}{
		{
			name:     "standard root LICENSE",
			rel:      "LICENSE",
			expected: "See LICENSE",
		},
		{
			name:     "lowercase license.md",
			rel:      "license.md",
			expected: "See license.md",
		},
		{
			name:     "nested COPYING file",
			rel:      "vendor/COPYING",
			expected: "See COPYING",
		},
		{
			name:     "LICENSE.TXT",
			rel:      "LICENSE.TXT",
			expected: "See LICENSE.TXT",
		},
		{
			name:     "unrelated file",
			rel:      "CONTRIBUTING.md",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := licenseName(tt.rel)
			if got != tt.expected {
				t.Errorf("licenseName(%q) = %q; want %q", tt.rel, got, tt.expected)
			}
		})
	}
}

func TestParseFrontmatterFields(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantFields   []FrontmatterField
		wantName     string
		wantDesc     string
	}{
		{
			name:       "no leading dashes",
			body:       "# Just markdown header",
			wantFields: nil,
			wantName:   "",
			wantDesc:   "",
		},
		{
			name:       "unclosed frontmatter",
			body:       "---
name: my-skill
description: test",
			wantFields: nil,
			wantName:   "",
			wantDesc:   "",
		},
		{
			name:       "malformed yaml",
			body:       "---
[invalid yaml: : :
---
Body content",
			wantFields: nil,
			wantName:   "",
			wantDesc:   "",
		},
		{
			name: "valid frontmatter with utf-8 BOM",
			body: "\ufeff---\nname: Web Search\ndescription: Searches the web\n---\n# Content",
			wantFields: []FrontmatterField{
				{Key: "name", Value: "Web Search"},
				{Key: "description", Value: "Searches the web"},
			},
			wantName: "Web Search",
			wantDesc: "Searches the web",
		},
		{
			name: "case insensitive keys with list and map values",
			body: "---\nNAME: Code Analyzer\nDESCRIPTION: Analyzes code\ntags:\n  - go\n  - linter\nconfig:\n  enabled: true\n---\n# Content",
			wantFields: []FrontmatterField{
				{Key: "NAME", Value: "Code Analyzer"},
				{Key: "DESCRIPTION", Value: "Analyzes code"},
				{Key: "tags", Value: "go, linter"},
				{Key: "config", Value: "enabled: true"},
			},
			wantName: "Code Analyzer",
			wantDesc: "Analyzes code",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields, gotName, gotDesc := parseFrontmatterFields(tt.body)
			if gotName != tt.wantName {
				t.Errorf("parseFrontmatterFields() name = %q; want %q", gotName, tt.wantName)
			}
			if gotDesc != tt.wantDesc {
				t.Errorf("parseFrontmatterFields() desc = %q; want %q", gotDesc, tt.wantDesc)
			}
			if len(fields) == 0 && len(tt.wantFields) == 0 {
				return
			}
			if !reflect.DeepEqual(fields, tt.wantFields) {
				t.Errorf("parseFrontmatterFields() fields = %+v; want %+v", fields, tt.wantFields)
			}
		})
	}
}

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		name     string
		vals     []string
		expected string
	}{
		{
			name:     "all empty",
			vals:     []string{"", "   ", "\t\n"},
			expected: "",
		},
		{
			name:     "first populated",
			vals:     []string{"first", "second"},
			expected: "first",
		},
		{
			name:     "second populated with whitespace trimmed",
			vals:     []string{"", "  second  ", "third"},
			expected: "second",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstNonEmpty(tt.vals...)
			if got != tt.expected {
				t.Errorf("firstNonEmpty(%v) = %q; want %q", tt.vals, got, tt.expected)
			}
		})
	}
}
