// Package logging owns Podiom's daemon log files and shared tail/follow helpers.
package logging

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	activeLogName    = "podiomd.log"
	rotatedLogGlob   = "podiomd-*.log"
	rotatedDateForm  = "2006-01-02"
	defaultPollEvery = 250 * time.Millisecond
)

// Options configures the daemon logger.
type Options struct {
	Dir           string
	RetentionDays int
	Level         string
	Stderr        io.Writer
	Now           func() time.Time
}

// Open returns a structured logger that writes to stderr and the active daemon
// log file. The returned closer must be closed on daemon shutdown.
func Open(opts Options) (*slog.Logger, io.Closer, error) {
	writer, err := NewRotatingWriter(opts.Dir, opts.RetentionDays, opts.Now)
	if err != nil {
		return nil, nil, err
	}
	out := io.Writer(writer)
	if opts.Stderr != nil {
		out = io.MultiWriter(opts.Stderr, writer)
	}
	level, err := parseLevel(opts.Level)
	if err != nil {
		_ = writer.Close()
		return nil, nil, err
	}
	return slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: level})), writer, nil
}

func parseLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unknown logging.level %q (want debug|info|warn|error)", raw)
	}
}

// Path returns the active log file path under dir.
func Path(dir string) string {
	return filepath.Join(dir, activeLogName)
}

// RotatingWriter appends to podiomd.log, rotating it once per local day.
type RotatingWriter struct {
	mu            sync.Mutex
	dir           string
	retentionDays int
	now           func() time.Time
	currentDate   string
	file          *os.File
}

// NewRotatingWriter opens the active log and rotates a stale active file.
func NewRotatingWriter(dir string, retentionDays int, now func() time.Time) (*RotatingWriter, error) {
	if retentionDays <= 0 {
		return nil, fmt.Errorf("logging.retention_days must be greater than 0")
	}
	if dir == "" {
		return nil, fmt.Errorf("log dir is required")
	}
	if now == nil {
		now = time.Now
	}
	w := &RotatingWriter{dir: dir, retentionDays: retentionDays, now: now}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir %s: %w", dir, err)
	}
	if err := w.openLocked(); err != nil {
		return nil, err
	}
	return w, nil
}

// Write implements io.Writer.
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.rotateIfNeededLocked(); err != nil {
		return 0, err
	}
	return w.file.Write(p)
}

// Close closes the active file.
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *RotatingWriter) openLocked() error {
	today := w.date()
	active := Path(w.dir)
	if info, err := os.Stat(active); err == nil {
		fileDate := info.ModTime().Local().Format(rotatedDateForm)
		if fileDate != today {
			if err := w.rotateActiveLocked(fileDate); err != nil {
				return err
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat active log: %w", err)
	}
	f, err := os.OpenFile(active, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open active log: %w", err)
	}
	w.file = f
	w.currentDate = today
	return w.cleanupLocked()
}

func (w *RotatingWriter) rotateIfNeededLocked() error {
	today := w.date()
	if w.currentDate == today && w.file != nil {
		return nil
	}
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return err
		}
		w.file = nil
	}
	if w.currentDate != "" {
		if err := w.rotateActiveLocked(w.currentDate); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(Path(w.dir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open active log: %w", err)
	}
	w.file = f
	w.currentDate = today
	return w.cleanupLocked()
}

func (w *RotatingWriter) rotateActiveLocked(date string) error {
	active := Path(w.dir)
	if _, err := os.Stat(active); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat active log: %w", err)
	}
	dest := filepath.Join(w.dir, "podiomd-"+date+".log")
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		if err := os.Rename(active, dest); err != nil {
			return fmt.Errorf("rotate log: %w", err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("stat rotated log: %w", err)
	}
	src, err := os.Open(active)
	if err != nil {
		return fmt.Errorf("open active log for rotation: %w", err)
	}
	defer src.Close()
	dst, err := os.OpenFile(dest, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open rotated log: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return fmt.Errorf("append rotated log: %w", err)
	}
	if err := dst.Close(); err != nil {
		return err
	}
	return os.Remove(active)
}

func (w *RotatingWriter) cleanupLocked() error {
	cutoff := w.now().Local().AddDate(0, 0, -(w.retentionDays - 1))
	matches, err := filepath.Glob(filepath.Join(w.dir, rotatedLogGlob))
	if err != nil {
		return err
	}
	for _, path := range matches {
		base := filepath.Base(path)
		raw := strings.TrimSuffix(strings.TrimPrefix(base, "podiomd-"), ".log")
		day, err := time.ParseInLocation(rotatedDateForm, raw, w.now().Location())
		if err != nil {
			continue
		}
		if day.Before(startOfDay(cutoff)) {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func (w *RotatingWriter) date() string {
	return w.now().Local().Format(rotatedDateForm)
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// Snapshot is the JSON response for GET /api/logs.
type Snapshot struct {
	Path  string   `json:"path"`
	Lines []string `json:"lines"`
}

// Tail reads the last n lines from path. Missing logs produce an empty tail.
func Tail(path string, n int) ([]string, error) {
	if n == 0 {
		return []string{}, nil
	}
	if n < 0 {
		n = 100
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	lines := make([]string, 0, n)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		if len(lines) == n {
			copy(lines, lines[1:])
			lines = lines[:n-1]
		}
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

// FollowEvent is emitted by Follow.
type FollowEvent struct {
	Type string `json:"type"`
	Line string `json:"line,omitempty"`
}

// Follow emits an initial tail, then new lines appended to path. It reopens the
// active file after rotation/truncation and emits a "reopen" event.
func Follow(ctx context.Context, path string, lines int, pollEvery time.Duration) <-chan FollowEvent {
	if pollEvery <= 0 {
		pollEvery = defaultPollEvery
	}
	out := make(chan FollowEvent, 64)
	go func() {
		defer close(out)
		var offset int64
		var last os.FileInfo
		if info, err := os.Stat(path); err == nil {
			last = info
		}
		// Read the initial tail and its end offset from a single read so any
		// append that happens after this point is guaranteed to be past offset.
		// Capturing offset separately (e.g. via a later Stat) races with writers
		// and can silently skip lines appended between emit and Stat.
		if res, err := readFrom(path, 0); err == nil {
			offset = res.offset
			tail := res.lines
			if lines == 0 {
				tail = nil
			} else if lines > 0 && len(tail) > lines {
				tail = tail[len(tail)-lines:]
			}
			for _, line := range tail {
				if !sendFollowEvent(ctx, out, FollowEvent{Type: "line", Line: line}) {
					return
				}
			}
		}
		ticker := time.NewTicker(pollEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			info, err := os.Stat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				continue
			}
			if last != nil && (!os.SameFile(last, info) || info.Size() < offset) {
				offset = 0
				if !sendFollowEvent(ctx, out, FollowEvent{Type: "reopen"}) {
					return
				}
			}
			last = info
			next, err := readFrom(path, offset)
			if err != nil {
				continue
			}
			offset = next.offset
			for _, line := range next.lines {
				if !sendFollowEvent(ctx, out, FollowEvent{Type: "line", Line: line}) {
					return
				}
			}
		}
	}()
	return out
}

type readResult struct {
	offset int64
	lines  []string
}

func readFrom(path string, offset int64) (readResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return readResult{}, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return readResult{}, err
	}
	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return readResult{}, err
	}
	pos, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return readResult{}, err
	}
	return readResult{offset: pos, lines: lines}, nil
}

func sendFollowEvent(ctx context.Context, out chan<- FollowEvent, event FollowEvent) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- event:
		return true
	}
}

type redactionPattern struct {
	re          *regexp.Regexp
	replacement string
}

var sensitivePatterns = []redactionPattern{
	{regexp.MustCompile(`(?i)(api[_-]?key|token|secret|password)\s*[:=]\s*[^,\s"}]+`), "$1=[REDACTED]"},
	{regexp.MustCompile(`(?i)(access_token|refresh_token)"\s*:\s*"[^"]+"`), `$1":"[REDACTED]"`},
	{regexp.MustCompile(`(?i)(authorization)\s*:\s*bearer\s+[^,\s"}]+`), "$1: Bearer [REDACTED]"},
	{regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._-]+`), "Bearer [REDACTED]"},
	{regexp.MustCompile(`(?i)(sk-proj-|sk-ant-|sk-)[A-Za-z0-9_-]{8,}`), "[REDACTED]"},
	{regexp.MustCompile(`(?i)https://[^,\s"}]*github[^,\s"}]*(?:token|access_token)=[^,\s"}&]+`), "[REDACTED_URL]"},
	{regexp.MustCompile(`(?i)(https://[^,\s"}]*/repos/[^,\s"}]*/zipball/[^,\s"}?]+)\?[^,\s"}]+`), "$1?[REDACTED]"},
}

// Redact removes common token/secret shapes before provider diagnostics are
// written to Podiom logs.
func Redact(text string) string {
	redacted := text
	for _, pattern := range sensitivePatterns {
		redacted = pattern.re.ReplaceAllString(redacted, pattern.replacement)
	}
	return redacted
}

// RedactTail redacts text and keeps only the final limit bytes.
func RedactTail(text string, limit int) string {
	redacted := Redact(strings.TrimSpace(text))
	if limit > 0 && len(redacted) > limit {
		redacted = redacted[len(redacted)-limit:]
	}
	return redacted
}

// ErrorAttr returns a redacted slog error attribute. Nil errors are represented
// as an empty string so callers can append the attr unconditionally.
func ErrorAttr(err error) slog.Attr {
	if err == nil {
		return slog.String("error", "")
	}
	return slog.String("error", Redact(err.Error()))
}

// DurationMS returns a duration rounded to whole milliseconds for compact logs.
func DurationMS(key string, d time.Duration) slog.Attr {
	return slog.Int64(key, d.Milliseconds())
}

// ChangedFields returns sorted field names whose before/after values differ.
func ChangedFields(before, after map[string]string) []string {
	seen := map[string]bool{}
	for key := range before {
		seen[key] = true
	}
	for key := range after {
		seen[key] = true
	}
	var changed []string
	for key := range seen {
		if before[key] != after[key] {
			changed = append(changed, key)
		}
	}
	sort.Strings(changed)
	return changed
}

// Count returns len(values) while treating nil as zero.
func Count[T any](values []T) int {
	return len(values)
}
