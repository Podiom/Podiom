package core

import (
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/store"
)

func TestFallbackName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		history     []store.Message
		wantName    string
		wantDescPfx string // prefix the description must start with
		wantDescHas string // substring the description must contain (optional)
		wantDescLen int    // if > 0, description rune length must be exactly this
		wantDescMax int    // if > 0, description rune length must be <= this
	}{
		{
			name:        "empty history returns untitled",
			history:     []store.Message{},
			wantName:    "Untitled Session",
			wantDescPfx: "Started with:",
		},
		{
			name: "short user message no assistant",
			history: []store.Message{
				{Role: store.RoleUser, Kind: store.KindMessage, Content: "hello world"},
			},
			wantName:    "hello world",
			wantDescPfx: "Started with: hello world",
		},
		{
			name: "long user message truncated to 6 words",
			history: []store.Message{
				{Role: store.RoleUser, Kind: store.KindMessage, Content: "one two three four five six seven eight"},
			},
			wantName:    "one two three four five six",
			wantDescPfx: "Started with:",
		},
		{
			name: "trailing punctuation stripped from name",
			history: []store.Message{
				{Role: store.RoleUser, Kind: store.KindMessage, Content: "fix the bug please!!!"},
			},
			wantName:    "fix the bug please",
			wantDescPfx: "Started with:",
		},
		{
			name: "user with trailing whitespace and newlines stripped",
			history: []store.Message{
				{Role: store.RoleUser, Kind: store.KindMessage, Content: "  deploy now  \n"},
			},
			wantName:    "deploy now",
			wantDescPfx: "Started with: deploy now",
		},
		{
			name: "user and assistant both present appends response",
			history: []store.Message{
				{Role: store.RoleUser, Kind: store.KindMessage, Content: "what is the plan"},
				{Role: store.RoleAssistant, Kind: store.KindMessage, Content: "the plan is X"},
			},
			wantName:    "what is the plan",
			wantDescPfx: "Started with:",
			wantDescHas: "Response:",
		},
		{
			name: "description capped at 140 runes with long messages",
			history: []store.Message{
				{Role: store.RoleUser, Kind: store.KindMessage, Content: strings.Repeat("a", 200)},
				{Role: store.RoleAssistant, Kind: store.KindMessage, Content: strings.Repeat("b", 200)},
			},
			// 200 'a' chars is one word, so truncateWords keeps it intact; name == strings.Repeat("a", 200)
			wantName:    strings.Repeat("a", 200),
			wantDescMax: 140,
		},
		{
			name: "non-conversation messages skipped, real ones picked correctly",
			history: []store.Message{
				{Role: store.RoleAssistant, Kind: store.KindReasoning, Content: "thinking..."},
				{Role: store.RoleAssistant, Kind: store.KindNarration, Content: "narrating..."},
				{Role: store.RoleUser, Kind: store.KindMessage, Content: "real user message"},
				{Role: store.RoleAssistant, Kind: store.KindMessage, Content: "real reply"},
			},
			wantName:    "real user message",
			wantDescPfx: "Started with: real user message",
			wantDescHas: "Response: real reply",
		},
		{
			name: "empty-kind messages treated as conversation messages",
			history: []store.Message{
				{Role: store.RoleUser, Content: "empty kind user"},
				{Role: store.RoleAssistant, Content: "empty kind assistant"},
			},
			wantName:    "empty kind user",
			wantDescHas: "Response:",
		},
		{
			name: "only non-conversation messages yields untitled",
			history: []store.Message{
				{Role: store.RoleAssistant, Kind: store.KindReasoning, Content: "thinking"},
				{Role: store.RoleAssistant, Kind: store.KindNarration, Content: "narrating"},
			},
			wantName:    "Untitled Session",
			wantDescPfx: "Started with:",
		},
		{
			name: "first user message is used even when preceded by non-conversation messages",
			history: []store.Message{
				{Role: store.RoleAssistant, Kind: store.KindReasoning, Content: "ignored"},
				{Role: store.RoleUser, Kind: store.KindMessage, Content: "first real user"},
				{Role: store.RoleUser, Kind: store.KindMessage, Content: "second user ignored"},
			},
			wantName:    "first real user",
			wantDescPfx: "Started with: first real user",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := fallbackName(tc.history)

			if got.Name != tc.wantName {
				t.Errorf("Name = %q, want %q", got.Name, tc.wantName)
			}
			if tc.wantDescPfx != "" && !strings.HasPrefix(got.Description, tc.wantDescPfx) {
				t.Errorf("Description %q does not start with %q", got.Description, tc.wantDescPfx)
			}
			if tc.wantDescHas != "" && !strings.Contains(got.Description, tc.wantDescHas) {
				t.Errorf("Description %q does not contain %q", got.Description, tc.wantDescHas)
			}
			if tc.wantDescMax > 0 {
				if n := len([]rune(got.Description)); n > tc.wantDescMax {
					t.Errorf("Description rune length = %d, want <= %d", n, tc.wantDescMax)
				}
			}
			if tc.wantDescLen > 0 {
				if n := len([]rune(got.Description)); n != tc.wantDescLen {
					t.Errorf("Description rune length = %d, want %d", n, tc.wantDescLen)
				}
			}
		})
	}
}

func TestParseNamingPayload(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		raw         string
		wantName    string
		wantDesc    string
		wantNameMax int // if > 0, name word count must be <= this
		wantDescMax int // if > 0, description rune length must be <= this
	}{
		{
			name:     "plain json no fence",
			raw:      `{"name":"fix the bug","description":"A simple fix"}`,
			wantName: "fix the bug",
			wantDesc: "A simple fix",
		},
		{
			name:     "json fenced with ```json",
			raw:      "```json\n{\"name\":\"deploy service\",\"description\":\"Deploy the service\"}\n```",
			wantName: "deploy service",
			wantDesc: "Deploy the service",
		},
		{
			name:     "json fenced with plain ```",
			raw:      "```\n{\"name\":\"review code\",\"description\":\"Code review session\"}\n```",
			wantName: "review code",
			wantDesc: "Code review session",
		},
		{
			name:     "invalid json returns zero value no panic",
			raw:      "this is not json at all",
			wantName: "",
			wantDesc: "",
		},
		{
			name:     "empty string returns zero value",
			raw:      "",
			wantName: "",
			wantDesc: "",
		},
		{
			name:     "json with extra whitespace trimmed",
			raw:      "   \n  {\"name\":\"trim spaces\",\"description\":\"Spaces trimmed\"}  \n  ",
			wantName: "trim spaces",
			wantDesc: "Spaces trimmed",
		},
		{
			name:        "name longer than 6 words gets truncated",
			raw:         `{"name":"one two three four five six seven eight","description":"short"}`,
			wantName:    "one two three four five six",
			wantDesc:    "short",
			wantNameMax: 6,
		},
		{
			name:        "description longer than 140 runes gets truncated",
			raw:         `{"name":"ok","description":"` + strings.Repeat("x", 200) + `"}`,
			wantName:    "ok",
			wantDescMax: 140,
		},
		{
			name:        "both name and description over limit are both truncated",
			raw:         `{"name":"` + strings.Repeat("word ", 10) + `","description":"` + strings.Repeat("c", 200) + `"}`,
			wantNameMax: 6,
			wantDescMax: 140,
		},
		{
			name:     "name with leading/trailing spaces is trimmed before truncation",
			raw:      `{"name":"  trimmed name  ","description":"desc"}`,
			wantName: "trimmed name",
			wantDesc: "desc",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := parseNamingPayload(tc.raw)

			if tc.wantName != "" && got.Name != tc.wantName {
				t.Errorf("Name = %q, want %q", got.Name, tc.wantName)
			}
			if tc.wantDesc != "" && got.Description != tc.wantDesc {
				t.Errorf("Description = %q, want %q", got.Description, tc.wantDesc)
			}
			// zero-value assertions
			if tc.wantName == "" && tc.wantNameMax == 0 && got.Name != "" {
				t.Errorf("Name = %q, want empty string", got.Name)
			}
			if tc.wantDesc == "" && tc.wantDescMax == 0 && got.Description != "" {
				t.Errorf("Description = %q, want empty string", got.Description)
			}
			if tc.wantNameMax > 0 {
				if n := len(strings.Fields(got.Name)); n > tc.wantNameMax {
					t.Errorf("Name word count = %d, want <= %d (got %q)", n, tc.wantNameMax, got.Name)
				}
			}
			if tc.wantDescMax > 0 {
				if n := len([]rune(got.Description)); n > tc.wantDescMax {
					t.Errorf("Description rune length = %d, want <= %d", n, tc.wantDescMax)
				}
			}
		})
	}
}
