package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Podiom/Podiom/internal/config"
	podiomlog "github.com/Podiom/Podiom/internal/logging"
)

// globalConfigDTO mirrors config.Global for the Settings page. Field names are
// snake_case to match the YAML keys and the frontend GlobalConfig type.
type globalConfigDTO struct {
	Provider          config.Provider       `json:"provider"`
	Profile           string                `json:"profile"`
	Model             string                `json:"model"`
	Effort            string                `json:"effort"`
	PermissionMode    config.PermissionMode `json:"permission_mode"`
	PermissionTimeout string                `json:"permission_timeout"`
	Fallback          []string              `json:"fallback"`
}

func globalToDTO(g config.Global) globalConfigDTO {
	if g.Fallback == nil {
		g.Fallback = []string{}
	}
	return globalConfigDTO{
		Provider:          g.Provider,
		Profile:           g.Profile,
		Model:             g.Model,
		Effort:            g.Effort,
		PermissionMode:    g.PermissionMode,
		PermissionTimeout: g.PermissionTimeout,
		Fallback:          g.Fallback,
	}
}

// globalConfigPatch is the PATCH body. Every field is a pointer so omitted
// fields keep their current value; a present-but-empty fallback clears it.
type globalConfigPatch struct {
	Provider          *config.Provider       `json:"provider,omitempty"`
	Profile           *string                `json:"profile,omitempty"`
	Model             *string                `json:"model,omitempty"`
	Effort            *string                `json:"effort,omitempty"`
	PermissionMode    *config.PermissionMode `json:"permission_mode,omitempty"`
	PermissionTimeout *string                `json:"permission_timeout,omitempty"`
	Fallback          *[]string              `json:"fallback,omitempty"`
}

// handleConfig serves the daemon-wide defaults the Settings page edits.
//
//	GET   /api/config -> current global defaults
//	PATCH /api/config -> merge, validate, persist to config.yaml, apply live
//
// Restricted to loopback clients like the update endpoints: it mutates an
// on-disk file and the running daemon's behavior.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, globalToDTO(s.core.GetGlobal()), nil)
	case http.MethodPatch, http.MethodPut:
		s.patchConfig(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) patchConfig(w http.ResponseWriter, r *http.Request) {
	var patch globalConfigPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	before := s.core.GetGlobal()
	g := before
	if patch.Provider != nil {
		g.Provider = *patch.Provider
	}
	if patch.Profile != nil {
		g.Profile = *patch.Profile
	}
	if patch.Model != nil {
		g.Model = *patch.Model
	}
	if patch.Effort != nil {
		g.Effort = *patch.Effort
	}
	if patch.PermissionMode != nil {
		g.PermissionMode = *patch.PermissionMode
	}
	if patch.PermissionTimeout != nil {
		g.PermissionTimeout = *patch.PermissionTimeout
	}
	if patch.Fallback != nil {
		g.Fallback = *patch.Fallback
	}

	profileNames := map[string]config.Provider{}
	for _, p := range s.core.ListProfiles() {
		profileNames[p.Name] = p.Provider
	}
	if err := config.ValidateGlobal(g, profileNames); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := config.SetGlobal(s.paths.ConfigYAML, g); err != nil {
		writeJSON(w, nil, err)
		return
	}
	s.core.SetGlobal(g)
	s.log.Info("global config updated",
		"event", "config",
		"changed", podiomlog.ChangedFields(globalLogFields(before), globalLogFields(g)),
		"provider", string(g.Provider),
		"profile", g.Profile,
		"permission", string(g.PermissionMode),
		"permission_timeout", g.PermissionTimeout,
		"fallback_count", len(g.Fallback),
	)
	writeJSON(w, globalToDTO(s.core.GetGlobal()), nil)
}

func globalLogFields(g config.Global) map[string]string {
	return map[string]string{
		"provider":           string(g.Provider),
		"profile":            g.Profile,
		"model":              g.Model,
		"effort":             g.Effort,
		"permission":         string(g.PermissionMode),
		"permission_timeout": g.PermissionTimeout,
		"fallback_count":     fmt.Sprintf("%d", len(g.Fallback)),
	}
}
