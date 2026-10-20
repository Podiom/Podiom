package onboard

import (
	"reflect"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	podiomgit "github.com/Podiom/Podiom/internal/git"
	"github.com/Podiom/Podiom/internal/providercheck"
)

func TestHasFoundProvider(t *testing.T) {
	tests := []struct {
		name     string
		statuses []providercheck.Status
		want     bool
	}{
		{name: "empty", statuses: nil, want: false},
		{name: "none found", statuses: []providercheck.Status{{Provider: config.ProviderClaude}, {Provider: config.ProviderCodex}}, want: false},
		{name: "one found", statuses: []providercheck.Status{{Provider: config.ProviderClaude}, {Provider: config.ProviderCodex, Found: true}}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasFoundProvider(tt.statuses); got != tt.want {
				t.Fatalf("hasFoundProvider() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProviderChoicesFilterProfilesAndPutDefaultsFirst(t *testing.T) {
	choices := providerChoices(
		[]config.Provider{config.ProviderClaude, config.ProviderCodex},
		[]config.Profile{
			{Name: "ignored", Provider: "unknown"},
			{Name: "codex-work", Provider: config.ProviderCodex},
			{Name: "claude-work", Provider: config.ProviderClaude},
		},
	)
	if len(choices) != 4 {
		t.Fatalf("providerChoices() returned %d choices, want 4: %#v", len(choices), choices)
	}
	if choices[0].Label != "Claude (default)" || choices[1].Label != "Codex (default)" {
		t.Fatalf("default choices should come first, got %#v", choices[:2])
	}
	if choices[0].Profile != "" || choices[1].Profile != "" {
		t.Fatalf("default choices must not select named profiles: %#v", choices[:2])
	}
	if choices[2].Label != "Codex profile: codex-work" || choices[3].Label != "Claude profile: claude-work" {
		t.Fatalf("profile choices are missing, reordered, or include an unsupported provider: %#v", choices[2:])
	}
	for _, choice := range choices {
		if choice.Profile == "ignored" {
			t.Fatalf("included profile whose provider was not available: %#v", choice)
		}
	}
}

func TestProviderChoicesNilInputsReturnNonNilEmptySlice(t *testing.T) {
	got := providerChoices(nil, nil)
	if got == nil {
		t.Fatal("providerChoices(nil, nil) returned nil, want a non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("providerChoices(nil, nil) returned %d choices, want 0", len(got))
	}
}

func TestProviderLabel(t *testing.T) {
	t.Run("quotes profile name", func(t *testing.T) {
		got := providerLabel(config.ProviderClaude, `desk "one"`)
		want := `Claude profile "desk \"one\""`
		if got != want {
			t.Fatalf("providerLabel() = %q, want %q", got, want)
		}
	})
	t.Run("empty profile uses provider title", func(t *testing.T) {
		if got, want := providerLabel(config.ProviderClaude, ""), "Claude"; got != want {
			t.Fatalf("providerLabel() = %q, want %q", got, want)
		}
	})
}

func TestSuggestNames(t *testing.T) {
	tests := []struct {
		role string
		want []string
	}{
		{role: "researcher", want: []string{"atlas", "mira", "sage", "lumen"}},
		{role: "operator", want: []string{"riley", "switch", "marin", "pilot"}},
		{role: "creative partner", want: []string{"sol", "nova", "fig", "muse"}},
		{role: "reviewer", want: []string{"quinn", "arden", "scope", "vera"}},
		{role: "", want: []string{"juno", "forge", "ember", "rowan"}},
		{role: "something new", want: []string{"juno", "forge", "ember", "rowan"}},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			if got := suggestNames(answers{Role: tt.role}); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("suggestNames(%q) = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}

func TestGitVersionLabel(t *testing.T) {
	t.Run("strips only the literal git prefix", func(t *testing.T) {
		got := gitVersionLabel(podiomgit.Status{Version: "git version 2.43.0"})
		if got != "version 2.43.0" {
			t.Fatalf("gitVersionLabel() = %q, want %q", got, "version 2.43.0")
		}
	})
	t.Run("blank version reports found", func(t *testing.T) {
		if got := gitVersionLabel(podiomgit.Status{Version: "  \t "}); got != "found" {
			t.Fatalf("gitVersionLabel() = %q, want found", got)
		}
	})
}
