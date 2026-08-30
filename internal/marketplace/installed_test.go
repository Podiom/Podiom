package marketplace

import (
	"slices"
	"testing"

	"github.com/Podiom/Podiom/internal/skills"
)

func TestRootLabels(t *testing.T) {
	tests := []struct {
		name string
		srcs []skills.Source
		want []string
	}{
		{name: "nil input", srcs: nil, want: []string{}},
		{name: "one source", srcs: []skills.Source{"project"}, want: []string{"project"}},
		{name: "multiple sources", srcs: []skills.Source{"managed", "project"}, want: []string{"managed", "project"}},
		{name: "duplicate sources", srcs: []skills.Source{"managed", "managed"}, want: []string{"managed", "managed"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rootLabels(tt.srcs)
			if !slices.Equal(got, tt.want) {
				t.Errorf("Want: %v, got %v", tt.want, got)
			}
		})
	}
}
