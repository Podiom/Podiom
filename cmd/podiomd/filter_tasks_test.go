package main

import (
	"strings"
	"testing"
)

// #368: filterTasks is pure and has behaviours that are easy to break by
// accident. Every expected output here was checked against the real function.
func TestFilterTasks(t *testing.T) {
	t.Run("no filters returns the body byte-for-byte", func(t *testing.T) {
		body := `   [{"ID":"a"}]   `
		out, err := filterTasks(body, "", "", "")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if out != body {
			t.Errorf("got %q, want the input verbatim", out)
		}
	})

	t.Run("any filter re-indents the body", func(t *testing.T) {
		out, err := filterTasks(`[{"ID":"a","Status":"backlog"}]`, "", "backlog", "")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if !strings.Contains(out, "\n  ") {
			t.Errorf("expected MarshalIndent output, got %q", out)
		}
	})

	t.Run("filtering reorders keys alphabetically through the map", func(t *testing.T) {
		out, err := filterTasks(`[{"ID":"a","AssignedAgent":"atlas"}]`, "", "", "atlas")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if !strings.Contains(out, `"AssignedAgent"`) || strings.Index(out, `"AssignedAgent"`) > strings.Index(out, `"ID"`) {
			t.Errorf("AssignedAgent should sort before ID, got %q", out)
		}
	})

	t.Run("a null body becomes an empty array, not null", func(t *testing.T) {
		out, err := filterTasks(`null`, "", "backlog", "")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if strings.TrimSpace(out) != "[]" {
			t.Errorf("got %q, want []", out)
		}
	})

	t.Run("a non-array body is returned verbatim and escapes the filter", func(t *testing.T) {
		body := `{"ID":"a","Status":"backlog"}`
		out, err := filterTasks(body, "", "in-progress", "")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if out != body {
			t.Errorf("got %q, want the input verbatim", out)
		}
	})

	t.Run("trailing bytes after valid JSON also escape the filter", func(t *testing.T) {
		body := `[{"ID":"a","Status":"backlog"}] trailing`
		out, err := filterTasks(body, "", "in-progress", "")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if out != body {
			t.Errorf("got %q, want the input verbatim", out)
		}
	})

	t.Run("a numeric field matches a string filter through the raw JSON fallback", func(t *testing.T) {
		out, err := filterTasks(`[{"ID":"a","Status":3}]`, "", "3", "")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if !strings.Contains(out, `"a"`) {
			t.Errorf("numeric Status 3 should match filter \"3\", got %q", out)
		}
	})

	t.Run("matching is case-sensitive and PascalCase only", func(t *testing.T) {
		out, err := filterTasks(`[{"ID":"a","Status":"Backlog"},{"id":"b","status":"backlog"}]`, "", "backlog", "")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if strings.Contains(out, `"a"`) || strings.Contains(out, `"b"`) {
			t.Errorf("\"Backlog\" must not match \"backlog\", and lowercase keys must not match at all: %q", out)
		}
	})

	t.Run("the error return is dead", func(t *testing.T) {
		for _, body := range []string{`null`, `[]`, `not json`, `[{"ID":"a"}]`} {
			if _, err := filterTasks(body, "p", "s", "a"); err != nil {
				t.Errorf("filterTasks(%q) returned error %v; every path returns nil", body, err)
			}
		}
	})
}
