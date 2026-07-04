// Package providercheck performs lightweight, credential-safe checks for the
// native CLIs Podiom orchestrates.
package providercheck

import (
	"bytes"
	"context"
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

	switch provider {
	case config.ProviderClaude:
		out, err := runCapture(ctx, timeout, found.Path, "doctor")
		status.LoginChecked = true
		status.Doctor = trimOutput(out)
		if err == nil {
			status.Ready = true
			return status
		}
		if status.Error == "" {
			status.Error = err.Error()
		}
		// Older/newer Claude builds may not expose a non-interactive doctor. A
		// discovered, version-reporting binary is enough to let onboarding offer
		// the native login flow and then perform the real LLM generation.
		status.Ready = status.Version != ""
	case config.ProviderCodex:
		status.LoginChecked = false
		status.Ready = status.Version != ""
	default:
		status.Error = fmt.Sprintf("unknown provider %q", provider)
	}
	return status
}

// CheckAll inspects Claude and Codex.
func CheckAll(ctx context.Context, opts Options) []Status {
	return []Status{
		Check(ctx, config.ProviderClaude, opts),
		Check(ctx, config.ProviderCodex, opts),
	}
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

// LoginArgs returns login commands that are safe inside containers and HA
// Ingress: device/terminal auth instead of localhost callback URLs.
func LoginArgs(provider config.Provider) ([]string, error) {
	switch provider {
	case config.ProviderClaude:
		return []string{"/login"}, nil
	case config.ProviderCodex:
		return []string{"login", "--device-auth"}, nil
	default:
		return nil, fmt.Errorf("unknown provider %q", provider)
	}
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
	switch provider {
	case config.ProviderClaude:
		return "Install Claude Code with Anthropic's current instructions, commonly: npm install -g @anthropic-ai/claude-code"
	case config.ProviderCodex:
		return "Install Codex CLI with OpenAI's current instructions, commonly: npm install -g @openai/codex"
	default:
		return ""
	}
}

func loginHint(provider config.Provider) string {
	switch provider {
	case config.ProviderClaude:
		if runtime.GOOS == "windows" {
			return "Run claude /login and follow the browser/device login prompts."
		}
		return "Run claude /login and follow the native Claude Code login prompts."
	case config.ProviderCodex:
		return "Run codex login --device-auth and follow the OpenAI account prompts."
	default:
		return ""
	}
}
