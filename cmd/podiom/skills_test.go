package main

import (
	"strings"
	"testing"
	"unicode/utf8"
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
