package server

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
)

// tinyPNG returns the bytes of a valid 2x2 PNG for upload tests.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 200, G: 80, B: 40, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestAgentAvatarRoundTrip(t *testing.T) {
	ctx := context.Background()
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	call := func(method string, body []byte) *httptest.ResponseRecorder {
		var r *http.Request
		if body != nil {
			r = httptest.NewRequest(method, "/api/agents/atlas/avatar", bytes.NewReader(body))
		} else {
			r = httptest.NewRequest(method, "/api/agents/atlas/avatar", nil)
		}
		rr := httptest.NewRecorder()
		srv.handleAgent(rr, r)
		return rr
	}

	// No picture yet → 404.
	if rr := call(http.MethodGet, nil); rr.Code != http.StatusNotFound {
		t.Fatalf("GET fresh avatar = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}

	// Upload a valid PNG.
	pngBytes := tinyPNG(t)
	postRR := call(http.MethodPost, pngBytes)
	if postRR.Code != http.StatusOK {
		t.Fatalf("POST avatar = %d, want 200; body=%s", postRR.Code, postRR.Body.String())
	}
	var stamp struct{ AvatarUpdatedAt string }
	if err := json.Unmarshal(postRR.Body.Bytes(), &stamp); err != nil {
		t.Fatalf("decode post response: %v", err)
	}
	if stamp.AvatarUpdatedAt == "" {
		t.Fatal("POST response AvatarUpdatedAt is empty")
	}

	// The version stamp reaches the agent record...
	agent, err := srv.core.GetAgent(ctx, "atlas")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if agent.AvatarUpdatedAt != stamp.AvatarUpdatedAt {
		t.Fatalf("agent stamp = %q, want %q", agent.AvatarUpdatedAt, stamp.AvatarUpdatedAt)
	}
	// ...and the bytes land on disk.
	if _, err := os.Stat(srv.core.AgentPaths("atlas").Avatar); err != nil {
		t.Fatalf("avatar file missing: %v", err)
	}

	// GET now serves the exact bytes as image/png.
	getRR := call(http.MethodGet, nil)
	if getRR.Code != http.StatusOK {
		t.Fatalf("GET avatar = %d, want 200", getRR.Code)
	}
	if ct := getRR.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", ct)
	}
	if !bytes.Equal(getRR.Body.Bytes(), pngBytes) {
		t.Fatalf("served bytes differ from uploaded bytes")
	}

	// A non-image body is rejected.
	if rr := call(http.MethodPost, []byte("this is not an image, just text")); rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("POST non-image = %d, want 415; body=%s", rr.Code, rr.Body.String())
	}

	// DELETE removes the picture and clears the stamp.
	if rr := call(http.MethodDelete, nil); rr.Code != http.StatusOK {
		t.Fatalf("DELETE avatar = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	agent, err = srv.core.GetAgent(ctx, "atlas")
	if err != nil {
		t.Fatalf("get agent after delete: %v", err)
	}
	if agent.AvatarUpdatedAt != "" {
		t.Fatalf("stamp after delete = %q, want empty", agent.AvatarUpdatedAt)
	}
	if _, err := os.Stat(srv.core.AgentPaths("atlas").Avatar); !os.IsNotExist(err) {
		t.Fatalf("avatar file should be gone, stat err = %v", err)
	}
	if rr := call(http.MethodGet, nil); rr.Code != http.StatusNotFound {
		t.Fatalf("GET after delete = %d, want 404", rr.Code)
	}
}
