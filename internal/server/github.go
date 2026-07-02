package server

import (
	"encoding/json"
	"errors"
	"net/http"

	podiomgithub "github.com/Podiom/Podiom/internal/github"
)

type githubDevicePollRequest struct {
	DeviceCode string `json:"device_code"`
}

func (s *Server) handleGitHubStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.github.Status(r.Context()), nil)
}

func (s *Server) handleGitHubDeviceStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !localRequest(r) {
		http.Error(w, "GitHub authorization is only available from loopback clients", http.StatusForbidden)
		return
	}
	start, err := s.github.StartDevice(r.Context())
	writeJSON(w, start, err)
}

func (s *Server) handleGitHubDevicePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !localRequest(r) {
		http.Error(w, "GitHub authorization is only available from loopback clients", http.StatusForbidden)
		return
	}
	var req githubDevicePollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	res, err := s.github.PollDevice(r.Context(), req.DeviceCode)
	writeJSON(w, res, err)
}

func (s *Server) handleGitHubRepos(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	repos, err := s.github.ListRepos(r.Context())
	writeJSON(w, repos, err)
}

func writeGitHubProjectError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, podiomgithub.ErrConfirmationRequired) {
		http.Error(w, err.Error(), http.StatusConflict)
		return true
	}
	writeJSON(w, nil, err)
	return true
}
