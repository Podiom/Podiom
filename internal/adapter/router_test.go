package adapter

import (
	"errors"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
)

func TestRouterAdapter(t *testing.T) {
	registered := Unavailable{Provider: config.ProviderClaude}
	router := NewRouter(map[config.Provider]Adapter{config.ProviderClaude: registered})

	tests := []struct {
		name     string
		router   *Router
		provider config.Provider
		want     Adapter
		wantErr  string
	}{
		{name: "registered provider", router: router, provider: config.ProviderClaude, want: registered},
		{name: "empty provider", router: router, provider: "", wantErr: "provider is required"},
		{name: "nil router with empty provider reports required first", router: nil, provider: "", wantErr: "provider is required"},
		{name: "nil router", router: nil, provider: config.ProviderClaude, wantErr: "claude adapter unavailable"},
		{name: "unregistered provider", router: router, provider: config.ProviderCodex, wantErr: "codex adapter unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.router.adapter(tt.provider)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("adapter(%q) error = %v, want %q", tt.provider, err, tt.wantErr)
				}
				if got != nil {
					t.Fatalf("adapter(%q) = %v, want nil on error", tt.provider, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("adapter(%q) error = %v", tt.provider, err)
			}
			if got != tt.want {
				t.Fatalf("adapter(%q) = %v, want %v", tt.provider, got, tt.want)
			}
		})
	}
}

func TestUnavailableErr(t *testing.T) {
	cause := errors.New("cli not found")
	tests := []struct {
		name      string
		err       error
		want      string
		wantCause bool
	}{
		{name: "wraps cause", err: cause, want: "codex adapter unavailable: cli not found", wantCause: true},
		{name: "no cause", err: nil, want: "codex adapter unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Unavailable{Provider: config.ProviderCodex, Err: tt.err}.err()
			if got == nil || got.Error() != tt.want {
				t.Fatalf("err() = %v, want %q", got, tt.want)
			}
			if tt.wantCause {
				if !errors.Is(got, cause) {
					t.Fatalf("errors.Is(err, cause) = false, want true")
				}
				if errors.Unwrap(got) != cause {
					t.Fatalf("errors.Unwrap(err) = %v, want %v", errors.Unwrap(got), cause)
				}
			} else if errors.Unwrap(got) != nil {
				t.Fatalf("errors.Unwrap(err) = %v, want nil", errors.Unwrap(got))
			}
		})
	}
}
