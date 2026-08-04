package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
)

func TestPatchConfigUpdatesPermissionTimeout(t *testing.T) {
	paths, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewBufferString(`{"permission_timeout":"5m"}`))
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	srv.handleConfig(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got globalConfigDTO
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.PermissionTimeout != "5m" {
		t.Fatalf("response permission_timeout = %q, want 5m", got.PermissionTimeout)
	}
	if live := srv.core.GetGlobal().PermissionTimeout; live != "5m" {
		t.Fatalf("live permission_timeout = %q, want 5m", live)
	}
	cfg, err := config.Load(paths.ConfigYAML)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Global.PermissionTimeout != "5m" {
		t.Fatalf("persisted permission_timeout = %q, want 5m", cfg.Global.PermissionTimeout)
	}
}

// The chat-display preference has to survive the YAML round-trip both ways: on
// is written as a key, off removes it so the default stands.
func TestPatchConfigTogglesCollapseReasoning(t *testing.T) {
	paths, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	patch := func(body string) globalConfigDTO {
		t.Helper()
		req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewBufferString(body))
		req.RemoteAddr = "127.0.0.1:1234"
		rr := httptest.NewRecorder()
		srv.handleConfig(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		var got globalConfigDTO
		if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return got
	}
	persisted := func() bool {
		t.Helper()
		cfg, err := config.Load(paths.ConfigYAML)
		if err != nil {
			t.Fatalf("load config: %v", err)
		}
		return cfg.Global.CollapseReasoning
	}

	if got := patch(`{"collapse_reasoning":true}`); !got.CollapseReasoning {
		t.Fatal("response collapse_reasoning = false, want true")
	}
	if !srv.core.GetGlobal().CollapseReasoning {
		t.Fatal("live collapse_reasoning = false, want true")
	}
	if !persisted() {
		t.Fatal("persisted collapse_reasoning = false, want true")
	}

	// An unrelated patch must not disturb it.
	if got := patch(`{"model":"opus"}`); !got.CollapseReasoning {
		t.Fatal("collapse_reasoning was cleared by an unrelated patch")
	}

	if got := patch(`{"collapse_reasoning":false}`); got.CollapseReasoning {
		t.Fatal("response collapse_reasoning = true, want false")
	}
	if persisted() {
		t.Fatal("persisted collapse_reasoning = true, want false")
	}
}

func TestConfigVoiceKeySetClearAndMasking(t *testing.T) {
	paths, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	// Set the key.
	req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewBufferString(`{"voice":{"openai_api_key":"sk-test-secret"}}`))
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	srv.handleConfig(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("set key status = %d; body=%s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); strings.Contains(body, "sk-test-secret") {
		t.Fatalf("raw key leaked into PATCH response: %s", body)
	}
	var got globalConfigDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.Voice.OpenAIAPIKeySet {
		t.Fatalf("openai_api_key_set = false after set")
	}
	if live := srv.core.GetVoice().OpenAIAPIKey; live != "sk-test-secret" {
		t.Fatalf("live voice key = %q", live)
	}
	cfg, err := config.Load(paths.ConfigYAML)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Voice.OpenAIAPIKey != "sk-test-secret" {
		t.Fatalf("persisted voice key = %q", cfg.Voice.OpenAIAPIKey)
	}

	// GET must expose presence only, never the key.
	req = httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rr = httptest.NewRecorder()
	srv.handleConfig(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("get status = %d", rr.Code)
	}
	if body := rr.Body.String(); strings.Contains(body, "sk-test-secret") {
		t.Fatalf("raw key leaked into GET response: %s", body)
	}
	if !strings.Contains(rr.Body.String(), `"openai_api_key_set":true`) {
		t.Fatalf("GET should report key presence: %s", rr.Body.String())
	}

	// A patch omitting voice leaves the key untouched.
	req = httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewBufferString(`{"model":"opus"}`))
	req.RemoteAddr = "127.0.0.1:1234"
	rr = httptest.NewRecorder()
	srv.handleConfig(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unrelated patch status = %d; body=%s", rr.Code, rr.Body.String())
	}
	if live := srv.core.GetVoice().OpenAIAPIKey; live != "sk-test-secret" {
		t.Fatalf("unrelated patch clobbered voice key: %q", live)
	}

	// Empty string clears the key and drops the block from yaml.
	req = httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewBufferString(`{"voice":{"openai_api_key":""}}`))
	req.RemoteAddr = "127.0.0.1:1234"
	rr = httptest.NewRecorder()
	srv.handleConfig(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("clear key status = %d; body=%s", rr.Code, rr.Body.String())
	}
	if live := srv.core.GetVoice().OpenAIAPIKey; live != "" {
		t.Fatalf("voice key not cleared: %q", live)
	}
	cfg, err = config.Load(paths.ConfigYAML)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if cfg.Voice.OpenAIAPIKey != "" {
		t.Fatalf("persisted voice key not cleared: %q", cfg.Voice.OpenAIAPIKey)
	}
}

func TestPatchConfigRejectsWhitespaceVoiceKey(t *testing.T) {
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewBufferString(`{"voice":{"openai_api_key":"sk bad key"}}`))
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	srv.handleConfig(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestPatchConfigRejectsInvalidPermissionTimeout(t *testing.T) {
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewBufferString(`{"permission_timeout":"0s"}`))
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	srv.handleConfig(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}
