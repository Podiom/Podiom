package capabilities

import (
	"errors"
	"testing"
)

func TestParseEffortsFromHelp(t *testing.T) {
	help := `--effort <level>  Effort level for the current session
                                        (low, medium, high, xhigh, max)`
	got := ParseEffortsFromHelp(help)
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5: %+v", len(got), got)
	}
	for i, want := range []string{"low", "medium", "high", "xhigh", "max"} {
		if got[i].Effort != want {
			t.Fatalf("got[%d] = %q, want %q", i, got[i].Effort, want)
		}
	}
}

func TestFallbackModelsCarryEfforts(t *testing.T) {
	caps := Fallback("codex", "")
	if len(caps.Models) == 0 {
		t.Fatal("expected fallback models")
	}
	for _, model := range caps.Models {
		if len(model.SupportedEfforts) == 0 {
			t.Fatalf("model %s has no efforts", model.Model)
		}
	}
}

func TestCapabilitiesHelpers(t *testing.T) {
	t.Run("WithError", func(t *testing.T) {
		caps := ProviderCapabilities{}
		if got := WithError(caps, nil); got.Error != caps.Error || got.Stale != caps.Stale || len(got.Models) != len(caps.Models) {
			t.Fatal("nil error should leave capabilities unchanged")
		}
		got := WithError(caps, errors.New("discovery failed"))
		if got.Error != "discovery failed" || !got.Stale {
			t.Fatalf("got %+v, want error and stale snapshot", got)
		}
	})

	t.Run("CloneModelsDeepCopiesNestedSlices", func(t *testing.T) {
		original := []ModelOption{{
			SupportedEfforts: []EffortOption{{Effort: "low"}},
			InputModalities:  []string{"text"},
		}}
		clone := CloneModels(original)
		clone[0].SupportedEfforts[0].Effort = "high"
		clone[0].InputModalities[0] = "image"
		if original[0].SupportedEfforts[0].Effort != "low" || original[0].InputModalities[0] != "text" {
			t.Fatal("clone mutation changed the original")
		}
	})

	efforts := []EffortOption{{Effort: "low"}, {Effort: "high"}}
	t.Run("MergeEfforts", func(t *testing.T) {
		caps := ProviderCapabilities{Models: []ModelOption{
			{DefaultReasoningEffort: "low"},
			{DefaultReasoningEffort: "medium"},
		}}
		got := MergeEfforts(caps, efforts)
		if got.Models[0].DefaultReasoningEffort != "low" || got.Models[1].DefaultReasoningEffort != "low" {
			t.Fatalf("defaults = %q, %q", got.Models[0].DefaultReasoningEffort, got.Models[1].DefaultReasoningEffort)
		}
		if got := MergeEfforts(caps, nil); got.Models[0].DefaultReasoningEffort != "low" {
			t.Fatal("empty efforts should leave capabilities unchanged")
		}
	})

	t.Run("defaultEffort", func(t *testing.T) {
		if got := defaultEffort([]EffortOption{{Effort: "high"}, {Effort: "medium"}}); got != "medium" {
			t.Fatalf("got %q, want medium", got)
		}
		if got := defaultEffort(nil); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
		if got := defaultEffort([]EffortOption{{Effort: "high"}}); got != "high" {
			t.Fatalf("got %q, want first effort", got)
		}
	})

	if !HasEffort(efforts, "high") || HasEffort(efforts, "medium") {
		t.Fatal("HasEffort returned an incorrect result")
	}
}
