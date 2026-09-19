package main

import (
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/adapter"
)

func TestSelectedUserInputOptions(t *testing.T) {
	options := []adapter.UserInputOption{{Label: "Alpha"}, {Label: "Beta"}, {Label: "Gamma"}}
	tests := []struct {
		name  string
		line  string
		multi bool
		want  []string
		err   string
	}{
		{"single", " 2 ", false, []string{"Beta"}, ""},
		{"single does not split", "1,2", false, nil, `invalid choice "1,2"`},
		{"multi trims and skips blanks", " 1 ,, 3 ", true, []string{"Alpha", "Gamma"}, ""},
		{"zero", "0", false, nil, `invalid choice "0"`},
		{"negative", "-1", false, nil, `invalid choice "-1"`},
		{"too large", "4", false, nil, `invalid choice "4"`},
		{"non numeric", "one", false, nil, `invalid choice "one"`},
		{"empty", " , ", true, nil, "at least one answer is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectedUserInputOptions(adapter.UserInputQuestion{Options: options, MultiSelect: tt.multi}, tt.line)
			if tt.err != "" {
				if err == nil || err.Error() != tt.err {
					t.Fatalf("error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFormatUserInputFollowup(t *testing.T) {
	one := adapter.UserInputRequest{Questions: []adapter.UserInputQuestion{{ID: "q1", Question: "Pick one"}}}
	if got := formatUserInputFollowup(one, map[string][]string{"q1": {"Alpha", "Beta"}}); got != `Answer to "Pick one": Alpha, Beta` {
		t.Fatalf("got %q", got)
	}
	many := adapter.UserInputRequest{Questions: []adapter.UserInputQuestion{{ID: "q1", Question: "First"}, {ID: "q2", Question: "Missing"}}}
	if got := formatUserInputFollowup(many, map[string][]string{"q1": {"Alpha", "Beta"}}); got != "Answers:\n- First: Alpha, Beta\n- Missing:" {
		t.Fatalf("got %q", got)
	}
}
