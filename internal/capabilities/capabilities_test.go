package capabilities

import "testing"

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
