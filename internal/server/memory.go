package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/store"
)

// memoryInfo is the view of an agent's memory returned to the UI/CLI. The memory
// body is sensitive (MEM22) but is served over the loopback API to the same
// single user who owns it; it is never logged.
type memoryInfo struct {
	Agent           string       `json:"agent"`
	Memory          string       `json:"memory"`
	Lines           int          `json:"lines"`
	BudgetLines     int          `json:"budget_lines"`
	PendingSessions int          `json:"pending_sessions"`
	LastDream       *store.Dream `json:"last_dream"`
}

type memoryUpdateRequest struct {
	Memory string `json:"memory"`
}

// memoryStatusRow is one agent's line in the fleet-wide memory status list.
type memoryStatusRow struct {
	Agent           string       `json:"agent"`
	PendingSessions int          `json:"pending_sessions"`
	MemoryLines     int          `json:"memory_lines"`
	BudgetLines     int          `json:"budget_lines"`
	LastDream       *store.Dream `json:"last_dream"`
}

// handleAgentSubresource routes /api/agents/{name}/{sub}: memory, dreams, dream.
func (s *Server) handleAgentSubresource(w http.ResponseWriter, r *http.Request, name, sub string) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	switch sub {
	case "memory":
		s.handleAgentMemory(w, r, name)
	case "dreams":
		s.handleAgentDreams(w, r, name)
	case "dream":
		s.handleAgentDream(w, r, name)
	default:
		http.Error(w, "unknown agent sub-resource", http.StatusNotFound)
	}
}

// handleAgentMemory serves GET/PUT/DELETE on an agent's MEMORY.md.
func (s *Server) handleAgentMemory(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet:
		info, err := s.memoryInfo(r, name)
		writeJSON(w, info, err)
	case http.MethodPut, http.MethodPatch:
		var req memoryUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.core.WriteAgentMemory(name, req.Memory); err != nil {
			writeJSON(w, nil, err)
			return
		}
		info, err := s.memoryInfo(r, name)
		writeJSON(w, info, err)
	case http.MethodDelete:
		if err := s.core.ClearAgentMemory(name); err != nil {
			writeJSON(w, nil, err)
			return
		}
		info, err := s.memoryInfo(r, name)
		writeJSON(w, info, err)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleAgentDreams serves GET /api/agents/{name}/dreams — the dream journal.
func (s *Server) handleAgentDreams(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 30
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	dreams, err := s.core.Store().ListDreams(r.Context(), name, limit)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	if dreams == nil {
		dreams = []store.Dream{}
	}
	writeJSON(w, dreams, nil)
}

// dreamResponse is the reply to a manual dream trigger.
type dreamResponse struct {
	NoOp  bool         `json:"noop"`
	Dream *store.Dream `json:"dream"`
}

// handleAgentDream serves POST /api/agents/{name}/dream — run a dream on demand.
func (s *Server) handleAgentDream(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	res, err := s.core.DreamAgent(r.Context(), name, core.DreamOptions{Trigger: store.DreamManual})
	if err != nil {
		if errors.Is(err, core.ErrDreamInProgress) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, nil, err)
		return
	}
	if res.NoOp {
		writeJSON(w, dreamResponse{NoOp: true}, nil)
		return
	}
	writeJSON(w, dreamResponse{Dream: &res.Dream}, nil)
}

// handleMemoryStatus serves GET /api/memory/status — a compact per-agent summary.
func (s *Server) handleMemoryStatus(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	agents, err := s.core.ListAgents(r.Context())
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	rows := make([]memoryStatusRow, 0, len(agents))
	for _, agent := range agents {
		st, err := s.core.MemoryStatus(r.Context(), agent.Name)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		rows = append(rows, memoryStatusRow{
			Agent:           agent.Name,
			PendingSessions: st.PendingSessions,
			MemoryLines:     st.MemoryLines,
			BudgetLines:     st.BudgetLines,
			LastDream:       st.LastDream,
		})
	}
	writeJSON(w, rows, nil)
}

// memoryInfo builds the single-agent memory view.
func (s *Server) memoryInfo(r *http.Request, name string) (memoryInfo, error) {
	status, err := s.core.MemoryStatus(r.Context(), name)
	if err != nil {
		return memoryInfo{}, err
	}
	memory, err := s.core.ReadAgentMemory(name)
	if err != nil {
		return memoryInfo{}, err
	}
	return memoryInfo{
		Agent:           name,
		Memory:          memory,
		Lines:           status.MemoryLines,
		BudgetLines:     status.BudgetLines,
		PendingSessions: status.PendingSessions,
		LastDream:       status.LastDream,
	}, nil
}
