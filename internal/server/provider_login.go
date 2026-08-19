package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/providercheck"
	"github.com/Podiom/Podiom/internal/providerlogin"
)

// providerAuthStatus is the credential-free projection of a providercheck
// Status. The binary path and doctor output stay server-side: the browser only
// needs to know whether this profile can run a turn, and what to do if not.
type providerAuthStatus struct {
	Provider config.Provider `json:"provider"`
	Profile  string          `json:"profile"`
	Found    bool            `json:"found"`
	Version  string          `json:"version,omitempty"`
	// LoginChecked is false when the CLI offers no probe we understand; the UI
	// shows "unknown" rather than claiming the profile is signed out.
	LoginChecked bool `json:"login_checked"`
	LoggedIn     bool `json:"logged_in"`
	// SupportsLogin reports whether Podiom can drive this provider's login from
	// the browser, as opposed to pointing the user at a terminal.
	SupportsLogin bool   `json:"supports_login"`
	InstallHint   string `json:"install_hint,omitempty"`
	LoginHint     string `json:"login_hint,omitempty"`
	Error         string `json:"error,omitempty"`
}

// providerStatusTTL caches the fan-out. Each probe spawns a CLI and may burn the
// full 8s timeout, so Settings re-rendering must not re-probe every time.
const providerStatusTTL = 30 * time.Second

type providerStatusCache struct {
	mu    sync.Mutex
	at    time.Time
	items []providerAuthStatus
}

// get returns the cached fan-out when it is still fresh.
func (c *providerStatusCache) get() ([]providerAuthStatus, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil || time.Since(c.at) > providerStatusTTL {
		return nil, false
	}
	return c.items, true
}

func (c *providerStatusCache) put(items []providerAuthStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items, c.at = items, time.Now()
}

// invalidate forces the next read to re-probe, e.g. right after a login lands.
func (c *providerStatusCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = nil
}

func (s *Server) handleProviderStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.URL.Query().Get("refresh") == "1" {
		s.providerStatus.invalidate()
	}
	if cached, ok := s.providerStatus.get(); ok {
		writeJSON(w, cached, nil)
		return
	}

	targets := providercheck.Targets(s.core.ListProfileDetails())
	statuses := providercheck.CheckTargets(r.Context(), targets, providercheck.Options{})
	items := make([]providerAuthStatus, 0, len(statuses))
	for _, st := range statuses {
		items = append(items, providerAuthStatus{
			Provider:      st.Provider,
			Profile:       st.Profile,
			Found:         st.Found,
			Version:       st.Version,
			LoginChecked:  st.LoginChecked,
			LoggedIn:      st.LoggedIn,
			SupportsLogin: providerlogin.Supported(st.Provider),
			InstallHint:   st.InstallHint,
			LoginHint:     st.LoginHint,
			Error:         st.Error,
		})
	}
	s.providerStatus.put(items)
	writeJSON(w, items, nil)
}

type providerLoginStartRequest struct {
	Provider config.Provider `json:"provider"`
	Profile  string          `json:"profile"`
}

type providerLoginCodeRequest struct {
	Code string `json:"code"`
}

// handleProviderLoginStart begins a login for one provider/profile pair.
func (s *Server) handleProviderLoginStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.core == nil || s.logins == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	var req providerLoginStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !config.KnownProvider(req.Provider) {
		http.Error(w, "unknown provider "+string(req.Provider), http.StatusBadRequest)
		return
	}
	dir, err := s.loginProfileDir(req.Provider, req.Profile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	sess, err := s.logins.Start(r.Context(), req.Provider, req.Profile, dir)
	if err != nil {
		writeProviderLoginError(w, err)
		return
	}
	// Profile is a name, not a path; the authorization URL and any code stay
	// out of the log entirely.
	s.log.Info("provider login started",
		"provider", req.Provider, "profile", req.Profile, "session", sess.ID)
	writeJSON(w, sess, nil)
}

// handleProviderLogin serves poll, code submission, and cancellation for one
// session: /api/provider-login/{id} and /api/provider-login/{id}/code.
func (s *Server) handleProviderLogin(w http.ResponseWriter, r *http.Request) {
	if s.logins == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/provider-login/")
	id, action, _ := strings.Cut(rest, "/")
	if id == "" {
		http.Error(w, "missing login session id", http.StatusBadRequest)
		return
	}

	switch {
	case action == "code" && r.Method == http.MethodPost:
		var req providerLoginCodeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		sess, err := s.logins.Submit(id, req.Code)
		if err != nil {
			writeProviderLoginError(w, err)
			return
		}
		writeJSON(w, sess, nil)
	case action == "" && r.Method == http.MethodGet:
		sess, err := s.logins.Get(id)
		if err != nil {
			writeProviderLoginError(w, err)
			return
		}
		if sess.Phase == providerlogin.PhaseSucceeded {
			// The profile's credentials just changed on disk; the cached
			// fan-out would otherwise keep reporting it signed out.
			s.providerStatus.invalidate()
			// Same for usage: without this its snapshot keeps reporting the old
			// expired token until the next poll.
			if s.usage != nil {
				s.usage.KickNow()
			}
		}
		writeJSON(w, sess, nil)
	case action == "" && r.Method == http.MethodDelete:
		if err := s.logins.Cancel(id); err != nil {
			writeProviderLoginError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// loginProfileDir resolves the auth directory a login should write into. An
// empty profile means the CLI's own global login, matching what "default"
// means everywhere else.
func (s *Server) loginProfileDir(provider config.Provider, profile string) (string, error) {
	if profile == "" {
		return "", nil
	}
	for _, p := range s.core.ListProfileDetails() {
		if p.Name != profile {
			continue
		}
		if p.Provider != provider {
			return "", errors.New("profile " + profile + " belongs to " + string(p.Provider))
		}
		return p.Dir(), nil
	}
	return "", errors.New("unknown profile " + profile)
}

// writeProviderLoginError maps the manager's sentinels onto status codes.
func writeProviderLoginError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, providerlogin.ErrNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, providerlogin.ErrUnsupported),
		errors.Is(err, providerlogin.ErrNoCodeExpected),
		errors.Is(err, providerlogin.ErrNotAwaitingCode),
		errors.Is(err, providerlogin.ErrInvalidCode):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		writeJSON(w, nil, err)
	}
}
