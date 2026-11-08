package mcp

import (
	"os"
	"reflect"
	"testing"
)

// serverNames extracts just the names, preserving order, for assertions.
func serverNames(servers []Server) []string {
	var out []string
	for _, s := range servers {
		out = append(out, s.Name)
	}
	return out
}

// TestDedupeNilAndEmptyInputs pins that dedupe never returns a nil slice:
// the result is built with make([]Server, 0, len(names)), so both a nil and
// an empty input yield a non-nil, empty result.
func TestDedupeNilAndEmptyInputs(t *testing.T) {
	if got := dedupe(nil); got == nil {
		t.Fatalf("dedupe(nil) = nil, want non-nil empty slice")
	} else if len(got) != 0 {
		t.Fatalf("dedupe(nil) = %#v, want empty", got)
	}
	if got := dedupe([]Server{}); got == nil {
		t.Fatalf("dedupe([]Server{}) = nil, want non-nil empty slice")
	} else if len(got) != 0 {
		t.Fatalf("dedupe([]Server{}) = %#v, want empty", got)
	}
}

// TestDedupeDropsOnlyEmptyNames pins the exact guard: s.Name == "" with no
// trimming, so whitespace-only names survive and "x " and "x" stay separate.
func TestDedupeDropsOnlyEmptyNames(t *testing.T) {
	got := dedupe([]Server{
		{Name: "  "},
		{Name: "x "},
		{Name: "x"},
		{Name: ""},
	})
	want := []string{"  ", "x", "x "}
	if !reflect.DeepEqual(serverNames(got), want) {
		t.Fatalf("names = %#v, want %#v", serverNames(got), want)
	}
}

// TestDedupeCaseSensitiveBytewiseOrder pins that names sort bytewise
// (uppercase before lowercase) rather than case-insensitively.
func TestDedupeCaseSensitiveBytewiseOrder(t *testing.T) {
	got := dedupe([]Server{{Name: "b"}, {Name: "a"}, {Name: "x"}, {Name: "X"}, {Name: "A"}})
	want := []string{"A", "X", "a", "b", "x"}
	if !reflect.DeepEqual(serverNames(got), want) {
		t.Fatalf("names = %#v, want %#v", serverNames(got), want)
	}
}

// TestDedupeMergesURLIntoStdio pins that a merged server can end up with a
// stdio transport and an HTTP URL at the same time. The first entry is
// normalised to stdio before it is stored, so the
// `if existing.Transport == ""` guard in the merge block can never fire and
// the URL is backfilled without a matching transport fix.
func TestDedupeMergesURLIntoStdio(t *testing.T) {
	got := dedupe([]Server{
		{Name: "x", Command: "c"},
		{Name: "x", URL: "https://u"},
	})
	if len(got) != 1 {
		t.Fatalf("got %d servers (%#v), want 1", len(got), serverNames(got))
	}
	s := got[0]
	if s.Transport != TransportStdio {
		t.Fatalf("Transport = %q, want %q", s.Transport, TransportStdio)
	}
	if s.URL != "https://u" {
		t.Fatalf("URL = %q, want %q", s.URL, "https://u")
	}
	if s.Command != "c" {
		t.Fatalf("Command = %q, want %q", s.Command, "c")
	}
}

// TestDedupeFirstWinsPerField pins that first-wins is per field, not per
// record: one result can be assembled from two different inputs.
func TestDedupeFirstWinsPerField(t *testing.T) {
	got := dedupe([]Server{
		{Name: "x", Command: "first", URL: "https://first", Sources: []Source{SourceCodex}},
		{Name: "x", Command: "second", URL: "https://second", Sources: []Source{SourcePodiom}},
	})
	if len(got) != 1 {
		t.Fatalf("got %d servers (%#v), want 1", len(got), serverNames(got))
	}
	if got[0].Command != "first" {
		t.Fatalf("Command = %q, want %q (first-wins per field)", got[0].Command, "first")
	}
	if got[0].URL != "https://first" {
		t.Fatalf("URL = %q, want %q (first-wins per field)", got[0].URL, "https://first")
	}
	wantSources := []Source{SourcePodiom, SourceCodex}
	if !reflect.DeepEqual(got[0].Sources, wantSources) {
		t.Fatalf("Sources = %#v, want %#v", got[0].Sources, wantSources)
	}
}

// TestDedupePodiomSourceInventedForFirstOnly pins that the invented podiom
// source is added only when the *first* occurrence has no sources, and that
// output order is always the canonical podiom -> claude -> codex.
func TestDedupePodiomSourceInventedForFirstOnly(t *testing.T) {
	t.Run("first without sources gets podiom, later sources merge in", func(t *testing.T) {
		got := dedupe([]Server{
			{Name: "x"},
			{Name: "x", Sources: []Source{SourceCodex}},
			{Name: "x", Sources: []Source{SourceClaude}},
		})
		if len(got) != 1 {
			t.Fatalf("got %d servers (%#v), want 1", len(got), serverNames(got))
		}
		want := []Source{SourcePodiom, SourceClaude, SourceCodex}
		if !reflect.DeepEqual(got[0].Sources, want) {
			t.Fatalf("Sources = %#v, want %#v", got[0].Sources, want)
		}
	})
	t.Run("first with sources never gets podiom", func(t *testing.T) {
		got := dedupe([]Server{
			{Name: "x", Sources: []Source{SourceCodex, SourceClaude}},
			{Name: "x", Sources: []Source{SourceCodex}},
		})
		if len(got) != 1 {
			t.Fatalf("got %d servers (%#v), want 1", len(got), serverNames(got))
		}
		want := []Source{SourceClaude, SourceCodex}
		if !reflect.DeepEqual(got[0].Sources, want) {
			t.Fatalf("Sources = %#v, want %#v (no invented podiom)", got[0].Sources, want)
		}
	})
}

// TestDedupeAliasesCallerArgsOnFirstInsert pins that the result aliases the
// caller's Args on the first insert: the duplicate path copies, so only the
// first insert aliases.
func TestDedupeAliasesCallerArgsOnFirstInsert(t *testing.T) {
	in := []string{"--flag", "value"}
	got := dedupe([]Server{{Name: "x", Args: in}})
	if len(got) != 1 {
		t.Fatalf("got %d servers (%#v), want 1", len(got), serverNames(got))
	}
	got[0].Args[0] = "MUTATED"
	if in[0] != "MUTATED" {
		t.Fatalf("first insert copied Args; caller slice = %#v", in)
	}

	in2 := []string{"b"}
	got2 := dedupe([]Server{
		{Name: "y"},
		{Name: "y", Args: in2},
	})
	if len(got2) != 1 {
		t.Fatalf("got %d servers (%#v), want 1", len(got2), serverNames(got2))
	}
	got2[0].Args[0] = "MUTATED"
	if in2[0] != "b" {
		t.Fatalf("duplicate path aliased Args; caller slice = %#v", in2)
	}
}

// TestDedupeNoEnvVarsPinsEnvStatusShape pins that a server with no env ends
// up with EnvVars still nil while EnvStatus is a non-nil empty slice.
func TestDedupeNoEnvVarsPinsEnvStatusShape(t *testing.T) {
	got := dedupe([]Server{{Name: "x"}})
	if len(got) != 1 {
		t.Fatalf("got %d servers (%#v), want 1", len(got), serverNames(got))
	}
	if got[0].EnvVars != nil {
		t.Fatalf("EnvVars = %#v, want nil", got[0].EnvVars)
	}
	if got[0].EnvStatus == nil {
		t.Fatalf("EnvStatus = nil, want non-nil empty slice")
	}
	if len(got[0].EnvStatus) != 0 {
		t.Fatalf("EnvStatus = %#v, want empty", got[0].EnvStatus)
	}
}

// TestDedupeEnvStatusDeterministic covers envStatus's os.LookupEnv dependency:
// the set case uses t.Setenv, the unset case a name guaranteed to be absent,
// so both are deterministic regardless of the host environment.
func TestDedupeEnvStatusDeterministic(t *testing.T) {
	const unsetName = "PODIOM_DEDUPE_TEST_DEFINITELY_UNSET_9281"
	if _, ok := os.LookupEnv(unsetName); ok {
		t.Skipf("refusing to run: %s is unexpectedly set in the environment", unsetName)
	}
	const setName = "PODIOM_DEDUPE_TEST_SET"
	t.Setenv(setName, "1")

	got := dedupe([]Server{{
		Name: "x",
		EnvVars: EnvVars{
			{Name: setName},
			{Name: unsetName},
		},
	}})
	if len(got) != 1 {
		t.Fatalf("got %d servers (%#v), want 1", len(got), serverNames(got))
	}
	want := []EnvStatus{
		{Name: setName, Set: true},
		{Name: unsetName, Set: false},
	}
	if !reflect.DeepEqual(got[0].EnvStatus, want) {
		t.Fatalf("EnvStatus = %#v, want %#v", got[0].EnvStatus, want)
	}
}
