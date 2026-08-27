package tools

import (
	"strings"
	"testing"
)

func TestTail(t *testing.T) {
	extraLength := 500
	extraOutputTail := outputTail + extraLength
	tests := []struct {
		name        string
		inputString string
		want        string
	}{
		{name: "short input",
			inputString: "Reading package lists... Done \nBuilding dependency tree... Done",
			want:        "Reading package lists... Done \nBuilding dependency tree... Done"},
		{name: "boundary length input",
			inputString: strings.Repeat("a", outputTail),
			want:        strings.Repeat("a", outputTail)},
		{name: "Long input",
			inputString: strings.Repeat("a", extraOutputTail),
			want:        "…" + strings.Repeat("a", outputTail)},
		{name: "Long input with Leading/trailing whitespace",
			inputString: "  " + strings.Repeat("a", extraOutputTail) + "  ",
			want:        "…" + strings.Repeat("a", outputTail)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tail(tt.inputString)
			if got != tt.want {
				t.Errorf("for: %s, want: %q, got: %q", tt.name, tt.want, got)
			}
		})
	}
}
