package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	podiomtools "github.com/Podiom/Podiom/internal/tools"
)

// installToolRequest is the POST /api/toolset body. It is the declarative
// payload an agent hands over — never a command line. Podiom builds the argv
// from these fields, which is what makes an unattended install safe to run.
type installToolRequest struct {
	Tool      string `json:"tool"`
	Installer string `json:"installer"`
	Package   string `json:"package"`
	Version   string `json:"version"`
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	Path      string `json:"path"`
	// InstalledBy/SessionID are provenance, stamped by the MCP helper from its
	// own launch flags rather than by the model, exactly as credentials are.
	InstalledBy string `json:"installed_by"`
	SessionID   string `json:"session_id"`
}

// handleToolset serves the shared toolset:
//
//	GET    /api/toolset         → manifest with per-entry health
//	POST   /api/toolset         → install one tool (synchronous)
//	DELETE /api/toolset/{tool}  → uninstall + manifest removal
func (s *Server) handleToolset(w http.ResponseWriter, r *http.Request) {
	tool := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/toolset"), "/")
	if strings.Contains(tool, "/") {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	root := s.paths.ToolsetDir

	switch {
	case tool == "" && r.Method == http.MethodGet:
		list, err := podiomtools.List(root)
		if list == nil {
			list = []podiomtools.ToolStatus{}
		}
		writeJSON(w, list, err)
	case tool == "" && r.Method == http.MethodPost:
		s.installTool(w, r, root)
	case tool != "" && r.Method == http.MethodDelete:
		if err := podiomtools.Uninstall(r.Context(), root, tool); err != nil {
			writeJSON(w, nil, err)
			return
		}
		s.log.Info("toolset tool removed", "event", "toolset", "tool", tool)
		writeJSON(w, map[string]string{"status": "removed", "tool": tool}, nil)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// installTool runs one install to completion and answers with the result. It
// is synchronous on purpose: the caller is an agent that wants to use the tool
// on its next command, so "installed, here is its version" is the useful
// answer and a failure is something it can act on immediately.
func (s *Server) installTool(w http.ResponseWriter, r *http.Request, root string) {
	var req installToolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	spec := podiomtools.SpecFromPayload(map[string]string{
		"tool":      req.Tool,
		"installer": req.Installer,
		"package":   req.Package,
		"version":   req.Version,
		"url":       req.URL,
		"sha256":    req.SHA256,
		"path":      req.Path,
	})
	if !spec.Installable() {
		writeJSON(w, nil, fmt.Errorf("installer is required: use npm, uv, go, cargo, binary, or archive"))
		return
	}
	if err := spec.Validate(); err != nil {
		writeJSON(w, nil, err)
		return
	}

	// Bounded by the install timeout rather than the request context alone: a
	// client that hangs up must not leave a half-finished install running with
	// no deadline.
	ctx, cancel := context.WithTimeout(r.Context(), podiomtools.InstallTimeout)
	defer cancel()

	res, err := podiomtools.Install(ctx, spec, root, podiomtools.ManifestEntry{
		InstalledBy: strings.TrimSpace(req.InstalledBy),
		SessionID:   strings.TrimSpace(req.SessionID),
	})
	if err != nil {
		s.log.Warn("toolset install failed",
			"event", "toolset",
			"tool", spec.Tool,
			"installer", string(spec.Installer),
			"agent", req.InstalledBy,
			"error", err,
		)
		writeJSON(w, nil, err)
		return
	}
	s.log.Info("toolset install finished",
		"event", "toolset",
		"tool", spec.Tool,
		"installer", string(spec.Installer),
		"agent", req.InstalledBy,
		"session", req.SessionID,
		"path", res.Path,
	)
	writeJSON(w, map[string]string{
		"status":  "installed",
		"tool":    spec.Tool,
		"path":    res.Path,
		"version": res.VersionOutput,
	}, nil)
}
