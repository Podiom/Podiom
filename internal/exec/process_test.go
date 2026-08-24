package exec

import (
	"reflect"
	"testing"
)

func TestProfileEnv(t *testing.T) {
	tests := []struct {
		name     string
		env      []string
		varName  string
		dir      string
		expected []string
	}{
		{
			name:     "empty name returns env unchanged",
			env:      []string{"A=1", "B=2"},
			varName:  "",
			dir:      "/path",
			expected: []string{"A=1", "B=2"},
		},
		{
			name:     "empty dir removes variable from env",
			env:      []string{"A=1", "TARGET=/old", "B=2"},
			varName:  "TARGET",
			dir:      "",
			expected: []string{"A=1", "B=2"},
		},
		{
			name:     "empty dir and absent variable is no-op",
			env:      []string{"A=1", "B=2"},
			varName:  "TARGET",
			dir:      "",
			expected: []string{"A=1", "B=2"},
		},
		{
			name:     "existing variable replaced with new dir",
			env:      []string{"A=1", "CLAUDE_CONFIG_DIR=/old", "B=2"},
			varName:  "CLAUDE_CONFIG_DIR",
			dir:      "/new",
			expected: []string{"A=1", "B=2", "CLAUDE_CONFIG_DIR=/new"},
		},
		{
			name:     "absent variable appended with new dir",
			env:      []string{"A=1", "B=2"},
			varName:  "CLAUDE_CONFIG_DIR",
			dir:      "/new",
			expected: []string{"A=1", "B=2", "CLAUDE_CONFIG_DIR=/new"},
		},
		{
			name:     "multiple duplicate keys in env are all stripped",
			env:      []string{"CLAUDE_CONFIG_DIR=old1", "A=1", "CLAUDE_CONFIG_DIR=old2"},
			varName:  "CLAUDE_CONFIG_DIR",
			dir:      "/final",
			expected: []string{"A=1", "CLAUDE_CONFIG_DIR=/final"},
		},
		{
			name:     "prefix match on key without equals is not stripped",
			env:      []string{"FOOBAR=123", "A=1"},
			varName:  "FOO",
			dir:      "/new",
			expected: []string{"FOOBAR=123", "A=1", "FOO=/new"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := make([]string, len(tt.env))
			copy(orig, tt.env)

			got := ProfileEnv(tt.env, tt.varName, tt.dir)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("ProfileEnv(%v, %q, %q) = %v; want %v", tt.env, tt.varName, tt.dir, got, tt.expected)
			}
			// Verify original slice was not mutated
			if !reflect.DeepEqual(tt.env, orig) {
				t.Errorf("ProfileEnv mutated input slice: got %v; want %v", tt.env, orig)
			}
		})
	}
}
