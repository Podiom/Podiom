package core

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Podiom/Podiom/internal/store"
)

func TestDeterministicSummary(t *testing.T) {
	tests := []struct {
		name    string
		history []store.Message
		want    string
	}{
		{name: "empty"},
		{
			name: "filters and flattens messages",
			history: []store.Message{
				{Role: store.RoleUser, Content: "  hello\n\tworld  "},
				{Role: store.RoleAssistant, Kind: store.KindReasoning, Content: "hidden"},
				{Role: store.RoleAssistant, Kind: store.KindMessage, Content: "   "},
			},
			want: "user: hello world",
		},
		{
			name: "includes attachments",
			history: []store.Message{{
				Role: store.RoleUser, Content: "look", Attachments: []store.Attachment{{Name: "a.png"}, {Name: "b.png"}},
			}},
			want: "user: look [Attached photo: a.png] [Attached photo: b.png]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deterministicSummary(tt.history); got != tt.want {
				t.Fatalf("deterministicSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDeterministicSummaryTruncatesRunes(t *testing.T) {
	got := strings.TrimPrefix(deterministicSummary([]store.Message{{Role: store.RoleUser, Content: strings.Repeat("あ", 300)}}), "user: ")
	if utf8.RuneCountInString(got) != 220 || !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated summary = %q (%d runes)", got, utf8.RuneCountInString(got))
	}
}
