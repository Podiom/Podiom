package server

import (
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/core"
)

// #366: validateInterviewQuestion's six error returns and two surprises (byte
// limits, a trim that is computed and thrown away) had no direct coverage.
// Every expected message here was checked against the real function.
func TestValidateInterviewQuestion(t *testing.T) {
	valid := func(q interviewQuestionRequest) {
		t.Helper()
		if err := validateInterviewQuestion(q); err != nil {
			t.Fatalf("expected valid, got: %v", err)
		}
	}

	t.Run("byte limits, not runes", func(t *testing.T) {
		ascii40 := strings.Repeat("a", 40)
		emoji20 := strings.Repeat("🙂", 20)  // 20 runes, 80 bytes
		emoji10 := strings.Repeat("🙂", 10)  // 40 bytes, exactly at the cap
		q := testInterviewQuestion(core.RequiredInterviewTopics[0])
		q.Header = ascii40
		valid(q)
		q.Header = emoji10
		valid(q)
		q.Header = emoji20
		if err := validateInterviewQuestion(q); err == nil || err.Error() != "question header must contain 1 to 40 characters" {
			t.Errorf("got %v, want the byte-limit error", err)
		}
	})

	t.Run("question length boundary", func(t *testing.T) {
		q := testInterviewQuestion(core.RequiredInterviewTopics[0])
		q.Question = strings.Repeat("a", 320)
		valid(q)
		q.Question = strings.Repeat("a", 321)
		if err := validateInterviewQuestion(q); err == nil || err.Error() != "question must contain 1 to 320 characters" {
			t.Errorf("got %v, want the question-length error", err)
		}
	})

	t.Run("option label and description length boundaries", func(t *testing.T) {
		q := testInterviewQuestion(core.RequiredInterviewTopics[0])
		q.Options[0].Label = strings.Repeat("a", 80)
		valid(q)
		q.Options[0].Label = strings.Repeat("a", 81)
		if err := validateInterviewQuestion(q); err == nil ||
			err.Error() != "option labels must be at most 80 characters and descriptions at most 240 characters" {
			t.Errorf("got %v, want the option-length error", err)
		}
		q.Options[0].Label = "a"
		q.Options[0].Description = strings.Repeat("a", 241)
		if err := validateInterviewQuestion(q); err == nil ||
			err.Error() != "option labels must be at most 80 characters and descriptions at most 240 characters" {
			t.Errorf("got %v, want the same error string for an over-long description", err)
		}
	})

	t.Run("the trim is computed and then thrown away", func(t *testing.T) {
		q := testInterviewQuestion(core.RequiredInterviewTopics[0])
		q.Header = "  padded  "
		valid(q)
		if q.Header != "  padded  " {
			t.Errorf("the caller's struct was mutated: %q", q.Header)
		}
	})

	t.Run("topic matching is exact-case", func(t *testing.T) {
		q := testInterviewQuestion("IDENTITY_CONTEXT")
		if err := validateInterviewQuestion(q); err == nil ||
			err.Error() != `unknown interview topic "IDENTITY_CONTEXT"` {
			t.Errorf("got %v, want the unknown-topic error", err)
		}
		q.Topic = ""
		if err := validateInterviewQuestion(q); err == nil ||
			err.Error() != `unknown interview topic ""` {
			t.Errorf("got %v, want the empty-topic error", err)
		}
	})

	t.Run("duplicate labels ignore case and whitespace, echoing a different form", func(t *testing.T) {
		mk := func(labels ...string) interviewQuestionRequest {
			q := testInterviewQuestion(core.RequiredInterviewTopics[0])
			for i, label := range labels {
				q.Options[i].Label = label
			}
			return q
		}
		q := mk("a", "A", "c")
		if err := validateInterviewQuestion(q); err == nil || err.Error() != `duplicate option label "A"` {
			t.Errorf("got %v, want the raw label echoed", err)
		}
		q = mk("a", " a ", "c")
		if err := validateInterviewQuestion(q); err == nil || err.Error() != `duplicate option label "a"` {
			t.Errorf("got %v, want the trimmed label echoed", err)
		}
	})

	t.Run("the option count is checked before any per-option content", func(t *testing.T) {
		q := testInterviewQuestion(core.RequiredInterviewTopics[0])
		q.Options = nil
		if err := validateInterviewQuestion(q); err == nil ||
			err.Error() != "question must contain 3 to 5 options" {
			t.Errorf("got %v, want the option-count error for nil options", err)
		}
		two := testInterviewQuestion(core.RequiredInterviewTopics[0])
		two.Options = two.Options[:2]
		q.Options = two.Options
		if err := validateInterviewQuestion(q); err == nil ||
			err.Error() != "question must contain 3 to 5 options" {
			t.Errorf("got %v, want the option-count error for two options", err)
		}
	})

	t.Run("whitespace-only description counts as empty", func(t *testing.T) {
		q := testInterviewQuestion(core.RequiredInterviewTopics[0])
		q.Options[0].Description = "   "
		if err := validateInterviewQuestion(q); err == nil ||
			err.Error() != "every option needs a label and description" {
			t.Errorf("got %v, want the empty-description error", err)
		}
	})

	t.Run("five options pass, six do not", func(t *testing.T) {
		q := testInterviewQuestion(core.RequiredInterviewTopics[0])
		q.Options = append(q.Options,
			adapter.UserInputOption{Label: "Four", Description: "Fourth."},
			adapter.UserInputOption{Label: "Five", Description: "Fifth."},
		)
		valid(q)
		q.Options = append(q.Options, adapter.UserInputOption{Label: "Six", Description: "Sixth."})
		if err := validateInterviewQuestion(q); err == nil ||
			err.Error() != "question must contain 3 to 5 options" {
			t.Errorf("got %v, want the option-count error for six options", err)
		}
	})
}
