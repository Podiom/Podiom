package adapter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCodexLegacyFileChangeSummary(t *testing.T) {
	tests := []struct {
		name   string
		fields map[string]json.RawMessage
		want   string
	}{
		{"missing", map[string]json.RawMessage{}, ""},
		{"both null", map[string]json.RawMessage{"fileChanges": json.RawMessage(`null`), "changes": json.RawMessage(`null`)}, ""},
		{"malformed", map[string]json.RawMessage{"fileChanges": json.RawMessage(`{`)}, ""},
		{"primary", map[string]json.RawMessage{"fileChanges": json.RawMessage(`[{"path":"a.go","type":"create"}]`)}, "Approve file changes: create a.go"},
		{"null fallback", map[string]json.RawMessage{"fileChanges": json.RawMessage(`null`), "changes": json.RawMessage(`[{"path":"old.go","type":"update","move_path":"new.go"}]`)}, "Approve file changes: move old.go -> new.go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codexLegacyFileChangeSummary(tt.fields); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCodexFormatFileChanges(t *testing.T) {
	item := func(path, kind, move string) codexFileChangeSummaryItem {
		v := codexFileChangeSummaryItem{Path: path}
		v.Kind.Type, v.Kind.MovePath = kind, move
		return v
	}
	tests := []struct {
		name    string
		changes []codexFileChangeSummaryItem
		want    string
	}{
		{"empty", nil, ""},
		{"defaults", []codexFileChangeSummaryItem{item(" ", " ", "")}, "Approve file changes: update unknown path"},
		{"update move", []codexFileChangeSummaryItem{item(" old ", " update ", " new ")}, "Approve file changes: move old -> new"},
		{"non-update move", []codexFileChangeSummaryItem{item("a", "create", "b")}, "Approve file changes: create a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codexFormatFileChanges(tt.changes); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
	five := []codexFileChangeSummaryItem{}
	for i := 0; i < 5; i++ {
		five = append(five, item("a", "update", ""))
	}
	if got := codexFormatFileChanges(five); strings.Contains(got, "more") {
		t.Fatalf("five changes were truncated: %q", got)
	}
	six := append(five, item("b", "delete", ""))
	if got := codexFormatFileChanges(six); !strings.HasSuffix(got, "; + 1 more") {
		t.Fatalf("six changes = %q", got)
	}
}
