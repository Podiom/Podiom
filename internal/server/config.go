package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

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
	CollapseReasoning bool                  `json:"collapse_reasoning"`
	Voice             voiceConfigDTO        `json:"voice"`
}

// voiceConfigDTO mirrors the `voice:` YAML block, except the key itself is a
// secret and only its presence is exposed.
type voiceConfigDTO struct {
	OpenAIAPIKeySet bool `json:"openai_api_key_set"`
}

func globalToDTO(g config.Global, v config.Voice) globalConfigDTO {
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
		CollapseReasoning: g.CollapseReasoning,
		Voice:             voiceConfigDTO{OpenAIAPIKeySet: v.OpenAIAPIKey != ""},
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
	CollapseReasoning *bool                  `json:"collapse_reasoning,omitempty"`
	Voice             *voiceConfigPatch      `json:"voice,omitempty"`
}

// voiceConfigPatch carries the raw key on writes only: nil leaves the key
// untouched, "" clears it, anything else replaces it.
type voiceConfigPatch struct {
	OpenAIAPIKey *string `json:"openai_api_key,omitempty"`
}

// handleConfig serves the daemon-wide defaults the Settings page edits.
//
//	GET   /api/config -> current global defaults
//	PATCH /api/config -> merge, validate, persist to config.yaml, apply live
//
// Like every /api route it sits behind the gateway token and the source-IP
// guard; it mutates an on-disk file and the running daemon's behavior.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, globalToDTO(s.core.GetGlobal(), s.core.GetVoice()), nil)
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
	if patch.CollapseReasoning != nil {
		g.CollapseReasoning = *patch.CollapseReasoning
	}
	voiceBefore := s.core.GetVoice()
	v := voiceBefore
	if patch.Voice != nil && patch.Voice.OpenAIAPIKey != nil {
		v.OpenAIAPIKey = strings.TrimSpace(*patch.Voice.OpenAIAPIKey)
	}

	profileNames := map[string]config.Provider{}
	for _, p := range s.core.ListProfiles() {
		profileNames[p.Name] = p.Provider
	}
	if err := config.ValidateGlobal(g, profileNames); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := config.ValidateVoice(v); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := config.SetGlobal(s.paths.ConfigYAML, g); err != nil {
		writeJSON(w, nil, err)
		return
	}
	s.core.SetGlobal(g)
	if v != voiceBefore {
		if err := config.SetVoice(s.paths.ConfigYAML, v); err != nil {
			writeJSON(w, nil, err)
			return
		}
		s.core.SetVoice(v)
	}
	s.log.Info("global config updated",
		"event", "config",
		"changed", podiomlog.ChangedFields(globalLogFields(before, voiceBefore), globalLogFields(g, v)),
		"provider", string(g.Provider),
		"profile", g.Profile,
		"permission", string(g.PermissionMode),
		"permission_timeout", g.PermissionTimeout,
		"fallback_count", len(g.Fallback),
	)
	writeJSON(w, globalToDTO(s.core.GetGlobal(), s.core.GetVoice()), nil)
}

func globalLogFields(g config.Global, v config.Voice) map[string]string {
	return map[string]string{
		"provider":           string(g.Provider),
		"profile":            g.Profile,
		"model":              g.Model,
		"effort":             g.Effort,
		"permission":         string(g.PermissionMode),
		"permission_timeout": g.PermissionTimeout,
		"fallback_count":     fmt.Sprintf("%d", len(g.Fallback)),
		"collapse_reasoning": fmt.Sprintf("%t", g.CollapseReasoning),
		// presence only — the key itself must never reach a log line
		"openai_api_key_set": fmt.Sprintf("%t", v.OpenAIAPIKey != ""),
	}
}
