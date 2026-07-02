package server

import (
	"context"
	"net/http"
	"time"

	podiomlog "github.com/Podiom/Podiom/internal/logging"
)

// handleUsage serves per-profile provider usage snapshots.
//
//	GET /api/usage            -> cached snapshots (cheap; served from memory)
//	GET /api/usage?refresh=1  -> force a live re-fetch (bounded to ~15s)
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	if s.usage == nil {
		http.Error(w, "usage tracker unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Query().Get("refresh") == "1" {
		started := time.Now()
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		snaps := s.usage.Refresh(ctx, true)
		s.log.Info("usage refresh requested", "event", "usage", "stage", "refresh",
			"remote_addr", r.RemoteAddr, podiomlog.DurationMS("duration_ms", time.Since(started)))
		writeJSON(w, snaps, nil)
		return
	}
	writeJSON(w, s.usage.Snapshots(), nil)
}
