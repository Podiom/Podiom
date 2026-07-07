package server

import (
	"net/http"

	podiomtools "github.com/Podiom/Podiom/internal/tools"
)

// handleAgentTools serves the workspace-tool surface of one agent
// (workspace-tool-installs spec §5):
//
//	GET    /api/agents/{name}/tools         → manifest with per-entry health
//	DELETE /api/agents/{name}/tools/{tool}  → uninstall + manifest removal
func (s *Server) handleAgentTools(w http.ResponseWriter, r *http.Request, name, tool string) {
	// The agent must exist — its name defines the tool directory.
	if _, err := s.core.GetAgent(r.Context(), name); err != nil {
		writeJSON(w, nil, err)
		return
	}
	root := s.core.AgentPaths(name).Tools

	switch {
	case tool == "" && r.Method == http.MethodGet:
		list, err := podiomtools.List(root)
		if list == nil {
			list = []podiomtools.ToolStatus{}
		}
		writeJSON(w, list, err)
	case tool != "" && r.Method == http.MethodDelete:
		if err := podiomtools.Uninstall(r.Context(), root, tool); err != nil {
			writeJSON(w, nil, err)
			return
		}
		s.log.Info("workspace tool removed", "event", "goal", "agent", name, "tool", tool)
		writeJSON(w, map[string]string{"status": "removed", "agent": name, "tool": tool}, nil)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
