package onboard

import (
	"strings"
	"testing"
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
	}, false)
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
