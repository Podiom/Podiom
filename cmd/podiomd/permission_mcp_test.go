package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/gateway"
)

func TestForwardPermissionExtractsDescription(t *testing.T) {
	tests := []struct {
		name      string
		arguments map[string]any
		want      string
	}{
		{
			name: "top-level description",
			arguments: map[string]any{
				"tool_name":   "Bash",
				"tool_use_id": "toolu-1",
				"description": "Run test counter",
				"input":       map[string]any{"command": "npm test"},
			},
			want: "Run test counter",
		},
		{
			name: "input description",
			arguments: map[string]any{
				"tool_name":   "Bash",
				"tool_use_id": "toolu-1",
				"input": map[string]any{
					"description": "Run test counter",
					"command":     "npm test",
				},
			},
			want: "Run test counter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqs := make(chan adapter.PermissionRequest, 1)
			srv := &http.Server{
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var req adapter.PermissionRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Errorf("decode request: %v", err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					reqs <- req
					_ = json.NewEncoder(w).Encode(adapter.PermissionDecision{Behavior: "allow"})
				}),
			}
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			go func() { _ = srv.Serve(ln) }()
			defer srv.Shutdown(context.Background())

			params, _ := json.Marshal(map[string]any{"arguments": tt.arguments})
			decision, err := forwardPermission(context.Background(), ln.Addr().String(), "turn-1", time.Second, params)
			if err != nil {
				t.Fatalf("forward permission: %v", err)
			}
			if decision.Behavior != "allow" {
				t.Fatalf("bad decision: %+v", decision)
			}
			req := <-reqs
			if req.Description != tt.want {
				t.Fatalf("description = %q, want %q", req.Description, tt.want)
			}
		})
	}
}

func TestForwardPermissionSendsGatewayToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	if err := os.WriteFile(config.NewPaths(home).GatewayToken, []byte("permission-token\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	tokens := make(chan string, 1)
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokens <- r.Header.Get(gateway.Header)
			_ = json.NewEncoder(w).Encode(adapter.PermissionDecision{Behavior: "allow"})
		}),
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Shutdown(context.Background())

	params, _ := json.Marshal(map[string]any{"arguments": map[string]any{"tool_name": "Bash"}})
	if _, err := forwardPermission(context.Background(), ln.Addr().String(), "turn-1", time.Second, params); err != nil {
		t.Fatalf("forward permission: %v", err)
	}
	if got := <-tokens; got != "permission-token" {
		t.Fatalf("gateway token header = %q, want permission-token", got)
	}
}
