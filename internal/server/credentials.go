package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Podiom/Podiom/internal/creds"
)

// credentialView is the outward projection of a stored credential. It has no
// value field on purpose: the secret never leaves the daemon.
type credentialView struct {
	Name             string `json:"name"`
	Purpose          string `json:"purpose,omitempty"`
	GoalID           string `json:"goal_id,omitempty"`
	CreatedAt        string `json:"created_at,omitempty"`
	CreatedByAgent   string `json:"created_by_agent,omitempty"`
	CreatedBySession string `json:"created_by_session,omitempty"`
}

func viewOf(c creds.Credential) credentialView {
	return credentialView{
		Name:             c.Name,
		Purpose:          c.Purpose,
		GoalID:           c.GoalID,
		CreatedAt:        c.CreatedAt,
		CreatedByAgent:   c.CreatedByAgent,
		CreatedBySession: c.CreatedBySession,
	}
}

// storeCredentialRequest is the POST body. It is the one place a secret value
// enters the daemon outside access-request approval, and it is shared by both
// writers: the Credentials page form (unattributed — the user did it) and an
// agent's podiom_store_credential (attributed, stamped by the MCP helper from
// its own launch flags rather than by the model).
type storeCredentialRequest struct {
	Name             string `json:"name"`
	Value            string `json:"value"`
	Purpose          string `json:"purpose"`
	CreatedByAgent   string `json:"created_by_agent"`
	CreatedBySession string `json:"created_by_session"`
	Overwrite        bool   `json:"overwrite"`
}

// handleCredentials handles GET /api/credentials — the names-only listing for
// the Settings page — and POST /api/credentials, which stores one secret.
func (s *Server) handleCredentials(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.listCredentials(w, r)
	case http.MethodPost:
		s.storeCredential(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) listCredentials(w http.ResponseWriter, r *http.Request) {
	list, err := s.core.ListCredentials(r.Context())
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	out := make([]credentialView, 0, len(list))
	for _, c := range list {
		out = append(out, viewOf(c))
	}
	writeJSON(w, out, nil)
}

// storeCredential upserts one secret. Name and value shape are validated by
// creds.Set so both writers obey one rule set; the overwrite guard lives here
// because only this layer knows whether the caller meant to replace something.
// The value is never echoed back, logged, or written to the response.
func (s *Server) storeCredential(w http.ResponseWriter, r *http.Request) {
	var req storeCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeJSON(w, nil, fmt.Errorf("credential name is required"))
		return
	}
	if strings.TrimSpace(req.Value) == "" {
		writeJSON(w, nil, fmt.Errorf("credential value is required"))
		return
	}

	cred := creds.Credential{
		Name:             req.Name,
		Value:            req.Value,
		Purpose:          strings.TrimSpace(req.Purpose),
		CreatedByAgent:   strings.TrimSpace(req.CreatedByAgent),
		CreatedBySession: strings.TrimSpace(req.CreatedBySession),
	}

	existing, err := s.core.ListCredentials(r.Context())
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	for _, c := range existing {
		if c.Name != cred.Name {
			continue
		}
		if !req.Overwrite {
			writeJSON(w, nil, fmt.Errorf("credential %s already exists — pass overwrite=true only if the user asked you to replace it", cred.Name))
			return
		}
		// Rotating a value does not change what the credential is for or which
		// goal asked for it, so carry that metadata across unless the caller
		// deliberately supplied a new purpose.
		if cred.Purpose == "" {
			cred.Purpose = c.Purpose
		}
		cred.GoalID = c.GoalID
		break
	}
	if err := s.core.StoreCredential(r.Context(), cred); err != nil {
		writeJSON(w, nil, err)
		return
	}
	// Propagate into any long-lived provider process (Codex app-server) so a
	// running session picks the credential up without a restart; Claude
	// re-reads it on its next turn.
	s.core.RefreshCredentials()

	// Read back so the response carries the stamped created_at rather than a
	// value the handler guessed at — and so it can never carry the secret.
	stored, err := s.core.ListCredentials(r.Context())
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	for _, c := range stored {
		if c.Name == req.Name {
			writeJSON(w, viewOf(c), nil)
			return
		}
	}
	writeJSON(w, nil, fmt.Errorf("credential %s was not found after storing", req.Name))
}

// handleCredential handles DELETE /api/credentials/{name}.
func (s *Server) handleCredential(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/credentials/")
	if name == "" {
		http.Error(w, "credential name is required", http.StatusBadRequest)
		return
	}
	if err := s.core.DeleteCredential(r.Context(), name); err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, map[string]string{"status": "deleted"}, nil)
}
