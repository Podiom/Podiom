package server

import (
	"net/http"

	"github.com/Podiom/Podiom/internal/skills"
)

// handleSkills serves the deduplicated skill catalogue the dashboard reads. The
// full SKILL.md bodies are included inline (everything is local and small, and
// the UI reveals them on expand without a second round-trip).
func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	catalogue, err := skills.Scan()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	if catalogue == nil {
		catalogue = []skills.Skill{}
	}
	writeJSON(w, catalogue, nil)
}

// handleSkillsRelink rebuilds the ~/.agents/skills union on demand (S16).
func (s *Server) handleSkillsRelink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rep, err := skills.Relink()
	writeJSON(w, rep, err)
}
