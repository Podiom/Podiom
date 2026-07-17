package server

import (
	"net/http"
	"strings"
)

// credentialView is the outward projection of a stored credential. It has no
// value field on purpose: the secret never leaves the daemon.
type credentialView struct {
	Name      string `json:"name"`
	Purpose   string `json:"purpose,omitempty"`
	GoalID    string `json:"goal_id,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// handleCredentials handles GET /api/credentials — the names-only listing for
// the Settings page.
func (s *Server) handleCredentials(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	list, err := s.core.ListCredentials(r.Context())
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	out := make([]credentialView, 0, len(list))
	for _, c := range list {
		out = append(out, credentialView{Name: c.Name, Purpose: c.Purpose, GoalID: c.GoalID, CreatedAt: c.CreatedAt})
	}
	writeJSON(w, out, nil)
}

// handleCredential handles DELETE /api/credentials/{name}.
func (s *Server) handleCredential(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/credentials/")
	if name == "" {
		http.Error(w, "credential name is required", http.StatusBadRequest)
		return
	}
	if err := s.core.DeleteCredential(r.Context(), name); err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, map[string]string{"status": "deleted"}, nil)
}
