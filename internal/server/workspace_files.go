package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Podiom/Podiom/internal/store"
)

type workspaceFileCreateRequest struct {
	SessionID string `json:"session_id"`
	Path      string `json:"path"`
	Label     string `json:"label"`
}

// handleWorkspaceFiles snapshots one file for an agent-authored link.
func (s *Server) handleWorkspaceFiles(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req workspaceFileCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result, err := s.core.SnapshotWorkspaceFile(r.Context(), req.SessionID, req.Path, req.Label)
	writeJSON(w, result, err)
}

// handleWorkspaceFile serves the immutable content used by the dashboard's
// authenticated in-app viewer.
func (s *Server) handleWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/workspace-files/"), "/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	snapshot, err := s.core.GetWorkspaceFileSnapshot(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, snapshot, nil)
}
