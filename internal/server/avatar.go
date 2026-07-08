package server

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
)

// maxAvatarBytes caps an uploaded profile picture. The client normalizes images
// to a small 256x256 PNG before upload, so this only guards against abuse.
const maxAvatarBytes = 512 << 10 // 512 KiB

// handleAgentAvatar serves an agent's uploaded profile picture:
//
//	GET    /api/agents/{name}/avatar  → the PNG bytes (404 if none uploaded)
//	POST   /api/agents/{name}/avatar  → replace it (raw image body)
//	DELETE /api/agents/{name}/avatar  → remove it (revert to derived monogram)
//
// The bytes are fetched by the SPA through the authed request helper and turned
// into an object URL, because /api is token-gated and a plain <img src> cannot
// carry the token.
func (s *Server) handleAgentAvatar(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet:
		data, err := s.core.ReadAgentAvatar(name)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				http.Error(w, "no avatar", http.StatusNotFound)
				return
			}
			writeJSON(w, nil, err)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(data)

	case http.MethodPost, http.MethodPut:
		// The agent must exist — its name defines the storage path.
		if _, err := s.core.GetAgent(r.Context(), name); err != nil {
			writeJSON(w, nil, err)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxAvatarBytes)
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "image too large (max 512 KiB)", http.StatusRequestEntityTooLarge)
			return
		}
		if !strings.HasPrefix(http.DetectContentType(data), "image/") {
			http.Error(w, "uploaded file is not an image", http.StatusUnsupportedMediaType)
			return
		}
		if err := s.core.SetAgentAvatar(r.Context(), name, data); err != nil {
			writeJSON(w, nil, err)
			return
		}
		agent, err := s.core.GetAgent(r.Context(), name)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		s.log.Info("agent avatar updated", "event", "agent", "agent", name)
		writeJSON(w, map[string]string{"AvatarUpdatedAt": agent.AvatarUpdatedAt}, nil)

	case http.MethodDelete:
		if err := s.core.DeleteAgentAvatar(r.Context(), name); err != nil {
			writeJSON(w, nil, err)
			return
		}
		s.log.Info("agent avatar removed", "event", "agent", "agent", name)
		writeJSON(w, map[string]string{"AvatarUpdatedAt": ""}, nil)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
