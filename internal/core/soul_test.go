package core

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateSoulMarkdown(t *testing.T) {
	tests := []struct {
		name    string
		soul    string
		wantErr error
	}{
		{name: "complete", soul: "# Identity, ## Purpose, ## Worldview, ## Working style, ## Voice, ## Strengths, ## Boundaries, ## Calibration notes", wantErr: nil},
		{name: "incomplete missing ## Voice", soul: "# Identity, ## Purpose, ## Worldview, ## Working style, ## Strengths, ## Boundaries, ## Calibration notes", wantErr: errors.New(`generated SOUL.md missing required section "## Voice"`)},
		{name: "empty", soul: "", wantErr: errors.New(`generated SOUL.md missing required section "# Identity"`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotErr := validateSoulMarkdown(tt.soul)
			if gotErr != nil && tt.wantErr == nil {
				t.Fatalf("unexpected error: %s", gotErr.Error())
			}
			if gotErr == nil && tt.wantErr != nil {
				t.Fatalf("expected error: want: %s, got: nil", tt.wantErr.Error())
			}
			if tt.wantErr != nil && gotErr.Error() != tt.wantErr.Error() {
				t.Errorf("invalid error meassage: want: %s, got: %s", tt.wantErr.Error(), gotErr.Error())
			}
		})
	}
}

func TestSoulPromptCarriesInputsAndRequiredShape(t *testing.T) {
	prompt := SoulPrompt("juno", "# Identity\n\nName: juno\n\nold soul", SoulGenerateRequest{
		Notes:         "make the voice more direct",
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
	for _, want := range []string{
		"juno",
		"builder",
		"warm and curious",
		"avoid destructive changes",
		"make the voice more direct",
		"old soul",
		"SOUL.md is static identity. MEMORY.md is later lived experience.",
		// Constitutional framing: colleague/personhood in the second person,
		// and an explicit ban on bot/AI/artifact language.
		"second person",
		"Never describe them as an AI",
		"## Purpose",
		"## Worldview",
		"## Working style",
		"## Voice",
		"## Strengths",
		"## Boundaries",
		"## Calibration notes",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "```json") {
		t.Fatalf("prompt should ask for markdown, not JSON:\n%s", prompt)
	}
}

func TestCleanSoulMarkdownRemovesFenceAndRequiresIdentity(t *testing.T) {
	got := CleanSoulMarkdown("```markdown\nName: juno\n\n## Working style\n- kind\n```")
	if strings.Contains(got, "```") {
		t.Fatalf("fence not removed: %q", got)
	}
	if !strings.HasPrefix(got, "# Identity\n\n") {
		t.Fatalf("missing identity heading: %q", got)
	}
}
