package mcp

import (
	"reflect"
	"testing"
)

// #378: dedupe is pure ([]Server in, []Server out) but its merge semantics are
// subtle: first-wins per FIELD (not per record), a merged server can end up
// with an HTTP URL and a stdio transport, the invented podiom source is added
// only to a first occurrence that has none, whitespace-only names survive, and
// the result aliases the caller's Args on first insert. Every expected output
// here was checked against the real function.
func TestDedupe(t *testing.T) {
	t.Run("nil and empty inputs return non-nil empty slices", func(t *testing.T) {
		got := dedupe(nil)
		if got == nil {
			t.Fatal("dedupe(nil) returned a nil slice; want a non-nil empty one (reflect.DeepEqual against []Server(nil) would fail)")
		}
		if len(got) != 0 {
			t.Errorf("dedupe(nil): len %d", len(got))
		}
		empty := dedupe([]Server{})
		if empty == nil || len(empty) != 0 {
			t.Errorf("dedupe(empty): %v", empty)
		}
		if reflect.DeepEqual(got, []Server(nil)) {
			t.Error("dedupe(nil) is DeepEqual to []Server(nil)")
		}
	})

	t.Run("a merged server can carry an HTTP URL and a stdio transport", func(t *testing.T) {
		got := dedupe([]Server{
			{Name: "x", Command: "c"},
			{Name: "x", URL: "https://u"},
		})
		if len(got) != 1 {
			t.Fatalf("got %d servers, want 1", len(got))
		}
		s := got[0]
		if s.Transport != TransportStdio {
			t.Errorf("transport %q: the first entry is normalised to stdio before storing, so the empty-transport guard never fires", s.Transport)
		}
		if s.URL != "https://u" {
			t.Errorf("URL %q: should be backfilled from the second entry", s.URL)
		}
		if s.Command != "c" {
			t.Errorf("command %q: should be kept from the first entry", s.Command)
		}
	})

	t.Run("first-wins is per field, not per record", func(t *testing.T) {
		got := dedupe([]Server{
			{Name: "x", Command: "first", Sources: []Source{SourceCodex}},
			{Name: "x", Command: "second", URL: "https://u", Sources: []Source{SourcePodiom}},
		})
		if len(got) != 1 {
			t.Fatalf("got %d servers, want 1", len(got))
		}
		if got[0].Command != "first" {
			t.Errorf("command %q: first record wins per field", got[0].Command)
		}
		if got[0].URL != "https://u" {
			t.Errorf("URL %q: backfilled from the second record", got[0].URL)
		}
		if got[0].Command == "second" && got[0].URL == "" {
			t.Error("the whole record must not be second-wins")
		}
	})

	t.Run("the invented podiom source is only for a sourceless first occurrence", func(t *testing.T) {
		got := dedupe([]Server{
			{Name: "x"},
			{Name: "x", Sources: []Source{SourceCodex}},
			{Name: "x", Sources: []Source{SourceClaude}},
		})
		if len(got) != 1 || !reflect.DeepEqual(got[0].Sources, []Source{SourcePodiom, SourceClaude, SourceCodex}) {
			t.Errorf("sourceless first occurrence: got %v, want [podiom claude codex]", got[0].Sources)
		}

		got = dedupe([]Server{
			{Name: "x", Sources: []Source{SourceCodex, SourceClaude}},
			{Name: "x", Sources: []Source{SourceCodex}},
		})
		if len(got) != 1 || !reflect.DeepEqual(got[0].Sources, []Source{SourceClaude, SourceCodex}) {
			t.Errorf("sourced first occurrence: got %v, want [claude codex] with no invented podiom", got[0].Sources)
		}
	})

	t.Run("whitespace-only names survive and sort bytewise", func(t *testing.T) {
		got := dedupe([]Server{
			{Name: "x "},
			{Name: "x"},
			{Name: "  "},
			{Name: ""},
			{Name: "A"}, {Name: "X"}, {Name: "a"}, {Name: "b"},
		})
		var names []string
		for _, s := range got {
			names = append(names, s.Name)
		}
		want := []string{"  ", "A", "X", "a", "b", "x", "x "}
		if !reflect.DeepEqual(names, want) {
			t.Errorf("names %v, want %v (bytewise, empty dropped, whitespace-only kept)", names, want)
		}
	})

	t.Run("the result aliases the caller's Args on first insert", func(t *testing.T) {
		in := []string{"--flag"}
		got := dedupe([]Server{{Name: "x", Args: in}})
		got[0].Args[0] = "MUTATED"
		if in[0] != "MUTATED" {
			t.Error("first insert must alias the caller's Args (only the duplicate path copies)")
		}
	})

	t.Run("a server with no env keeps EnvVars nil but EnvStatus non-nil", func(t *testing.T) {
		got := dedupe([]Server{{Name: "x"}})
		if got[0].EnvVars != nil {
			t.Errorf("EnvVars %v: should stay nil", got[0].EnvVars)
		}
		if got[0].EnvStatus == nil {
			t.Error("EnvStatus should be a non-nil empty slice")
		}
	})
}
