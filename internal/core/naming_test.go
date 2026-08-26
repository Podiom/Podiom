package core

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Podiom/Podiom/internal/store"
)

func TestFallbackName(t *testing.T) {
	tests := []struct {
		name               string
		history            []store.Message
		wantName           string
		wantDescription    string
		wantDescriptionHas string
		wantDescriptionMax int
	}{
		{
			name:            "empty history",
			wantName:        "Untitled Session",
			wantDescription: "Started with: ",
		},
		{
			name: "long user message",
			history: []store.Message{
				{Role: store.RoleUser, Content: "  one two three four five six seven!!!  "},
			},
			wantName:        "one two three four five six",
			wantDescription: "Started with: one two three four five six seven!!!",
		},
		{
			name: "user without assistant",
			history: []store.Message{
				{Role: store.RoleUser, Content: "Help me test naming"},
			},
			wantName:        "Help me test naming",
			wantDescription: "Started with: Help me test naming",
		},
		{
			name: "user and assistant capped at 140 runes",
			history: []store.Message{
				{Role: store.RoleUser, Content: "Summarize this session"},
				{Role: store.RoleAssistant, Content: strings.Repeat("yanıt ", 40)},
			},
			wantName:           "Summarize this session",
			wantDescriptionHas: "Started with: Summarize this session Response: ",
			wantDescriptionMax: 140,
		},
		{
			name: "non-conversation messages are skipped",
			history: []store.Message{
				{Role: store.RoleUser, Kind: store.KindError, Content: "ignored user"},
				{Role: store.RoleUser, Content: "real user"},
				{Role: store.RoleAssistant, Kind: store.KindReasoning, Content: "ignored reasoning"},
				{Role: store.RoleAssistant, Kind: store.KindNarration, Content: "ignored narration"},
				{Role: store.RoleAssistant, Content: "real assistant"},
			},
			wantName:        "real user",
			wantDescription: "Started with: real user Response: real assistant",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fallbackName(tt.history)
			if got.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", got.Name, tt.wantName)
			}
			if tt.wantDescription != "" && got.Description != tt.wantDescription {
				t.Errorf("Description = %q, want %q", got.Description, tt.wantDescription)
			}
			if tt.wantDescriptionHas != "" && !strings.HasPrefix(got.Description, tt.wantDescriptionHas) {
				t.Errorf("Description = %q, want prefix %q", got.Description, tt.wantDescriptionHas)
			}
			if tt.wantDescriptionMax != 0 && utf8.RuneCountInString(got.Description) != tt.wantDescriptionMax {
				t.Errorf("Description has %d runes, want %d", utf8.RuneCountInString(got.Description), tt.wantDescriptionMax)
			}
		})
	}
}

func TestParseNamingPayload(t *testing.T) {
	longDescription := strings.Repeat("ü", 150)
	tests := []struct {
		name            string
		raw             string
		want            namingPayload
		wantDescription int
	}{
		{
			name: "plain JSON",
			raw:  `{"name":"Test Session","description":"Test description"}`,
			want: namingPayload{Name: "Test Session", Description: "Test description"},
		},
		{
			name: "fenced JSON",
			raw:  "```json\n{\"name\":\"Fenced Session\",\"description\":\"From a code fence\"}\n```",
			want: namingPayload{Name: "Fenced Session", Description: "From a code fence"},
		},
		{
			name: "invalid JSON",
			raw:  `{"name":`,
			want: namingPayload{},
		},
		{
			name:            "fields are truncated",
			raw:             `{"name":"one two three four five six seven eight","description":"` + longDescription + `"}`,
			want:            namingPayload{Name: "one two three four five six"},
			wantDescription: 140,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseNamingPayload(tt.raw)
			if got.Name != tt.want.Name {
				t.Errorf("Name = %q, want %q", got.Name, tt.want.Name)
			}
			if tt.wantDescription == 0 && got.Description != tt.want.Description {
				t.Errorf("Description = %q, want %q", got.Description, tt.want.Description)
			}
			if tt.wantDescription != 0 {
				if utf8.RuneCountInString(got.Description) != tt.wantDescription {
					t.Errorf("Description has %d runes, want %d", utf8.RuneCountInString(got.Description), tt.wantDescription)
				}
				if !strings.HasSuffix(got.Description, "…") {
					t.Errorf("Description = %q, want truncated value ending in ellipsis", got.Description)
				}
			}
		})
	}
}
