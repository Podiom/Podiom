package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Session-scoped project endpoints backing the podiom_project MCP helper.
//
// The session id is in the path rather than a request body because the helper
// is launched with it fixed: the agent cannot address a project other than the
// one its session is bound to.
func (s *Server) handleSessionProject(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/session-project/")
	sessionID, action, _ := strings.Cut(rest, "/")
	if strings.TrimSpace(sessionID) == "" {
		http.Error(w, "session id is required", http.StatusBadRequest)
		return
	}

	switch action {
	case "", "context":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx, err := s.core.SessionProjectContext(r.Context(), sessionID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, ctx, nil)
	case "start-work":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Kind string `json:"kind"`
			Slug string `json:"slug"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := s.core.StartWork(r.Context(), sessionID, body.Kind, body.Slug)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, result, nil)
	default:
		http.NotFound(w, r)
	}
}

// handleGitStatus reports host git readiness for the Settings card.
func (s *Server) handleGitStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.core.GitStatus(r.Context()), nil)
}

// handleGitIdentity saves the commit identity Podiom's agents will use.
func (s *Server) handleGitIdentity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.core.SetGitIdentity(r.Context(), body.Name, body.Email); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, s.core.GitStatus(r.Context()), nil)
}
