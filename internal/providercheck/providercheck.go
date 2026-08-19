// Package providercheck performs lightweight, credential-safe checks for the
// native CLIs Podiom orchestrates.
package providercheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Podiom/Podiom/internal/config"
	podiomexec "github.com/Podiom/Podiom/internal/exec"
)

const defaultTimeout = 8 * time.Second

// Status describes one provider CLI from a user's point of view.
type Status struct {
	Provider     config.Provider
	Profile      string
	Found        bool
	Path         string
	Version      string
	Doctor       string
	Ready        bool
	LoginChecked bool
	LoggedIn     bool
	Error        string
	InstallHint  string
	LoginHint    string
}

// Options configures provider checks.
type Options struct {
	Discovery podiomexec.Discovery
	Timeout   time.Duration

	// Profile and ProfileDir scope the login probe to one named profile. An
	// empty ProfileDir probes the CLI's own global login, which is what the
	// "default" profile means everywhere else in Podiom.
	Profile    string
	ProfileDir string
}

// Check inspects one provider without reading or storing credentials.
func Check(ctx context.Context, provider config.Provider, opts Options) Status {
	status := Status{
		Provider:    provider,
		Profile:     opts.Profile,
		InstallHint: installHint(provider),
		LoginHint:   loginHint(provider),
	}
	name := string(provider)
	found, err := opts.Discovery.Find(name)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Found = true
	status.Path = found.Path
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	env := profileEnv(provider, opts.ProfileDir)

	version, err := runCapture(ctx, timeout, env, found.Path, "--version")
	if err != nil {
		status.Error = err.Error()
	} else {
		status.Version = firstLine(version)
	}

	if probe, ok := authProbes[provider]; ok {
		probe(ctx, timeout, env, found.Path, &status)
	} else {
		status.Error = fmt.Sprintf("unknown provider %q", provider)
	}
	return status
}

// profileEnv builds the probe environment for one profile. It mirrors the
// adapters: the provider's directory variable is always stripped first, so an
// inherited value can never make a profile look logged in on another's
// credentials. Returns nil (inherit) only when the provider is unknown.
func profileEnv(provider config.Provider, dir string) []string {
	info, ok := config.ProviderInfoFor(provider)
	if !ok || info.ProfileEnvVar == "" {
		return nil
	}
	return podiomexec.ProfileEnv(os.Environ(), info.ProfileEnvVar, dir)
}

// authProbes holds the per-provider credential-safe login/readiness probes.
// A provider without an entry is reported as unknown; a new provider whose CLI
// has no probe can use a version-only probe (Ready = Version != "").
var authProbes = map[config.Provider]func(ctx context.Context, timeout time.Duration, env []string, path string, status *Status){
	config.ProviderClaude: probeClaude,
	config.ProviderCodex:  probeCodex,
}

func probeClaude(ctx context.Context, timeout time.Duration, env []string, path string, status *Status) {
	if out, err := runCapture(ctx, timeout, env, path, "auth", "status"); parseClaudeAuthStatus(out, status) {
		status.Ready = status.Version != ""
		return
	} else if err != nil && status.Error == "" && !errors.Is(err, context.DeadlineExceeded) {
		status.Error = err.Error()
	}

	out, err := runCapture(ctx, timeout, env, path, "doctor")
	status.Doctor = trimOutput(out)
	if err == nil {
		status.Ready = true
		return
	}
	if status.Error == "" {
		status.Error = err.Error()
	}
	// Older/newer Claude builds may not expose a non-interactive doctor. A
	// discovered, version-reporting binary is enough to let onboarding offer
	// the native login flow and then perform the real LLM generation.
	status.Ready = status.Version != ""
}

func probeCodex(ctx context.Context, timeout time.Duration, env []string, path string, status *Status) {
	out, err := runCapture(ctx, timeout, env, path, "login", "status")
	status.LoginChecked = true
	if err == nil {
		status.LoggedIn = true
	} else if errors.Is(err, context.DeadlineExceeded) || looksLikeMissingSubcommand(out) {
		status.LoginChecked = false
		if status.Error == "" {
			status.Error = err.Error()
		}
	} else if status.Error == "" {
		status.Error = err.Error()
	}
	status.Ready = status.Version != ""
}

func parseClaudeAuthStatus(out string, status *Status) bool {
	i := strings.IndexByte(out, '{')
	if i < 0 {
		return false
	}
	var parsed struct {
		LoggedIn bool `json:"loggedIn"`
	}
	if err := json.Unmarshal([]byte(out[i:]), &parsed); err != nil {
		return false
	}
	status.LoginChecked = true
	status.LoggedIn = parsed.LoggedIn
	status.Error = ""
	return true
}

func looksLikeMissingSubcommand(out string) bool {
	lower := strings.ToLower(out)
	return strings.Contains(lower, "unrecognized") ||
		strings.Contains(lower, "unknown command") ||
		strings.Contains(lower, "usage:")
}

// refreshCommands holds the per-provider command that makes a CLI refresh its
// own expired OAuth token. Both CLIs do it as a side effect of their doctor
// command; their status commands do not — `claude auth status` is purely local
// and reports loggedIn even for a long-expired token.
var refreshCommands = map[config.Provider][]string{
	config.ProviderClaude: {"doctor"},
	config.ProviderCodex:  {"doctor"},
}

// RefreshCredentials asks the provider CLI to refresh its own OAuth token.
// Podiom never performs the token exchange: the CLI owns it and writes its own
// credential store (file or Keychain). A nil error means the command ran, not
// that a token was refreshed — the caller re-reads credentials to learn that.
//
// The failure modes are the CLI's own, and match what a real turn already does:
// a definitively rejected refresh token makes it clear its credentials, while an
// unreachable network leaves them untouched.
func RefreshCredentials(ctx context.Context, provider config.Provider, opts Options) error {
	args, ok := refreshCommands[provider]
	if !ok {
		return fmt.Errorf("unknown provider %q", provider)
	}
	found, err := opts.Discovery.Find(string(provider))
	if err != nil {
		return err
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	_, err = runCapture(ctx, timeout, profileEnv(provider, opts.ProfileDir), found.Path, args...)
	return err
}

// CheckAll inspects every registered provider's default (global) login.
func CheckAll(ctx context.Context, opts Options) []Status {
	ids := config.ProviderIDs()
	out := make([]Status, 0, len(ids))
	for _, id := range ids {
		out = append(out, Check(ctx, id, opts))
	}
	return out
}

// Target names one provider account to probe: a provider plus, optionally, a
// named profile and the auth directory that scopes it. An empty Profile is the
// provider's own global login.
type Target struct {
	Provider config.Provider
	Profile  string
	Dir      string
}

// Targets enumerates the per-provider implicit defaults plus every named
// profile, matching how usage snapshots fan out so the two views line up.
func Targets(profiles []config.Profile) []Target {
	out := make([]Target, 0, len(profiles)+2)
	for _, id := range config.ProviderIDs() {
		out = append(out, Target{Provider: id})
	}
	for _, p := range profiles {
		out = append(out, Target{Provider: p.Provider, Profile: p.Name, Dir: p.Dir()})
	}
	return out
}

// CheckTargets probes every target. Each probe spawns its own CLI and can burn
// the full timeout, so they run concurrently rather than serially — a handful of
// profiles would otherwise take the better part of a minute.
func CheckTargets(ctx context.Context, targets []Target, opts Options) []Status {
	out := make([]Status, len(targets))
	var wg sync.WaitGroup
	for i, target := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scoped := opts
			scoped.Profile = target.Profile
			scoped.ProfileDir = target.Dir
			out[i] = Check(ctx, target.Provider, scoped)
		}()
	}
	wg.Wait()
	return out
}

// RunNativeLogin starts the provider's own login flow in the current terminal.
func RunNativeLogin(ctx context.Context, provider config.Provider, path string) error {
	args, err := LoginArgs(provider)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// InstallPackage returns the npm package used to install a provider CLI.
func InstallPackage(provider config.Provider) (string, error) {
	if info, ok := config.ProviderInfoFor(provider); ok && info.InstallPackage != "" {
		return info.InstallPackage, nil
	}
	return "", fmt.Errorf("unknown provider %q", provider)
}

// NpmPath returns npm's executable path, or an empty string when unavailable.
func NpmPath() string {
	path, err := exec.LookPath("npm")
	if err != nil {
		return ""
	}
	return path
}

// RunInstall installs a provider CLI with npm in the current terminal.
func RunInstall(ctx context.Context, provider config.Provider) error {
	pkg, err := InstallPackage(provider)
	if err != nil {
		return err
	}
	npm := NpmPath()
	if npm == "" {
		return errors.New("npm not found")
	}
	cmd := exec.CommandContext(ctx, npm, "install", "-g", pkg)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// LoginArgs returns login commands that are safe inside containers and HA
// Ingress: device/terminal auth instead of localhost callback URLs.
func LoginArgs(provider config.Provider) ([]string, error) {
	if info, ok := config.ProviderInfoFor(provider); ok && len(info.LoginArgs) > 0 {
		return append([]string(nil), info.LoginArgs...), nil
	}
	return nil, fmt.Errorf("unknown provider %q", provider)
}

// runCapture runs bin and returns its merged output. A nil env inherits the
// daemon's environment; a non-nil env replaces it wholesale (see profileEnv).
func runCapture(ctx context.Context, timeout time.Duration, env []string, bin string, args ...string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, bin, args...)
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	text := trimOutput(out.String())
	if runCtx.Err() != nil {
		return text, runCtx.Err()
	}
	if err != nil {
		if text == "" {
			return text, err
		}
		return text, fmt.Errorf("%w: %s", err, text)
	}
	return text, nil
}

func firstLine(s string) string {
	s = trimOutput(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func trimOutput(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
}

func installHint(provider config.Provider) string {
	info, ok := config.ProviderInfoFor(provider)
	if !ok {
		return ""
	}
	return info.InstallHint
}

func loginHint(provider config.Provider) string {
	// Claude's browser-based login needs different phrasing on Windows; the
	// registry carries the common hint, this override stays OS-local.
	if provider == config.ProviderClaude && runtime.GOOS == "windows" {
		return "Run claude /login and follow the browser/device login prompts."
	}
	info, ok := config.ProviderInfoFor(provider)
	if !ok {
		return ""
	}
	return info.LoginHint
}
