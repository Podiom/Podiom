package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Podiom/Podiom/internal/core"
)

// userProfileInfo is the view of the app-wide USER.md returned to the UI.
type userProfileInfo struct {
	Exists  bool   `json:"exists"`
	Profile string `json:"profile"`
}

type userProfileUpdateRequest struct {
	Profile string `json:"profile"`
}

// handleUserProfile serves GET/PUT/DELETE on /api/user-profile — the app-wide
// USER.md injected into every agent's context.
func (s *Server) handleUserProfile(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		info, err := s.userProfileInfo()
		writeJSON(w, info, err)
	case http.MethodPut, http.MethodPatch:
		var req userProfileUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Profile) == "" {
			http.Error(w, "profile is empty", http.StatusBadRequest)
			return
		}
		cleaned := core.CleanUserProfileMarkdown(req.Profile)
		if err := s.core.WriteUserProfile(cleaned); err != nil {
			writeJSON(w, nil, err)
			return
		}
		info, err := s.userProfileInfo()
		writeJSON(w, info, err)
	case http.MethodDelete:
		if err := s.core.DeleteUserProfile(); err != nil {
			writeJSON(w, nil, err)
			return
		}
		writeJSON(w, userProfileInfo{}, nil)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) userProfileInfo() (userProfileInfo, error) {
	profile, err := s.core.ReadUserProfile()
	if err != nil {
		return userProfileInfo{}, err
	}
	return userProfileInfo{Exists: strings.TrimSpace(profile) != "", Profile: profile}, nil
}
