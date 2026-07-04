package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	podiommcp "github.com/Podiom/Podiom/internal/mcp"
	"github.com/Podiom/Podiom/internal/store"
)

type mcpSnapshot struct {
	Servers     []podiommcp.Server  `json:"servers"`
	Agents      []mcpAgent          `json:"agents"`
	Assignments map[string][]string `json:"assignments"`
}

type mcpAgent struct {
	Name       string   `json:"name"`
	Provider   string   `json:"provider"`
	MCPServers []string `json:"mcp_servers"`
}

type mcpAssignmentRequest struct {
	AgentName  string `json:"agent_name"`
	ServerName string `json:"server_name"`
	Assigned   bool   `json:"assigned"`
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	snapshot, err := s.mcpSnapshot(r.Context())
	writeJSON(w, snapshot, err)
}

func (s *Server) handleMCPServers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req podiommcp.Server
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := podiommcp.UpsertUserServer(s.paths.MCPYAML, req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	s.log.Info("mcp server upserted",
		"event", "mcp",
		"server", req.Name,
		"transport", string(req.Transport),
		"command_set", strings.TrimSpace(req.Command) != "",
	)
	snapshot, err := s.mcpSnapshot(r.Context())
	writeJSON(w, snapshot, err)
}

func (s *Server) handleMCPServer(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/mcp/servers/")
	if unescaped, err := url.PathUnescape(name); err == nil {
		name = unescaped
	}
	if name == "" {
		http.Error(w, "mcp server name is required", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodDelete:
		if err := podiommcp.RemoveUserServer(s.paths.MCPYAML, name); err != nil {
			writeJSON(w, nil, err)
			return
		}
		s.log.Info("mcp server deleted", "event", "mcp", "server", name)
		snapshot, err := s.mcpSnapshot(r.Context())
		writeJSON(w, snapshot, err)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleMCPAssignments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPatch {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req mcpAssignmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	agent, err := s.core.GetAgent(r.Context(), req.AgentName)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	cat, err := podiommcp.LoadCatalogue(s.paths.MCPYAML)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	if req.Assigned {
		if _, err := podiommcp.Assigned(cat, []string{req.ServerName}); err != nil {
			writeJSON(w, nil, err)
			return
		}
		agent.MCPServers = addString(agent.MCPServers, req.ServerName)
	} else {
		agent.MCPServers = removeString(agent.MCPServers, req.ServerName)
	}
	if _, err := s.core.UpdateAgent(r.Context(), agent); err != nil {
		writeJSON(w, nil, err)
		return
	}
	s.log.Info("mcp assignment updated",
		"event", "mcp",
		"agent", req.AgentName,
		"server", req.ServerName,
		"assigned", req.Assigned,
		"mcp_servers", len(agent.MCPServers),
	)
	snapshot, err := s.mcpSnapshot(r.Context())
	writeJSON(w, snapshot, err)
}

func (s *Server) mcpSnapshot(ctx context.Context) (mcpSnapshot, error) {
	cat, err := podiommcp.LoadCatalogue(s.paths.MCPYAML)
	if err != nil {
		return mcpSnapshot{}, err
	}
	agents, err := s.core.ListAgents(ctx)
	if err != nil {
		return mcpSnapshot{}, err
	}
	out := mcpSnapshot{
		Servers:     cat.Servers,
		Assignments: map[string][]string{},
	}
	for _, a := range agents {
		out.Agents = append(out.Agents, agentMCP(a))
		out.Assignments[a.Name] = append([]string(nil), a.MCPServers...)
	}
	return out, nil
}

func agentMCP(a store.Agent) mcpAgent {
	return mcpAgent{
		Name:       a.Name,
		Provider:   string(a.Provider),
		MCPServers: append([]string(nil), a.MCPServers...),
	}
}

func addString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, v := range values {
		if v == value {
			return values
		}
	}
	return append(values, value)
}

func removeString(values []string, value string) []string {
	var out []string
	for _, v := range values {
		if v != value {
			out = append(out, v)
		}
	}
	return out
}
