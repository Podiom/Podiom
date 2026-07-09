package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/claudeauth"
)

const testModelsToken = "sk-ant-oat-MODELS-SECRET-do-not-log"

// writeClaudeModelsCreds writes a non-expired .credentials.json into a temp dir.
func writeClaudeModelsCreds(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	payload := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":      testModelsToken,
			"expiresAt":        time.Now().Add(time.Hour).UnixMilli(),
			"scopes":           []string{"user:profile"},
			"subscriptionType": "max",
		},
	}
	raw, _ := json.Marshal(payload)
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestParseClaudeModelList(t *testing.T) {
	raw := []byte(`{
		"data": [
			{"type":"model","id":"claude-opus-4-8","display_name":"Claude Opus 4.8"},
			{"type":"model","id":"claude-fable-5","display_name":"Claude Fable 5"},
			{"type":"model","id":"","display_name":"skip me"},
			{"type":"model","id":"claude-haiku-4-5"}
		],
		"has_more": true,
		"last_id": "claude-haiku-4-5"
	}`)
	models, hasMore, lastID, err := parseClaudeModelList(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 {
		t.Fatalf("models = %d, want 3 (empty id skipped): %+v", len(models), models)
	}
	if !hasMore || lastID != "claude-haiku-4-5" {
		t.Fatalf("hasMore=%v lastID=%q", hasMore, lastID)
	}
	if models[0].Model != "claude-opus-4-8" || models[0].DisplayName != "Claude Opus 4.8" {
		t.Errorf("model[0] = %+v", models[0])
	}
	if models[0].ID != models[0].Model {
		t.Errorf("ID should mirror Model: %+v", models[0])
	}
	// display_name absent -> falls back to id.
	if models[2].DisplayName != "claude-haiku-4-5" {
		t.Errorf("model[2] display fallback = %q", models[2].DisplayName)
	}
}

func TestFetchClaudeModelsPaginatesAndAuth(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+testModelsToken {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("anthropic-beta"); got != claudeauth.OAuthBeta {
			t.Errorf("anthropic-beta = %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != claudeAPIVersion {
			t.Errorf("anthropic-version = %q", got)
		}
		calls++
		if r.URL.Query().Get("after_id") == "" {
			fmt.Fprint(w, `{"data":[{"id":"claude-sonnet-5","display_name":"Claude Sonnet 5"}],"has_more":true,"last_id":"claude-sonnet-5"}`)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"claude-opus-4-8","display_name":"Claude Opus 4.8"}],"has_more":false,"last_id":"claude-opus-4-8"}`)
	}))
	defer srv.Close()
	restore := claudeModelsURL
	claudeModelsURL = srv.URL
	defer func() { claudeModelsURL = restore }()

	dir := writeClaudeModelsCreds(t)
	models, err := fetchClaudeModels(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("server calls = %d, want 2 (paginated)", calls)
	}
	if len(models) != 2 {
		t.Fatalf("models = %d, want 2: %+v", len(models), models)
	}
	// Opus should be flagged default even though it is not first.
	var defaulted string
	for _, m := range models {
		if m.IsDefault {
			defaulted = m.Model
		}
	}
	if defaulted != "claude-opus-4-8" {
		t.Errorf("default model = %q, want claude-opus-4-8", defaulted)
	}
}

func TestFetchClaudeModelsFallbackOnStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	restore := claudeModelsURL
	claudeModelsURL = srv.URL
	defer func() { claudeModelsURL = restore }()

	dir := writeClaudeModelsCreds(t)
	if _, err := fetchClaudeModels(context.Background(), dir); err == nil {
		t.Fatal("expected error on 401 so caller falls back to catalogue")
	}
}

func TestFetchClaudeModelsNoToken(t *testing.T) {
	if _, err := fetchClaudeModels(context.Background(), t.TempDir()); err == nil {
		t.Fatal("expected error when no credentials present")
	}
}
