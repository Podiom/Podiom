// Package providerlogin drives a provider CLI's own interactive login from the
// daemon, so a browser client can authenticate a profile without a terminal.
//
// Both supported CLIs print an authorization URL on stdout and then wait: Claude
// wants an authorization code pasted back on stdin (its manual OAuth redirect),
// Codex shows a device code and polls by itself. Neither needs a pty — plain
// pipes are enough — so this package scrapes the URL, exposes it for the browser
// to open, and forwards the pasted code when there is one.
//
// Credentials are never read, stored or logged here. The CLI owns the token
// exchange and writes to its own profile directory; Podiom only sees the
// authorization URL, the one-time code on its way to the child's stdin, and the
// exit status.
package providerlogin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Podiom/Podiom/internal/config"
	podiomexec "github.com/Podiom/Podiom/internal/exec"
)

// Phase is the coarse state a login session is in. The browser polls for it and
// renders one step per phase.
type Phase string

const (
	// PhaseStarting means the CLI is running but has not printed a URL yet.
	PhaseStarting Phase = "starting"
	// PhaseAwaitingCode means the user must authorize in the browser and paste
	// the resulting code back (Claude).
	PhaseAwaitingCode Phase = "awaiting_code"
	// PhaseAwaitingAuthorization means the user must authorize in the browser
	// and the CLI is polling on its own (Codex device code).
	PhaseAwaitingAuthorization Phase = "awaiting_authorization"
	// PhaseVerifying means a code was submitted and the CLI is exchanging it.
	PhaseVerifying Phase = "verifying"
	// PhaseSucceeded means the CLI exited 0; the profile is authenticated.
	PhaseSucceeded Phase = "succeeded"
	// PhaseFailed means the CLI exited non-zero, timed out, or was cancelled.
	PhaseFailed Phase = "failed"
)

// Done reports whether the phase is terminal.
func (p Phase) Done() bool { return p == PhaseSucceeded || p == PhaseFailed }

// Session is the client-visible state of one login attempt. It deliberately
// carries no credential material: URL and UserCode are values the user is meant
// to see, and the submitted authorization code is never echoed back.
type Session struct {
	ID        string          `json:"id"`
	Provider  config.Provider `json:"provider"`
	Profile   string          `json:"profile"`
	Phase     Phase           `json:"phase"`
	URL       string          `json:"url,omitempty"`
	UserCode  string          `json:"user_code,omitempty"`
	Message   string          `json:"message,omitempty"`
	NeedsCode bool            `json:"needs_code"`
	ExpiresAt time.Time       `json:"expires_at"`
}

// DefaultTTL bounds a login attempt. Both CLIs expire their own codes after
// 15 minutes, so a longer session could only ever fail.
const DefaultTTL = 15 * time.Minute

// retention keeps a finished session pollable long enough for the browser to
// observe the terminal phase before it is swept.
const retention = 5 * time.Minute

var (
	// ErrUnsupported means the provider has no login flow in loginFlows.
	ErrUnsupported = errors.New("provider does not support browser login")
	// ErrNotFound means the id is unknown or the session was already swept.
	ErrNotFound = errors.New("login session not found")
	// ErrNoCodeExpected means the provider polls by itself and takes no code.
	ErrNoCodeExpected = errors.New("this provider's login takes no code")
	// ErrNotAwaitingCode means a code arrived before the URL or after the end.
	ErrNotAwaitingCode = errors.New("login session is not awaiting a code")
	// ErrInvalidCode means the submitted code was empty or malformed.
	ErrInvalidCode = errors.New("authorization code is empty or malformed")
)

// Options configures a Manager.
type Options struct {
	Discovery podiomexec.Discovery
	TTL       time.Duration
}

// Manager owns the in-flight login sessions. The zero value is not usable; call
// New.
type Manager struct {
	discovery podiomexec.Discovery
	ttl       time.Duration

	mu       sync.Mutex
	sessions map[string]*loginSession
}

// New builds a Manager.
func New(opts Options) *Manager {
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Manager{
		discovery: opts.Discovery,
		ttl:       ttl,
		sessions:  make(map[string]*loginSession),
	}
}

type loginSession struct {
	state    Session
	cancel   context.CancelFunc
	stdin    io.WriteCloser
	tail     []string
	finished time.Time
}

// Start launches the provider's login CLI for one profile. dir is the profile's
// auth directory ("" for the CLI's own global login). It returns as soon as the
// process is running; the caller polls Get for the URL.
//
// The session outlives the request that started it — ctx is used only to bound
// binary discovery, not the login itself.
func (m *Manager) Start(ctx context.Context, provider config.Provider, profile, dir string) (Session, error) {
	flow, ok := loginFlows[provider]
	if !ok {
		return Session{}, fmt.Errorf("%w: %s", ErrUnsupported, provider)
	}
	info, ok := config.ProviderInfoFor(provider)
	if !ok || len(info.LoginArgs) == 0 {
		return Session{}, fmt.Errorf("%w: %s", ErrUnsupported, provider)
	}
	found, err := m.discovery.Find(string(provider))
	if err != nil {
		return Session{}, err
	}

	// One attempt per target: a second click supersedes the first rather than
	// leaving an orphaned CLI holding a half-finished OAuth flow.
	m.cancelTarget(provider, profile)
	m.sweep()

	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), m.ttl)
	cmd := podiomexec.Command(runCtx, found.Path, info.LoginArgs...)
	cmd.Env = loginEnv(info.ProfileEnvVar, dir)
	// CommandContext's default cancel kills only the leader; take the group so
	// a timed-out login cannot leave a listener behind.
	cmd.Cancel = func() error { return podiomexec.Kill(cmd) }

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return Session{}, err
	}

	sess := &loginSession{
		state: Session{
			ID:        uuid.NewString(),
			Provider:  provider,
			Profile:   profile,
			Phase:     PhaseStarting,
			NeedsCode: flow.needsCode,
			ExpiresAt: time.Now().Add(m.ttl),
		},
		cancel: cancel,
		stdin:  stdin,
	}

	// Both streams fold into one line reader: the CLIs narrate progress on
	// stdout and report failures on stderr, and the state machine wants both.
	out := &lineWriter{on: func(line string) { m.onLine(sess, flow, line) }}
	cmd.Stdout = out
	cmd.Stderr = out

	if err := cmd.Start(); err != nil {
		cancel()
		_ = stdin.Close()
		return Session{}, err
	}

	m.mu.Lock()
	m.sessions[sess.state.ID] = sess
	state := sess.state
	m.mu.Unlock()

	go m.wait(sess, cmd, runCtx)
	return state, nil
}

// onLine folds one line of CLI output into the session state.
func (m *Manager) onLine(sess *loginSession, flow loginFlow, raw string) {
	line := strings.TrimRight(stripANSI(raw), " \t\r")
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess.state.Phase.Done() {
		return
	}
	if trimmed := strings.TrimSpace(line); trimmed != "" {
		sess.tail = append(sess.tail, trimmed)
		if len(sess.tail) > 10 {
			sess.tail = sess.tail[len(sess.tail)-10:]
		}
	}
	flow.parse(line, &sess.state)
}

// wait turns the CLI's exit status into the terminal phase. Exit code is the
// signal rather than a success string: it is stable across CLI versions, and
// both flows already print their own diagnostics on the way out.
func (m *Manager) wait(sess *loginSession, cmd *exec.Cmd, runCtx context.Context) {
	waitErr := cmd.Wait()
	_ = sess.stdin.Close()
	sess.cancel()

	m.mu.Lock()
	defer m.mu.Unlock()
	if sess.state.Phase.Done() {
		return // already cancelled
	}
	sess.finished = time.Now()
	if waitErr == nil {
		sess.state.Phase = PhaseSucceeded
		sess.state.Message = ""
		return
	}
	sess.state.Phase = PhaseFailed
	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		sess.state.Message = "login timed out; start over"
	case sess.state.Message != "":
		// Keep the parser's more specific message (e.g. "Invalid code").
	default:
		sess.state.Message = lastLine(sess.tail, waitErr)
	}
}

// Submit forwards a pasted authorization code to the CLI's stdin.
func (m *Manager) Submit(id, code string) (Session, error) {
	code = strings.TrimSpace(code)
	// A newline would inject a second answer into the CLI's line reader, so
	// anything multi-line is rejected outright rather than truncated.
	if code == "" || strings.ContainsAny(code, "\r\n") {
		return Session{}, ErrInvalidCode
	}

	m.mu.Lock()
	sess, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return Session{}, ErrNotFound
	}
	if !sess.state.NeedsCode {
		m.mu.Unlock()
		return Session{}, ErrNoCodeExpected
	}
	if sess.state.Phase != PhaseAwaitingCode {
		m.mu.Unlock()
		return Session{}, ErrNotAwaitingCode
	}
	stdin := sess.stdin
	m.mu.Unlock()

	if _, err := io.WriteString(stdin, code+"\n"); err != nil {
		return Session{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// The CLI may have already exited between the unlock and the write.
	if !sess.state.Phase.Done() {
		sess.state.Phase = PhaseVerifying
		sess.state.Message = ""
	}
	return sess.state, nil
}

// Get returns the current state of one session.
func (m *Manager) Get(id string) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[id]
	if !ok {
		return Session{}, ErrNotFound
	}
	return sess.state, nil
}

// Cancel stops a login attempt and kills the CLI's process group.
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	sess, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	if !sess.state.Phase.Done() {
		sess.state.Phase = PhaseFailed
		sess.state.Message = "cancelled"
		sess.finished = time.Now()
	}
	cancel := sess.cancel
	m.mu.Unlock()

	cancel()
	return nil
}

// Shutdown cancels every in-flight session.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(m.sessions))
	for _, sess := range m.sessions {
		if !sess.state.Phase.Done() {
			sess.state.Phase = PhaseFailed
			sess.state.Message = "cancelled"
			sess.finished = time.Now()
		}
		cancels = append(cancels, sess.cancel)
	}
	m.sessions = make(map[string]*loginSession)
	m.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
}

// cancelTarget ends any in-flight session for the same provider+profile.
func (m *Manager) cancelTarget(provider config.Provider, profile string) {
	m.mu.Lock()
	var cancels []context.CancelFunc
	for _, sess := range m.sessions {
		if sess.state.Provider != provider || sess.state.Profile != profile {
			continue
		}
		if !sess.state.Phase.Done() {
			sess.state.Phase = PhaseFailed
			sess.state.Message = "superseded by a newer login"
			sess.finished = time.Now()
			cancels = append(cancels, sess.cancel)
		}
	}
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// sweep drops finished sessions the browser has had time to observe.
func (m *Manager) sweep() {
	cutoff := time.Now().Add(-retention)
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, sess := range m.sessions {
		if sess.state.Phase.Done() && !sess.finished.IsZero() && sess.finished.Before(cutoff) {
			delete(m.sessions, id)
		}
	}
}

// loginEnv builds the child environment: the profile directory variable, plus a
// no-op browser opener. Both CLIs try to launch a browser on the daemon host,
// which is pointless when the user is on another machine and impossible in the
// Home Assistant container — the browser step happens in the popup instead.
func loginEnv(profileVar, dir string) []string {
	env := podiomexec.ProfileEnv(os.Environ(), profileVar, dir)
	env = podiomexec.ProfileEnv(env, "BROWSER", "true")
	return podiomexec.ProfileEnv(env, "DISPLAY", "")
}

func lastLine(tail []string, fallback error) string {
	if len(tail) > 0 {
		return tail[len(tail)-1]
	}
	return fallback.Error()
}

// ansiPattern matches CSI and OSC escape sequences. Codex colourises its login
// output even when stdout is a pipe, so lines must be cleaned before matching.
var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)")

func stripANSI(s string) string { return ansiPattern.ReplaceAllString(s, "") }

// lineWriter splits a byte stream into lines. It exists instead of a
// bufio.Scanner because Claude's "Paste code here if prompted > " prompt has no
// trailing newline: a scanner would block on it, while the state machine only
// needs the URL line that precedes it.
type lineWriter struct {
	mu  sync.Mutex
	buf []byte
	on  func(string)
}

// maxLine caps the buffer so a CLI that never emits a newline cannot grow it
// without bound.
const maxLine = 64 * 1024

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := string(w.buf[:i])
		w.buf = append(w.buf[:0], w.buf[i+1:]...)
		w.on(line)
	}
	if len(w.buf) > maxLine {
		w.on(string(w.buf))
		w.buf = w.buf[:0]
	}
	return len(p), nil
}
