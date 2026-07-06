package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Podiom/Podiom/internal/marketplace"
)

// Skill-marketplace HTTP handlers (Spec 07 §6). All are behind the gateway token
// and source guard already applied by the middleware stack (SEC-1). Installs are
// a gateway-token-authenticated action: reached from the dashboard, and also from
// agents via the manage-mcp `podiom_install_skill` tool, which calls this same
// endpoint with the daemon's gateway token.

// marketplaceReady guards every handler: a nil service (skills root unresolved)
// degrades to 503 rather than a panic.
func (s *Server) marketplaceReady(w http.ResponseWriter) bool {
	if s.marketplace == nil {
		http.Error(w, "skill marketplace unavailable", http.StatusServiceUnavailable)
		return false
	}
	return true
}

// handleSkillSearch: GET /api/skills/search?q=&registry=&page=&sort= (FR-1..4).
func (s *Server) handleSkillSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.marketplaceReady(w) {
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	res, err := s.marketplace.Search(r.Context(), q.Get("q"), q.Get("registry"), q.Get("sort"), page)
	if err != nil {
		s.writeMarketplaceError(w, err)
		return
	}
	writeJSON(w, res, nil)
}

// handleSkillDetail: GET /api/skills/detail?registry=&id= (FR-7).
func (s *Server) handleSkillDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.marketplaceReady(w) {
		return
	}
	q := r.URL.Query()
	registry, id := q.Get("registry"), q.Get("id")
	if registry == "" || id == "" {
		http.Error(w, "registry and id are required", http.StatusBadRequest)
		return
	}
	detail, err := s.marketplace.Detail(r.Context(), registry, id)
	if err != nil {
		s.writeMarketplaceError(w, err)
		return
	}
	writeJSON(w, detail, nil)
}

// skillFileResponse carries a single file's content for the pre-install viewer
// (FR-8). Binary flags content that isn't valid UTF-8 (rendered as a notice).
type skillFileResponse struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Binary  bool   `json:"binary"`
}

// handleSkillFile: GET /api/skills/detail/file?registry=&id=&path= (FR-8).
func (s *Server) handleSkillFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.marketplaceReady(w) {
		return
	}
	q := r.URL.Query()
	registry, id, path := q.Get("registry"), q.Get("id"), q.Get("path")
	if registry == "" || id == "" || path == "" {
		http.Error(w, "registry, id and path are required", http.StatusBadRequest)
		return
	}
	raw, err := s.marketplace.File(r.Context(), registry, id, path)
	if err != nil {
		s.writeMarketplaceError(w, err)
		return
	}
	resp := skillFileResponse{Path: path}
	if utf8.Valid(raw) {
		resp.Content = string(raw)
	} else {
		resp.Binary = true
		resp.Content = "(binary file — not shown)"
	}
	writeJSON(w, resp, nil)
}

// handleSkillResolveURL: POST /api/skills/resolve {url} → []SkillSummary
// (FR-22/23). Resolving a monorepo returns the list to pick from.
func (s *Server) handleSkillResolveURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.marketplaceReady(w) {
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	rows, err := s.marketplace.ResolveURL(r.Context(), body.URL)
	if err != nil {
		s.writeMarketplaceError(w, err)
		return
	}
	if rows == nil {
		rows = []marketplace.SkillSummary{}
	}
	writeJSON(w, rows, nil)
}

// handleSkillInstall: POST /api/skills/install (FR-11, FR-22, SEC-1/2). Body is
// an InstallRequest (registry+id or url, plus acknowledge for executable skills).
func (s *Server) handleSkillInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.marketplaceReady(w) {
		return
	}
	var req marketplace.InstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	installed, err := s.marketplace.Install(r.Context(), req)
	if err != nil {
		s.writeMarketplaceError(w, err)
		return
	}
	writeJSON(w, installed, nil)
}

// handleSkillsInstalled: GET /api/skills/installed (FR-17).
func (s *Server) handleSkillsInstalled(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.marketplaceReady(w) {
		return
	}
	list, err := s.marketplace.Installed()
	if err != nil {
		s.writeMarketplaceError(w, err)
		return
	}
	if list == nil {
		list = []marketplace.InstalledSkill{}
	}
	writeJSON(w, list, nil)
}

// handleSkillInstalledItem routes the {name} sub-resources:
//
//	DELETE /api/skills/installed/{name}          → uninstall (FR-18)
//	GET    /api/skills/installed/{name}/update   → check update (FR-19)
//	POST   /api/skills/installed/{name}/update   → apply update (FR-19)
func (s *Server) handleSkillInstalledItem(w http.ResponseWriter, r *http.Request) {
	if !s.marketplaceReady(w) {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/skills/installed/")
	name, action, _ := strings.Cut(rest, "/")
	name = strings.TrimSpace(name)
	if name == "" {
		http.Error(w, "skill name is required", http.StatusBadRequest)
		return
	}
	switch action {
	case "":
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := s.marketplace.Uninstall(name); err != nil {
			s.writeMarketplaceError(w, err)
			return
		}
		writeJSON(w, map[string]string{"status": "uninstalled", "name": name}, nil)
	case "update":
		switch r.Method {
		case http.MethodGet:
			status, err := s.marketplace.CheckUpdate(r.Context(), name)
			if err != nil {
				s.writeMarketplaceError(w, err)
				return
			}
			writeJSON(w, status, nil)
		case http.MethodPost:
			var body struct {
				Acknowledge bool `json:"acknowledge"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			installed, err := s.marketplace.ApplyUpdate(r.Context(), name, body.Acknowledge)
			if err != nil {
				s.writeMarketplaceError(w, err)
				return
			}
			writeJSON(w, installed, nil)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	default:
		http.NotFound(w, r)
	}
}

// writeMarketplaceError maps marketplace errors to HTTP status codes: validation
// and collision errors are 400; a not-managed target is 404; anything that smells
// like a registry/network failure is 503; the rest fall through to 400.
func (s *Server) writeMarketplaceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, marketplace.ErrNotManaged):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, marketplace.ErrUnmanagedCollision),
		errors.Is(err, marketplace.ErrAckRequired),
		errors.Is(err, marketplace.ErrUnsafePath),
		errors.Is(err, marketplace.ErrTooLarge):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		msg := err.Error()
		if strings.Contains(msg, "github request failed") || strings.Contains(msg, "unavailable") ||
			strings.Contains(msg, "skillsmp") || strings.Contains(msg, "not found") {
			http.Error(w, msg, http.StatusServiceUnavailable)
			return
		}
		http.Error(w, msg, http.StatusBadRequest)
	}
}
