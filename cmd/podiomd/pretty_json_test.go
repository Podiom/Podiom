package main

import (
	"testing"
)

// #369: both helpers are small pure functions with surprising corners. Every
// expected output was checked against the real functions. The one production
// change is the doc comment: prettyJSON("").
func TestPrettyJSON(t *testing.T) {
	t.Run("an empty body returns ok — and that is relied on by callers", func(t *testing.T) {
		// The doc comment used to say a non-JSON or empty body is returned
		// verbatim; in fact empty (and nil, and whitespace-only) renders as
		// "ok", which callers rely on to render an empty 204 as a confirmation.
		// Do NOT "fix" it (#369).
		if got := prettyJSON(nil); got != "ok" {
			t.Errorf("prettyJSON(nil) = %q, want \"ok\"", got)
		}
		if got := prettyJSON([]byte("")); got != "ok" {
			t.Errorf("prettyJSON(\"\") = %q, want \"ok\"", got)
		}
		if got := prettyJSON([]byte("   ")); got != "ok" {
			t.Errorf("prettyJSON(whitespace) = %q, want \"ok\"", got)
		}
	})

	t.Run("trailing whitespace is absorbed but trailing content is not", func(t *testing.T) {
		if got := prettyJSON([]byte(`{"a":1}   `)); got != "{\n  \"a\": 1\n}" {
			t.Errorf("trailing spaces absorbed: got %q", got)
		}
		if got := prettyJSON([]byte(`{"a":1} trailing`)); got != `{"a":1} trailing` {
			t.Errorf("trailing content: got %q, want verbatim", got)
		}
		if got := prettyJSON([]byte(`{"a":1}{"b":2}`)); got != `{"a":1}{"b":2}` {
			t.Errorf("trailing object: got %q, want verbatim", got)
		}
	})

	t.Run("bare scalars pass through unchanged", func(t *testing.T) {
		for _, raw := range []string{"123", `"str"`, "null", "true"} {
			if got := prettyJSON([]byte(raw)); got != raw {
				t.Errorf("prettyJSON(%s) = %q, want verbatim", raw, got)
			}
		}
	})
}

func TestPermissionInputDescription(t *testing.T) {
	t.Run("a non-string description silently becomes empty", func(t *testing.T) {
		for _, raw := range []string{`{"description":123}`, `{"description":true}`, `{"description":["a"]}`, `{"description":{"x":1}}`} {
			if got := permissionInputDescription([]byte(raw)); got != "" {
				t.Errorf("%s: got %q, want empty", raw, got)
			}
		}
	})

	t.Run("the key is case-sensitive", func(t *testing.T) {
		if got := permissionInputDescription([]byte(`{"Description":"hi"}`)); got != "" {
			t.Errorf("got %q, want empty for a PascalCase key", got)
		}
		if got := permissionInputDescription([]byte(`{"description":"hi"}`)); got != "hi" {
			t.Errorf("got %q, want \"hi\"", got)
		}
	})

	t.Run("on duplicate keys the last one wins", func(t *testing.T) {
		if got := permissionInputDescription([]byte(`{"description":"a","description":"b"}`)); got != "b" {
			t.Errorf("got %q, want \"b\"", got)
		}
	})

	t.Run("the value is not trimmed", func(t *testing.T) {
		if got := permissionInputDescription([]byte(`{"description":"  padded  "}`)); got != "  padded  " {
			t.Errorf("got %q, want the padding intact (contrast prettyJSON, which trims)", got)
		}
	})

	t.Run("any non-object input returns empty", func(t *testing.T) {
		for _, raw := range []string{`"str"`, `123`, `null`, `[{"description":"a"}]`} {
			if got := permissionInputDescription([]byte(raw)); got != "" {
				t.Errorf("%s: got %q, want empty", raw, got)
			}
		}
	})
}
