package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	podiomlog "github.com/Podiom/Podiom/internal/logging"
)

const (
	defaultLogLines = 200
	maxLogLines     = 5000
)

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !localRequest(r) {
		http.Error(w, "logs are only available from loopback clients", http.StatusForbidden)
		return
	}
	path := podiomlog.Path(s.paths.LogsDir)
	lines, err := podiomlog.Tail(path, logLines(r, defaultLogLines, false))
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, podiomlog.Snapshot{Path: path, Lines: lines}, nil)
}

func (s *Server) handleLogsFollow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !localRequest(r) {
		http.Error(w, "logs are only available from loopback clients", http.StatusForbidden)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	for event := range podiomlog.Follow(r.Context(), podiomlog.Path(s.paths.LogsDir), logLines(r, 100, true), 0) {
		if err := enc.Encode(event); err != nil {
			return
		}
		flusher.Flush()
	}
}

func logLines(r *http.Request, fallback int, allowZero bool) int {
	raw := r.URL.Query().Get("lines")
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || (n == 0 && !allowZero) {
		return fallback
	}
	if n > maxLogLines {
		return maxLogLines
	}
	return n
}
