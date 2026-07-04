package server

import (
	"net/http"
	"time"

	"github.com/Podiom/Podiom/internal/onboardstate"
)

type onboardingResponse struct {
	Completed   bool   `json:"completed"`
	CompletedAt string `json:"completed_at,omitempty"`
}

func (s *Server) handleOnboardingState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.haMode {
		http.NotFound(w, r)
		return
	}
	st, err := onboardstate.Read(s.paths.Onboarding)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, onboardingStateResponse(st), nil)
}

func (s *Server) handleOnboardingComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.haMode {
		http.NotFound(w, r)
		return
	}
	st, err := onboardstate.MarkComplete(s.paths.Onboarding, time.Now())
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, onboardingStateResponse(st), nil)
}

func (s *Server) handleOnboardingToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.haMode {
		http.NotFound(w, r)
		return
	}
	st, err := onboardstate.Read(s.paths.Onboarding)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	if !st.Completed {
		http.Error(w, "onboarding is not complete", http.StatusForbidden)
		return
	}
	if s.tokens == nil {
		http.Error(w, "gateway token disabled", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, map[string]string{"token": s.tokens.Current()}, nil)
}

func onboardingStateResponse(st onboardstate.State) onboardingResponse {
	out := onboardingResponse{Completed: st.Completed}
	if !st.CompletedAt.IsZero() {
		out.CompletedAt = st.CompletedAt.UTC().Format(time.RFC3339)
	}
	return out
}
