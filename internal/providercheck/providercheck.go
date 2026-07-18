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
	"time"

	"github.com/Podiom/Podiom/internal/config"
	podiomexec "github.com/Podiom/Podiom/internal/exec"
)

const defaultTimeout = 8 * time.Second

// Status describes one provider CLI from a user's point of view.
type Status struct {
	Provider     config.Provider
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
}

// Check inspects one provider without reading or storing credentials.
func Check(ctx context.Context, provider config.Provider, opts Options) Status {
	status := Status{
		Provider:    provider,
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

	version, err := runCapture(ctx, timeout, found.Path, "--version")
	if err != nil {
		status.Error = err.Error()
	} else {
		status.Version = firstLine(version)
	}

	if probe, ok := authProbes[provider]; ok {
		probe(ctx, timeout, found.Path, &status)
	} else {
		status.Error = fmt.Sprintf("unknown provider %q", provider)
	}
	return status
}

// authProbes holds the per-provider credential-safe login/readiness probes.
// A provider without an entry is reported as unknown; a new provider whose CLI
// has no probe can use a version-only probe (Ready = Version != "").
var authProbes = map[config.Provider]func(ctx context.Context, timeout time.Duration, path string, status *Status){
	config.ProviderClaude: probeClaude,
	config.ProviderCodex:  probeCodex,
}

func probeClaude(ctx context.Context, timeout time.Duration, path string, status *Status) {
	if out, err := runCapture(ctx, timeout, path, "auth", "status"); parseClaudeAuthStatus(out, status) {
		status.Ready = status.Version != ""
		return
	} else if err != nil && status.Error == "" && !errors.Is(err, context.DeadlineExceeded) {
		status.Error = err.Error()
	}

	out, err := runCapture(ctx, timeout, path, "doctor")
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

func probeCodex(ctx context.Context, timeout time.Duration, path string, status *Status) {
	out, err := runCapture(ctx, timeout, path, "login", "status")
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

// CheckAll inspects every registered provider.
func CheckAll(ctx context.Context, opts Options) []Status {
	ids := config.ProviderIDs()
	out := make([]Status, 0, len(ids))
	for _, id := range ids {
		out = append(out, Check(ctx, id, opts))
	}
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

func runCapture(ctx context.Context, timeout time.Duration, bin string, args ...string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, bin, args...)
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
