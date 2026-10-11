package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInterviewMCPExposesQuestionAndSubmitTools(t *testing.T) {
	tools := interviewMCPTools("127.0.0.1:8787", "session-1")
	if len(tools) != 2 {
		t.Fatalf("tool count = %d, want 2", len(tools))
	}
	if tools[0].Name != "podiom_ask_profile_question" || tools[1].Name != "podiom_submit_user_profile" {
		t.Fatalf("unexpected tools: %q, %q", tools[0].Name, tools[1].Name)
	}

	byName := map[string]mcpTool{tools[0].Name: tools[0], tools[1].Name: tools[1]}
	resp := dispatchStdioMCP(context.Background(), "podiom-interview", tools, byName, rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
	})
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal tools/list: %v", err)
	}
	for _, want := range []string{"identity_context", "technical_depth", "minItems", "maxItems"} {
		if !json.Valid(raw) || !containsBytes(raw, want) {
			t.Fatalf("tools/list missing %q: %s", want, raw)
		}
	}
}

func TestForwardInterviewCall(t *testing.T) {
	t.Run("requires session ID before making request", func(t *testing.T) {
		var called bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		}))
		t.Cleanup(srv.Close)

		addr := strings.TrimPrefix(srv.URL, "http://")
		_, err := forwardInterviewCall(context.Background(), addr, "", "questions", nil)
		if err == nil {
			t.Fatal("expected session ID error")
		}
		if err.Error() != "session id is required" {
			t.Fatalf("error = %q, want %q", err, "session id is required")
		}
		if called {
			t.Fatal("request was sent despite missing session ID")
		}
	})

	t.Run("sends empty args as JSON object", func(t *testing.T) {
		var gotMethod string
		var gotPath string
		var gotBody string
		var bodyErr error

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotMethod = r.Method
			gotPath = r.URL.Path

			body, err := io.ReadAll(r.Body)
			if err != nil {
				bodyErr = err
				return
			}
			gotBody = string(body)
		}))
		t.Cleanup(srv.Close)

		addr := strings.TrimPrefix(srv.URL, "http://")
		got, err := forwardInterviewCall(
			context.Background(),
			addr,
			"session-1",
			"questions",
			json.RawMessage(" \n\t "),
		)
		if err != nil {
			t.Fatalf("forwardInterviewCall() error = %v", err)
		}
		if bodyErr != nil {
			t.Fatalf("read request body: %v", bodyErr)
		}
		if got != "ok" {
			t.Fatalf("result = %q, want %q", got, "ok")
		}
		if gotMethod != http.MethodPost {
			t.Fatalf("method = %q, want %q", gotMethod, http.MethodPost)
		}
		if gotPath != "/api/interviews/session-1/questions" {
			t.Fatalf("path = %q, want %q", gotPath, "/api/interviews/session-1/questions")
		}
		if gotBody != "{}" {
			t.Fatalf("body = %q, want %q", gotBody, "{}")
		}
	})

	t.Run("reports non-200 status and body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTeapot)
			_, _ = io.WriteString(w, "interview failed")
		}))
		t.Cleanup(srv.Close)

		addr := strings.TrimPrefix(srv.URL, "http://")
		_, err := forwardInterviewCall(
			context.Background(),
			addr,
			"session-1",
			"questions",
			json.RawMessage(`{"question":"hello"}`),
		)
		if err == nil {
			t.Fatal("expected non-200 response error")
		}
		if !strings.Contains(err.Error(), "418") {
			t.Fatalf("error = %q, want HTTP status 418", err)
		}
		if !strings.Contains(err.Error(), "interview failed") {
			t.Fatalf("error = %q, want response body", err)
		}
	})

	t.Run("returns ok for blank 200 response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)

		addr := strings.TrimPrefix(srv.URL, "http://")
		got, err := forwardInterviewCall(
			context.Background(),
			addr,
			"session-1",
			"draft",
			json.RawMessage(`{"identity_context":[]}`),
		)
		if err != nil {
			t.Fatalf("forwardInterviewCall() error = %v", err)
		}
		if got != "ok" {
			t.Fatalf("result = %q, want %q", got, "ok")
		}
	})
}

func containsBytes(raw []byte, want string) bool {
	for i := 0; i+len(want) <= len(raw); i++ {
		if string(raw[i:i+len(want)]) == want {
			return true
		}
	}
	return false
}
