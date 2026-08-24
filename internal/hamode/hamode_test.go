package hamode

import (
	"os"
	"testing"
)

func TestDetect(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T)
		want  bool
	}{
		{
			name: "unset",
			setup: func(t *testing.T) {
				if prev, ok := os.LookupEnv(EnvSupervisorToken); ok {
					if err := os.Unsetenv(EnvSupervisorToken); err != nil {
						t.Fatalf("failed to unset %s: %v", EnvSupervisorToken, err)
					}
					t.Cleanup(func() {
						os.Setenv(EnvSupervisorToken, prev)
					})
				}
			},
			want: false,
		},
		{
			name: "set to non-empty",
			setup: func(t *testing.T) {
				t.Setenv(EnvSupervisorToken, "mock-supervisor-token")
			},
			want: true,
		},
		{
			name: "set to empty string",
			setup: func(t *testing.T) {
				t.Setenv(EnvSupervisorToken, "")
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			if got := Detect(); got != tc.want {
				t.Errorf("Detect() = %v, want %v", got, tc.want)
			}
		})
	}
}
