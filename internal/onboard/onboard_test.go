package onboard

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/providercheck"
)

func TestCleanSoulMarkdownRemovesFenceAndRequiresIdentity(t *testing.T) {
	got := CleanSoulMarkdown("```markdown\nName: juno\n\n## Working style\n- kind\n```")
	if strings.Contains(got, "```") {
		t.Fatalf("fence not removed: %q", got)
	}
	if !strings.HasPrefix(got, "# Identity\n\n") {
		t.Fatalf("missing identity heading: %q", got)
	}
}

func TestDeterministicSoulCarriesQuestionnaire(t *testing.T) {
	soul := deterministicSoul("juno", answers{
		Role:          "builder",
		Temperament:   "warm and curious",
		Collaboration: "make reasonable calls",
		Autonomy:      "medium",
		Strengths:     "careful implementation",
		Boundaries:    "avoid destructive changes",
		Playfulness:   "moderate",
		CaresAbout:    "finishing meaningful work",
		Extra:         "likes concise plans",
	})
	for _, want := range []string{"juno", "builder", "warm and curious", "avoid destructive changes", "## Worldview", "## Calibration notes"} {
		if !strings.Contains(soul, want) {
			t.Fatalf("soul missing %q:\n%s", want, soul)
		}
	}
}

func TestSanitizeName(t *testing.T) {
	if got := sanitizeName("Juno Bright!"); got != "juno-bright" {
		t.Fatalf("sanitizeName = %q", got)
	}
	if got := sanitizeName("../../"); got != "juno" {
		t.Fatalf("empty unsafe name fallback = %q", got)
	}
}

func TestLoggedInProviders(t *testing.T) {
	statuses := []providercheck.Status{
		{Provider: config.ProviderClaude, Found: true, Ready: true, LoginChecked: true, LoggedIn: true},
		{Provider: config.ProviderCodex, Found: true, Ready: true, LoginChecked: true, LoggedIn: false},
	}
	got := loggedInProviders(statuses)
	if len(got) != 1 || got[0] != config.ProviderClaude {
		t.Fatalf("loggedInProviders = %v, want [claude]", got)
	}

	legacy := []providercheck.Status{
		{Provider: config.ProviderClaude, Found: true, Ready: true, LoginChecked: false},
		{Provider: config.ProviderCodex, Found: true, Ready: false, LoginChecked: false},
	}
	got = loggedInProviders(legacy)
	if len(got) != 1 || got[0] != config.ProviderClaude {
		t.Fatalf("legacy loggedInProviders = %v, want [claude]", got)
	}
}

func TestPrintDoctorStates(t *testing.T) {
	var out bytes.Buffer
	printDoctor(&out, []providercheck.Status{
		{Provider: config.ProviderClaude},
		{Provider: config.ProviderCodex, Found: true, Version: "codex 1", LoginChecked: true},
		{Provider: config.ProviderClaude, Found: true, Version: "claude 1"},
		{Provider: config.ProviderCodex, Found: true, Version: "codex 2", Ready: true, LoginChecked: true, LoggedIn: true},
	})
	text := out.String()
	for _, want := range []string{"missing", "installed, not logged in", "login unknown", "ready"} {
		if !strings.Contains(text, want) {
			t.Fatalf("printDoctor missing %q in:\n%s", want, text)
		}
	}
}
