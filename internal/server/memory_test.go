package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
)

func TestMemoryGetPutClearRoundTrip(t *testing.T) {
	ctx := context.Background()
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	// GET on a fresh agent returns empty memory with the budget populated.
	getReq := httptest.NewRequest(http.MethodGet, "/api/agents/atlas/memory", nil)
	getRR := httptest.NewRecorder()
	srv.handleAgent(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body=%s", getRR.Code, getRR.Body.String())
	}
	var info memoryInfo
	if err := json.Unmarshal(getRR.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode memory info: %v", err)
	}
	if info.Memory != "" || info.BudgetLines == 0 {
		t.Fatalf("unexpected fresh memory info: %+v", info)
	}

	// PUT a body, then confirm it round-trips through the store.
	putReq := httptest.NewRequest(http.MethodPut, "/api/agents/atlas/memory",
		bytes.NewBufferString(`{"memory":"# Memory\n\n- kept item\n"}`))
	putRR := httptest.NewRecorder()
	srv.handleAgent(putRR, putReq)
	if putRR.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200; body=%s", putRR.Code, putRR.Body.String())
	}
	stored, err := srv.core.ReadAgentMemory("atlas")
	if err != nil {
		t.Fatalf("read memory: %v", err)
	}
	if stored != "# Memory\n\n- kept item\n" {
		t.Fatalf("stored memory = %q", stored)
	}

	// DELETE clears it.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/agents/atlas/memory", nil)
	delRR := httptest.NewRecorder()
	srv.handleAgent(delRR, delReq)
	if delRR.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200; body=%s", delRR.Code, delRR.Body.String())
	}
	cleared, _ := srv.core.ReadAgentMemory("atlas")
	if cleared != "" {
		t.Fatalf("memory should be cleared, got %q", cleared)
	}
}

func TestMemoryStatusListsAgents(t *testing.T) {
	ctx := context.Background()
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/memory/status", nil)
	rr := httptest.NewRecorder()
	srv.handleMemoryStatus(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var rows []memoryStatusRow
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 1 || rows[0].Agent != "atlas" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if rows[0].LastDream != nil {
		t.Fatalf("a never-dreamed agent should have no last dream")
	}
}

func TestDreamEndpointNoOpWithNoSessions(t *testing.T) {
	ctx := context.Background()
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/agents/atlas/dream", nil)
	rr := httptest.NewRecorder()
	srv.handleAgent(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var res dreamResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !res.NoOp {
		t.Fatalf("expected no-op dream with no sessions, got %+v", res)
	}
}
