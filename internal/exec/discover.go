// Package exec isolates every cross-platform subprocess concern: locating the
// claude/codex binaries (including Windows npm .cmd/.exe shims), and starting and
// killing child processes via process groups with context cancellation. All OS
// differences in Podiom live here (Principle 4 / §10 / R10.3 / R10.4).
package exec

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Discovery locates a provider CLI binary on the host.
type Discovery struct {
	// ExtraDirs are searched in addition to PATH and the conventional npm global
	// locations (useful for tests or unusual installs).
	ExtraDirs []string
}

// Found describes a discovered binary.
type Found struct {
	Name string // the logical name asked for, e.g. "claude"
	Path string // absolute path to the executable (or shim on Windows)
}

// Find locates a binary by logical name (e.g. "claude" or "codex"). It first
// honours an explicit override via the <NAME>_BIN environment variable
// (e.g. CLAUDE_BIN), then falls back to PATH lookup (which on Windows resolves
// .cmd/.exe shims via PATHEXT), then to conventional npm global bin locations.
func (d Discovery) Find(name string) (Found, error) {
	if override := os.Getenv(envOverride(name)); override != "" {
		if abs, err := resolveExecutable(override); err == nil {
			return Found{Name: name, Path: abs}, nil
		}
		return Found{}, fmt.Errorf("%s set to %q but it is not an executable", envOverride(name), override)
	}

	// PATH lookup. On Windows, LookPath consults PATHEXT, so "claude" resolves to
	// claude.cmd / claude.exe automatically.
	for _, candidate := range candidateNames(name) {
		if p, err := exec.LookPath(candidate); err == nil {
			if abs, aerr := filepath.Abs(p); aerr == nil {
				return Found{Name: name, Path: abs}, nil
			}
			return Found{Name: name, Path: p}, nil
		}
	}

	// Conventional npm global bin directories (npm installs claude/codex as shims).
	searchDirs := append(npmGlobalBinDirs(), d.ExtraDirs...)
	for _, dir := range searchDirs {
		for _, candidate := range candidateNames(name) {
			full := filepath.Join(dir, candidate)
			if abs, err := resolveExecutable(full); err == nil {
				return Found{Name: name, Path: abs}, nil
			}
		}
	}

	return Found{}, fmt.Errorf("%s CLI not found on PATH or npm global locations (set %s to override)", name, envOverride(name))
}

// candidateNames returns the platform-specific filenames to try for a logical
// binary name. On Windows, npm installs both a .cmd shim and sometimes a .exe.
func candidateNames(name string) []string {
	if runtime.GOOS == "windows" {
		return []string{name + ".cmd", name + ".exe", name + ".bat", name}
	}
	return []string{name}
}

func envOverride(name string) string {
	return strings.ToUpper(name) + "_BIN"
}

// resolveExecutable returns the absolute path if the file exists and is
// executable (on Windows, existence is sufficient — execute bits aren't used).
func resolveExecutable(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("%s is not executable", path)
	}
	return filepath.Abs(path)
}

// npmGlobalBinDirs returns conventional locations where npm places global bin
// shims, so a CLI installed via `npm i -g` is found even if PATH wasn't updated.
func npmGlobalBinDirs() []string {
	var dirs []string
	home, _ := os.UserHomeDir()

	if runtime.GOOS == "windows" {
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			dirs = append(dirs, filepath.Join(appdata, "npm"))
		}
		if home != "" {
			dirs = append(dirs, filepath.Join(home, "AppData", "Roaming", "npm"))
		}
		return dirs
	}

	// Unix-like: common global prefixes' bin dirs.
	dirs = append(dirs,
		"/usr/local/bin",
		"/opt/homebrew/bin",
	)
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".npm-global", "bin"),
			filepath.Join(home, "n", "bin"),
		)
	}
	if prefix := os.Getenv("NPM_CONFIG_PREFIX"); prefix != "" {
		dirs = append(dirs, filepath.Join(prefix, "bin"))
	}
	return dirs
}
