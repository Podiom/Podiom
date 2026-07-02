package server

import (
	"net/http"

	"github.com/Podiom/Podiom/internal/config"
)

func (s *Server) handleProviderCapabilities(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	provider := config.Provider(r.URL.Query().Get("provider"))
	profile := r.URL.Query().Get("profile")
	refresh := r.URL.Query().Get("refresh") == "1" || r.URL.Query().Get("refresh") == "true"
	caps, err := s.core.ProviderCapabilities(r.Context(), provider, profile, refresh)
	writeJSON(w, caps, err)
}
