package server

import (
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"time"

	podiomlog "github.com/Podiom/Podiom/internal/logging"
	"github.com/Podiom/Podiom/internal/updater"
)

type updateApplyRequest struct {
	Version string `json:"version,omitempty"`
	Force   bool   `json:"force,omitempty"`
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.haMode {
		http.Error(w, "updates are managed by Home Assistant", http.StatusForbidden)
		return
	}
	started := time.Now()
	s.log.Info("update check started", "event", "config", "current_version", s.build.Version, "current_commit", s.build.Commit)
	status, err := updater.Check(r.Context(), updater.Options{
		CurrentVersion: s.build.Version,
		CurrentCommit:  s.build.Commit,
		Home:           s.paths.Home,
	})
	if err != nil {
		s.log.Warn("update check failed", "event", "config", podiomlog.ErrorAttr(err), podiomlog.DurationMS("duration_ms", time.Since(started)))
	} else {
		s.log.Info("update check finished", "event", "config", "update_available", status.UpdateAvailable, "latest_version", status.LatestVersion, podiomlog.DurationMS("duration_ms", time.Since(started)))
	}
	writeJSON(w, status, err)
}

func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Self-update is refused in HA mode: new Podiom versions arrive as app
	// updates through Home Assistant, never by the daemon replacing itself
	// (HA26). Elsewhere the gateway-token middleware already authenticated the
	// caller, which supersedes the old loopback-only gate.
	if s.haMode {
		http.Error(w, "updates are managed by Home Assistant", http.StatusForbidden)
		return
	}
	var req updateApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	started := time.Now()
	s.log.Info("update apply started", "event", "config", "requested_version", req.Version, "force", req.Force, "current_version", s.build.Version)
	result, err := updater.Apply(r.Context(), updater.Options{
		CurrentVersion: s.build.Version,
		CurrentCommit:  s.build.Commit,
		Version:        req.Version,
		Force:          req.Force,
		Home:           s.paths.Home,
		RestartDaemon:  true,
	})
	if err != nil {
		s.log.Warn("update apply failed", "event", "config", "requested_version", req.Version, "force", req.Force, podiomlog.ErrorAttr(err), podiomlog.DurationMS("duration_ms", time.Since(started)))
		writeJSON(w, result, err)
		return
	}
	s.log.Info("update apply finished", "event", "config", "requested_version", req.Version, "force", req.Force, "restart_required", result.RestartRequired, "helper_started", result.HelperStarted, podiomlog.DurationMS("duration_ms", time.Since(started)))
	writeJSON(w, result, nil)
	if result.RestartRequired || result.HelperStarted {
		go s.exitAfterUpdate()
	}
}

func (s *Server) exitAfterUpdate() {
	time.Sleep(300 * time.Millisecond)
	if runtime.GOOS != "windows" {
		if installDir, err := updater.ResolveInstallDir(""); err == nil {
			_ = updater.ScheduleUnixDaemonRestart(installDir)
		}
	}
	os.Exit(0)
}
