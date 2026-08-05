package providerlogin

import (
	"regexp"
	"strings"

	"github.com/Podiom/Podiom/internal/config"
)

// loginFlow is one provider's browser-login shape: whether the user pastes a
// code back, and how to read the CLI's narration.
type loginFlow struct {
	// needsCode distinguishes Claude's manual OAuth redirect (the user copies
	// a code from the redirect page) from Codex's device code (the CLI polls).
	needsCode bool
	// parse folds one ANSI-stripped output line into the session state. It is
	// called under the Manager lock and must not block.
	parse func(line string, s *Session)
}

// loginFlows is the per-layer table of browser-login parsers, keyed by provider
// exactly like providercheck.authProbes. A provider without an entry reports
// ErrUnsupported and the UI falls back to the terminal instructions.
var loginFlows = map[config.Provider]loginFlow{
	config.ProviderClaude: {needsCode: true, parse: parseClaudeLine},
	config.ProviderCodex:  {needsCode: false, parse: parseCodexLine},
}

// Supported reports whether a provider can be signed in from the browser. The
// UI falls back to the terminal instructions for anything else.
func Supported(provider config.Provider) bool {
	_, ok := loginFlows[provider]
	return ok
}

var urlPattern = regexp.MustCompile(`https?://\S+`)

// parseClaudeLine reads `claude auth login`, which prints:
//
//	Opening browser to sign in…
//	If the browser didn't open, visit: https://claude.com/cai/oauth/authorize?...
//	Paste code here if prompted >
//
// The printed URL is the manual-redirect variant (redirect_uri points at
// platform.claude.com/oauth/code/callback), so it works from any browser on any
// device and ends on a page showing a "code#state" string to paste back. The
// loopback variant is only ever handed to the browser the CLI opens locally,
// which is useless for a remote daemon.
func parseClaudeLine(line string, s *Session) {
	if strings.Contains(line, "visit:") && s.URL == "" {
		if url := urlPattern.FindString(line); url != "" {
			s.URL = url
			s.Phase = PhaseAwaitingCode
			return
		}
	}
	// A rejected code leaves the CLI running and waiting for another attempt,
	// so this is a message, not a terminal state. The text is sliced from the
	// match because "Paste code here if prompted > " has no trailing newline
	// and is therefore still buffered when the rejection arrives on stderr —
	// showing the user their own prompt back would be noise.
	if i := strings.Index(line, "Invalid code"); i >= 0 {
		s.Message = strings.TrimSpace(line[i:])
		if s.URL != "" {
			s.Phase = PhaseAwaitingCode
		}
		return
	}
	if rest, ok := cutPrefix(line, "Login failed:"); ok {
		s.Message = rest
	}
}

// codePattern matches Codex's one-time device code, e.g. "BDJL-IOS16". It is
// anchored to a whole trimmed line so URLs and prose cannot match.
var codePattern = regexp.MustCompile(`^[A-Z0-9]{4,8}-[A-Z0-9]{4,8}$`)

// parseCodexLine reads `codex login --device-auth`, which prints (in colour,
// even on a pipe):
//
//  1. Open this link in your browser and sign in to your account
//     https://auth.openai.com/codex/device
//
//  2. Enter this one-time code (expires in 15 minutes)
//     BDJL-IOS16
//
// There is nothing to submit: the CLI polls and exits on its own.
func parseCodexLine(line string, s *Session) {
	trimmed := strings.TrimSpace(line)
	if s.URL == "" {
		if url := urlPattern.FindString(trimmed); url != "" {
			s.URL = url
			s.Phase = PhaseAwaitingAuthorization
			return
		}
	}
	if s.UserCode == "" && codePattern.MatchString(trimmed) {
		s.UserCode = trimmed
		return
	}
	if strings.Contains(trimmed, "device auth timed out") ||
		strings.Contains(trimmed, "device code login is not enabled") {
		s.Message = trimmed
	}
}

// cutPrefix returns the trimmed remainder after prefix, if line carries it.
func cutPrefix(line, prefix string) (string, bool) {
	i := strings.Index(line, prefix)
	if i < 0 {
		return "", false
	}
	return strings.TrimSpace(line[i+len(prefix):]), true
}
