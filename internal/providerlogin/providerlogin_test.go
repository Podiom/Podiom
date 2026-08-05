package providerlogin

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/config"
	podiomexec "github.com/Podiom/Podiom/internal/exec"
)

// fakeClaude mirrors the real `claude auth login`: it narrates the manual
// redirect URL on stdout, leaves the "Paste code here" prompt without a
// trailing newline, then reads one line from stdin. Exit code is the verdict.
const fakeClaude = `#!/usr/bin/env sh
if [ "$1" != "auth" ] || [ "$2" != "login" ]; then exit 64; fi
echo "Opening browser to sign in…"
echo "If the browser didn't open, visit: https://claude.com/cai/oauth/authorize?code=true&state=abc"
printf 'Paste code here if prompted > '
while read -r line; do
  case "$line" in
    good*) echo "Login successful."; exit 0 ;;
    *) echo "Invalid code. Please make sure the full code was copied." >&2 ;;
  esac
done
exit 1
`

// codexNarration mirrors `codex login --device-auth`, colour codes included:
// the CLI polls on its own, so there is nothing to submit.
const codexNarration = `#!/usr/bin/env sh
if [ "$1" != "login" ] || [ "$2" != "--device-auth" ]; then exit 64; fi
echo ""
echo "Follow these steps to sign in with ChatGPT using device code authorization:"
echo "1. Open this link in your browser and sign in to your account"
printf '   \033[94mhttps://auth.openai.com/codex/device\033[0m\n'
echo "2. Enter this one-time code"
printf '   \033[94mBDJL-IOS16\033[0m\n'
`

// fakeCodexQuick authorizes immediately; fakeCodexWaiting parks on stdin so the
// session stays in awaiting_authorization.
const (
	fakeCodexQuick   = codexNarration + "exit 0\n"
	fakeCodexWaiting = codexNarration + "read -r _\nexit 0\n"
)

func newTestManager(t *testing.T, provider config.Provider, script string) (*Manager, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a Unix shell script")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, string(provider))
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(strings.ToUpper(string(provider))+"_BIN", bin)
	m := New(Options{Discovery: podiomexec.Discovery{ExtraDirs: []string{dir}}})
	t.Cleanup(m.Shutdown)
	return m, dir
}

// waitPhase polls until the session reaches want, or fails the test.
func waitPhase(t *testing.T, m *Manager, id string, want Phase) Session {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last Session
	for time.Now().Before(deadline) {
		sess, err := m.Get(id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		last = sess
		if sess.Phase == want {
			return sess
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("phase = %q (message %q), want %q", last.Phase, last.Message, want)
	return last
}

func TestClaudeLoginScrapesURLAndAcceptsCode(t *testing.T) {
	m, _ := newTestManager(t, config.ProviderClaude, fakeClaude)

	started, err := m.Start(context.Background(), config.ProviderClaude, "work", t.TempDir())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !started.NeedsCode {
		t.Fatalf("NeedsCode = false, want true for Claude")
	}

	sess := waitPhase(t, m, started.ID, PhaseAwaitingCode)
	if !strings.HasPrefix(sess.URL, "https://claude.com/cai/oauth/authorize?") {
		t.Fatalf("URL = %q, want the manual authorize URL", sess.URL)
	}

	if _, err := m.Submit(started.ID, "good#abc"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	final := waitPhase(t, m, started.ID, PhaseSucceeded)
	if final.Message != "" {
		t.Fatalf("Message = %q, want empty on success", final.Message)
	}
}

func TestClaudeLoginRejectedCodeStaysOpen(t *testing.T) {
	m, _ := newTestManager(t, config.ProviderClaude, fakeClaude)

	started, err := m.Start(context.Background(), config.ProviderClaude, "", "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitPhase(t, m, started.ID, PhaseAwaitingCode)

	if _, err := m.Submit(started.ID, "wrong#abc"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	// The CLI complains and keeps waiting, so the session returns to
	// awaiting_code rather than failing.
	sess := waitPhase(t, m, started.ID, PhaseAwaitingCode)
	// The message is the CLI's rejection alone: the newline-less "Paste code
	// here if prompted > " prompt is still buffered when stderr arrives, and
	// echoing the user's own prompt back would be noise.
	if sess.Message != "Invalid code. Please make sure the full code was copied." {
		t.Fatalf("Message = %q, want only the CLI's rejection text", sess.Message)
	}

	// A retry on the same session still works.
	if _, err := m.Submit(started.ID, "good#abc"); err != nil {
		t.Fatalf("Submit retry: %v", err)
	}
	waitPhase(t, m, started.ID, PhaseSucceeded)
}

func TestSubmitRejectsMalformedCode(t *testing.T) {
	m, _ := newTestManager(t, config.ProviderClaude, fakeClaude)
	started, err := m.Start(context.Background(), config.ProviderClaude, "", "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitPhase(t, m, started.ID, PhaseAwaitingCode)

	for _, code := range []string{"", "   ", "good#abc\nextra", "good#abc\rmore"} {
		if _, err := m.Submit(started.ID, code); err != ErrInvalidCode {
			t.Fatalf("Submit(%q) err = %v, want ErrInvalidCode", code, err)
		}
	}
}

func TestCodexLoginScrapesURLAndUserCode(t *testing.T) {
	m, _ := newTestManager(t, config.ProviderCodex, fakeCodexQuick)

	started, err := m.Start(context.Background(), config.ProviderCodex, "", "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if started.NeedsCode {
		t.Fatalf("NeedsCode = true, want false for Codex device auth")
	}

	final := waitPhase(t, m, started.ID, PhaseSucceeded)
	if final.URL != "https://auth.openai.com/codex/device" {
		t.Fatalf("URL = %q, want the device URL with ANSI stripped", final.URL)
	}
	if final.UserCode != "BDJL-IOS16" {
		t.Fatalf("UserCode = %q, want BDJL-IOS16", final.UserCode)
	}
}

func TestCodexLoginTakesNoCode(t *testing.T) {
	m, _ := newTestManager(t, config.ProviderCodex, fakeCodexWaiting)

	started, err := m.Start(context.Background(), config.ProviderCodex, "", "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitPhase(t, m, started.ID, PhaseAwaitingAuthorization)
	if _, err := m.Submit(started.ID, "whatever"); err != ErrNoCodeExpected {
		t.Fatalf("Submit err = %v, want ErrNoCodeExpected", err)
	}
}

func TestFailedLoginReportsCLIOutput(t *testing.T) {
	m, _ := newTestManager(t, config.ProviderClaude,
		"#!/usr/bin/env sh\necho 'Login failed: browser unreachable' >&2\nexit 1\n")

	started, err := m.Start(context.Background(), config.ProviderClaude, "", "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	sess := waitPhase(t, m, started.ID, PhaseFailed)
	if !strings.Contains(sess.Message, "browser unreachable") {
		t.Fatalf("Message = %q, want the CLI's failure text", sess.Message)
	}
}

func TestCancelEndsSessionAndKillsProcess(t *testing.T) {
	m, _ := newTestManager(t, config.ProviderClaude, fakeClaude)
	started, err := m.Start(context.Background(), config.ProviderClaude, "", "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitPhase(t, m, started.ID, PhaseAwaitingCode)

	if err := m.Cancel(started.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	sess, err := m.Get(started.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if sess.Phase != PhaseFailed || sess.Message != "cancelled" {
		t.Fatalf("session = %+v, want failed/cancelled", sess)
	}
	// Submitting into a dead session must not panic or block.
	if _, err := m.Submit(started.ID, "good#abc"); err == nil {
		t.Fatalf("Submit after cancel succeeded, want an error")
	}
}

func TestStartSupersedesEarlierSessionForSameProfile(t *testing.T) {
	m, _ := newTestManager(t, config.ProviderClaude, fakeClaude)

	first, err := m.Start(context.Background(), config.ProviderClaude, "work", t.TempDir())
	if err != nil {
		t.Fatalf("Start first: %v", err)
	}
	waitPhase(t, m, first.ID, PhaseAwaitingCode)

	second, err := m.Start(context.Background(), config.ProviderClaude, "work", t.TempDir())
	if err != nil {
		t.Fatalf("Start second: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("second session reused the first id")
	}
	old, err := m.Get(first.ID)
	if err != nil {
		t.Fatalf("Get first: %v", err)
	}
	if old.Phase != PhaseFailed || !strings.Contains(old.Message, "superseded") {
		t.Fatalf("first session = %+v, want superseded", old)
	}
	waitPhase(t, m, second.ID, PhaseAwaitingCode)
}

func TestStartUnsupportedProvider(t *testing.T) {
	m := New(Options{})
	if _, err := m.Start(context.Background(), config.Provider("nope"), "", ""); err == nil {
		t.Fatalf("Start with unknown provider succeeded, want ErrUnsupported")
	}
}

func TestLoginTimesOut(t *testing.T) {
	m, dir := newTestManager(t, config.ProviderClaude, fakeClaude)
	m = New(Options{Discovery: podiomexec.Discovery{ExtraDirs: []string{dir}}, TTL: 300 * time.Millisecond})
	t.Cleanup(m.Shutdown)

	started, err := m.Start(context.Background(), config.ProviderClaude, "", "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	sess := waitPhase(t, m, started.ID, PhaseFailed)
	if !strings.Contains(sess.Message, "timed out") {
		t.Fatalf("Message = %q, want a timeout message", sess.Message)
	}
}

func TestGetUnknownSession(t *testing.T) {
	m := New(Options{})
	if _, err := m.Get("nope"); err != ErrNotFound {
		t.Fatalf("Get err = %v, want ErrNotFound", err)
	}
}
