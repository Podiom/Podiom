package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
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

func TestForwardPermissionRoutesAskUserQuestionToUserInput(t *testing.T) {
	requests := make(chan adapter.UserInputRequest, 1)
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/user-input-requests/turn-1" {
				t.Errorf("path = %q, want user-input relay", r.URL.Path)
			}
			var req adapter.UserInputRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			requests <- req
			_ = json.NewEncoder(w).Encode(adapter.UserInputDecision{Answers: map[string][]string{
				"q1": {"Stash:a dem"},
			}})
		}),
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Shutdown(context.Background())

	question := "Vad ska hända med de ohanterade ändringarna innan jag byter till master?"
	input := map[string]any{
		"questions": []map[string]any{{
			"question":    question,
			"header":      "Lokala ändringar",
			"multiSelect": false,
			"options": []map[string]string{
				{"label": "Släng dem", "description": "Börja rent."},
				{"label": "Stash:a dem", "description": "Spara undan säkert."},
				{"label": "Committa dem först", "description": "Bevara i historiken."},
			},
		}},
	}
	params, _ := json.Marshal(map[string]any{"arguments": map[string]any{
		"tool_name":   "AskUserQuestion",
		"tool_use_id": "toolu-question",
		"input":       input,
	}})
	decision, err := forwardPermission(context.Background(), ln.Addr().String(), "turn-1", time.Second, params)
	if err != nil {
		t.Fatalf("forward question: %v", err)
	}
	if decision.Behavior != "allow" {
		t.Fatalf("decision = %+v, want allow", decision)
	}
	req := <-requests
	if req.Provider != "" || req.ItemID != "toolu-question" || req.EndsTurn {
		t.Fatalf("bad relayed request: %+v", req)
	}
	if len(req.Questions) != 1 || req.Questions[0].ID != "q1" || req.Questions[0].MultiSelect {
		t.Fatalf("bad normalized questions: %+v", req.Questions)
	}
	var updated struct {
		Questions []adapter.UserInputQuestion `json:"questions"`
		Answers   map[string]string           `json:"answers"`
	}
	if err := json.Unmarshal(decision.UpdatedInput, &updated); err != nil {
		t.Fatalf("decode updated input: %v", err)
	}
	if len(updated.Questions) != 1 || updated.Answers[question] != "Stash:a dem" {
		t.Fatalf("updated input = %+v", updated)
	}
}

func TestForwardPermissionFormatsAskUserQuestionMultiSelect(t *testing.T) {
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(adapter.UserInputDecision{Answers: map[string][]string{
				"choices": {"A", "B"},
			}})
		}),
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Shutdown(context.Background())

	params, _ := json.Marshal(map[string]any{"arguments": map[string]any{
		"tool_name": "AskUserQuestion",
		"input": map[string]any{"questions": []map[string]any{{
			"id": "choices", "question": "Pick", "multiSelect": true,
		}}},
	}})
	decision, err := forwardPermission(context.Background(), ln.Addr().String(), "turn-1", time.Second, params)
	if err != nil {
		t.Fatalf("forward question: %v", err)
	}
	if !strings.Contains(string(decision.UpdatedInput), `"Pick":"A, B"`) {
		t.Fatalf("updated input = %s", decision.UpdatedInput)
	}
}

func TestForwardPermissionDeniesInvalidOrUnansweredAskUserQuestion(t *testing.T) {
	t.Run("invalid payload never reaches approval relay", func(t *testing.T) {
		params, _ := json.Marshal(map[string]any{"arguments": map[string]any{
			"tool_name": "AskUserQuestion",
			"input":     map[string]any{},
		}})
		decision, err := forwardPermission(context.Background(), "127.0.0.1:1", "turn-1", time.Millisecond, params)
		if err != nil || decision.Behavior != "deny" || !strings.Contains(decision.Message, "questions are required") {
			t.Fatalf("decision = %+v, err = %v", decision, err)
		}
	})

	t.Run("empty answer is denied", func(t *testing.T) {
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(adapter.UserInputDecision{Answers: map[string][]string{}})
		})}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		go func() { _ = srv.Serve(ln) }()
		defer srv.Shutdown(context.Background())

		params, _ := json.Marshal(map[string]any{"arguments": map[string]any{
			"tool_name": "AskUserQuestion",
			"input": map[string]any{"questions": []map[string]any{{
				"question": "Pick one",
			}}},
		}})
		decision, err := forwardPermission(context.Background(), ln.Addr().String(), "turn-1", time.Second, params)
		if err != nil || decision.Behavior != "deny" || !strings.Contains(decision.Message, "not answered") {
			t.Fatalf("decision = %+v, err = %v", decision, err)
		}
	})
}
