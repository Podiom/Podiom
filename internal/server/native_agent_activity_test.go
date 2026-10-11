package server

import (
	"testing"

	"github.com/Podiom/Podiom/internal/adapter"
)

func TestNativeAgentActivityKey(t *testing.T) {
	tests := []struct {
		name     string
		activity *adapter.NativeAgentActivity
		want     string
	}{
		{
			name: "nil activity",
			want: "",
		},
		{
			name:     "all fields empty",
			activity: &adapter.NativeAgentActivity{},
			want:     "",
		},
		{
			name: "task ID takes precedence",
			activity: &adapter.NativeAgentActivity{
				TaskID:            "task-1",
				ToolUseID:         "tool-1",
				Provider:          "claude",
				ProviderAgentName: "researcher",
				Description:       "Find files",
			},
			want: "task:task-1",
		},
		{
			name: "tool use ID takes precedence over provider agent name",
			activity: &adapter.NativeAgentActivity{
				ToolUseID:         "tool-1",
				Provider:          "claude",
				ProviderAgentName: "researcher",
				Description:       "Find files",
			},
			want: "tool:tool-1",
		},
		{
			name: "provider agent name includes description",
			activity: &adapter.NativeAgentActivity{
				Provider:          "claude",
				ProviderAgentName: "researcher",
				Description:       "Find files",
			},
			want: "claude:researcher:Find files",
		},
		{
			name: "empty description retains trailing colon",
			activity: &adapter.NativeAgentActivity{
				Provider:          "claude",
				ProviderAgentName: "researcher",
			},
			want: "claude:researcher:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nativeAgentActivityKey(tt.activity); got != tt.want {
				t.Fatalf("nativeAgentActivityKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNativeAgentActivityKeyDistinguishesDescriptions(t *testing.T) {
	first := &adapter.NativeAgentActivity{
		Provider:          "claude",
		ProviderAgentName: "researcher",
		Description:       "Find files",
	}
	second := &adapter.NativeAgentActivity{
		Provider:          "claude",
		ProviderAgentName: "researcher",
		Description:       "Inspect tests",
	}

	firstKey := nativeAgentActivityKey(first)
	secondKey := nativeAgentActivityKey(second)
	if firstKey == secondKey {
		t.Fatalf("keys for different descriptions are equal: %q", firstKey)
	}
	if firstKey != "claude:researcher:Find files" || secondKey != "claude:researcher:Inspect tests" {
		t.Fatalf("keys = (%q, %q), want description-specific keys", firstKey, secondKey)
	}
}

func TestCloneNativeAgentActivity(t *testing.T) {
	if got := cloneNativeAgentActivity(nil); got != (adapter.NativeAgentActivity{}) {
		t.Fatalf("cloneNativeAgentActivity(nil) = %#v, want zero value", got)
	}

	input := &adapter.NativeAgentActivity{
		Provider:          "claude",
		TaskID:            "task-1",
		ToolUseID:         "tool-1",
		ProviderAgentName: "researcher",
		PodiomAgentName:   "Research Agent",
		DisplayName:       "Research",
		Description:       "Find files",
		Status:            "running",
	}
	got := cloneNativeAgentActivity(input)
	if got != *input {
		t.Fatalf("cloneNativeAgentActivity() = %#v, want %#v", got, *input)
	}

	got.Status = "completed"
	if input.Status != "running" {
		t.Fatalf("mutating clone changed original status to %q", input.Status)
	}
}

func TestCloneNativeAgentActivities(t *testing.T) {
	if got := cloneNativeAgentActivities(nil); got != nil {
		t.Fatalf("cloneNativeAgentActivities(nil) = %#v, want nil", got)
	}
	if got := cloneNativeAgentActivities([]adapter.NativeAgentActivity{}); got != nil {
		t.Fatalf("cloneNativeAgentActivities(empty) = %#v, want nil", got)
	}

	input := []adapter.NativeAgentActivity{
		{Provider: "claude", TaskID: "task-1", Status: "running"},
		{Provider: "codex", ToolUseID: "tool-2", Status: "completed"},
	}
	got := cloneNativeAgentActivities(input)
	if len(got) != len(input) {
		t.Fatalf("clone length = %d, want %d", len(got), len(input))
	}
	for i := range input {
		if got[i] != input[i] {
			t.Fatalf("clone[%d] = %#v, want %#v", i, got[i], input[i])
		}
	}

	got[0].Status = "completed"
	if input[0].Status != "running" {
		t.Fatalf("mutating clone changed original status to %q", input[0].Status)
	}
}
