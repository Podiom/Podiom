package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// execCommand is swapped in tests so installer behavior can be exercised
// without npm/uv/go on the machine.
var execCommand = exec.CommandContext

// httpClient is swapped in tests. Installs are user-approved, deliberate
// actions; a generous timeout guards against a hung download, the caller's
// ctx bounds the whole install.
var httpClient = &http.Client{Timeout: 5 * time.Minute}

// SetHTTPClientForTest swaps the download client so tests (including other
// packages' integration tests) can serve binaries from a local server. Never
// call outside tests.
func SetHTTPClientForTest(c *http.Client) (restore func()) {
	orig := httpClient
	httpClient = c
	return func() { httpClient = orig }
}

// Result reports a successful install.
type Result struct {
	// Path is where the executable was found after installing.
	Path string
	// VersionOutput is the first line of `tool --version`, best-effort.
	VersionOutput string
}

// outputTail caps how much installer output is kept for error reporting.
const outputTail = 2000

// Install performs one workspace tool install per the spec (§4): run the
// declarative installer (or download+verify for binary), confirm the expected
// executable landed in the agent's tool dirs, capture best-effort version
// evidence, and record the manifest entry. The caller's ctx carries the
// overall timeout.
func Install(ctx context.Context, spec Spec, root string, entry ManifestEntry) (Result, error) {
	if err := spec.Validate(); err != nil {
		return Result{}, err
	}
	if !spec.Installable() {
		return Result{}, fmt.Errorf("tool %q is a host-only request; nothing to install", spec.Tool)
	}
	d := DirsFor(root)
	for _, dir := range []string{d.Bin, d.NPM, d.UV} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Result{}, fmt.Errorf("create tool dir: %w", err)
		}
	}

	if spec.Installer == InstallerBinary {
		if err := downloadBinary(ctx, spec, d); err != nil {
			return Result{}, err
		}
	} else {
		argv, env := Command(spec, d)
		if err := runInstaller(ctx, argv, env); err != nil {
			return Result{}, err
		}
	}

	// Verify inside the agent's tool dirs only — a host binary of the same
	// name must never satisfy the check.
	path, found := findExecutable(root, spec.Tool)
	if !found {
		return Result{}, fmt.Errorf("install finished but %q did not appear in the agent's tool directories — check the package actually provides that executable", spec.Tool)
	}

	version := probeVersion(ctx, path)

	entry.Tool = spec.Tool
	entry.Installer = string(spec.Installer)
	entry.Package = spec.Package
	entry.Version = spec.Version
	entry.URL = spec.URL
	entry.SHA256 = spec.SHA256
	entry.VersionOutput = version
	if err := recordInstall(d, entry); err != nil {
		return Result{}, err
	}
	return Result{Path: path, VersionOutput: version}, nil
}

// Uninstall reverses an install (§5). The manifest entry is removed even when
// the installer-specific cleanup fails — the manifest must never claim a tool
// the user asked to remove — but the cleanup error is still reported.
func Uninstall(ctx context.Context, root, tool string) error {
	d := DirsFor(root)
	entry, found, err := removeManifestEntry(d, tool)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("tool %q is not in the manifest", tool)
	}

	var cleanupErr error
	if argv, env := UninstallCommand(entry, d); argv != nil {
		cleanupErr = runInstaller(ctx, argv, env)
	}
	// Best-effort shim/binary removal for every installer: uv/go/binary all
	// place (or link) the executable under bin/, npm under npm/bin.
	for _, dir := range PathDirs(root) {
		if p := filepath.Join(dir, tool); p != "" {
			_ = os.Remove(p)
		}
	}
	if cleanupErr != nil {
		return fmt.Errorf("tool %q removed from the manifest, but cleanup failed: %w", tool, cleanupErr)
	}
	return nil
}

func runInstaller(ctx context.Context, argv, extraEnv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("no install command")
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		return fmt.Errorf("installer %q is not available on the host: %w", argv[0], err)
	}
	cmd := execCommand(ctx, argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), extraEnv...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed: %w\n%s", strings.Join(argv, " "), err, tail(out.String()))
	}
	return nil
}

// downloadBinary fetches a checksum-pinned executable over https. A digest
// mismatch discards the download.
func downloadBinary(ctx context.Context, spec Spec, d Dirs) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, spec.URL, nil)
	if err != nil {
		return fmt.Errorf("binary download: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("binary download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("binary download: %s returned %s", spec.URL, resp.Status)
	}

	tmp, err := os.CreateTemp(d.Bin, "."+spec.Tool+"-*")
	if err != nil {
		return fmt.Errorf("binary download: %w", err)
	}
	defer func() {
		tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hasher), resp.Body); err != nil {
		return fmt.Errorf("binary download: %w", err)
	}
	if got := hex.EncodeToString(hasher.Sum(nil)); got != spec.SHA256 {
		return fmt.Errorf("binary checksum mismatch: got %s, pinned %s — download discarded", got, spec.SHA256)
	}
	if err := tmp.Chmod(0o755); err != nil {
		return fmt.Errorf("binary download: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("binary download: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(d.Bin, spec.Tool)); err != nil {
		return fmt.Errorf("binary download: %w", err)
	}
	return nil
}

// probeVersion runs `tool --version` for evidence. Best-effort: tools without
// --version don't fail verification (§4).
func probeVersion(ctx context.Context, path string) string {
	vctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := execCommand(vctx, path, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	if len(line) > 200 {
		line = line[:200]
	}
	return line
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= outputTail {
		return s
	}
	return "…" + s[len(s)-outputTail:]
}
