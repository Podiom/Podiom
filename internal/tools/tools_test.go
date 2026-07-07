package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpecValidation(t *testing.T) {
	cases := []struct {
		name    string
		payload map[string]string
		ok      bool
	}{
		{"host-only needs just a tool", map[string]string{"tool": "brew-thing"}, true},
		{"missing tool", map[string]string{"installer": "npm", "package": "lychee"}, false},
		{"tool with a path", map[string]string{"tool": "../evil", "installer": "npm", "package": "x"}, false},
		{"tool with spaces", map[string]string{"tool": "rm -rf", "installer": "npm", "package": "x"}, false},
		{"npm ok", map[string]string{"tool": "lychee", "installer": "npm", "package": "@scope/lychee", "version": "1.2.0"}, true},
		{"npm missing package", map[string]string{"tool": "lychee", "installer": "npm"}, false},
		{"npm shell metachars", map[string]string{"tool": "x", "installer": "npm", "package": "a;rm -rf /"}, false},
		{"uv ok", map[string]string{"tool": "ruff", "installer": "uv", "package": "ruff"}, true},
		{"go ok", map[string]string{"tool": "gopls", "installer": "go", "package": "golang.org/x/tools/gopls"}, true},
		{"binary ok", map[string]string{"tool": "jq", "installer": "binary", "url": "https://example.com/jq", "sha256": strings.Repeat("ab", 32)}, true},
		{"binary http", map[string]string{"tool": "jq", "installer": "binary", "url": "http://example.com/jq", "sha256": strings.Repeat("ab", 32)}, false},
		{"binary bad sha", map[string]string{"tool": "jq", "installer": "binary", "url": "https://example.com/jq", "sha256": "short"}, false},
		{"unknown installer", map[string]string{"tool": "x", "installer": "brew", "package": "x"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := SpecFromPayload(tc.payload).Validate()
			if tc.ok && err != nil {
				t.Fatalf("want valid, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("want invalid, got ok")
			}
		})
	}
}

func TestCommandConstruction(t *testing.T) {
	d := DirsFor("/root/tools")
	cases := []struct {
		spec     Spec
		wantArgv string
		wantEnv  string
	}{
		{Spec{Tool: "lychee", Installer: InstallerNPM, Package: "lychee", Version: "1.2.0"},
			"npm install -g --prefix /root/tools/npm lychee@1.2.0", ""},
		{Spec{Tool: "ruff", Installer: InstallerUV, Package: "ruff"},
			"uv tool install ruff", "UV_TOOL_DIR=/root/tools/uv UV_TOOL_BIN_DIR=/root/tools/bin"},
		{Spec{Tool: "gopls", Installer: InstallerGo, Package: "golang.org/x/tools/gopls"},
			"go install golang.org/x/tools/gopls@latest", "GOBIN=/root/tools/bin"},
		{Spec{Tool: "jq", Installer: InstallerBinary, URL: "https://x/jq", SHA256: strings.Repeat("00", 32)},
			"", ""},
	}
	for _, tc := range cases {
		argv, env := Command(tc.spec, d)
		if got := strings.Join(argv, " "); got != tc.wantArgv {
			t.Errorf("%s argv = %q, want %q", tc.spec.Installer, got, tc.wantArgv)
		}
		if got := strings.Join(env, " "); got != tc.wantEnv {
			t.Errorf("%s env = %q, want %q", tc.spec.Installer, got, tc.wantEnv)
		}
	}
}

func TestBinaryInstallVerifyAndManifest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tools")
	payload := []byte("#!/bin/sh\necho fake-tool 9.9\n")
	sum := sha256.Sum256(payload)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	// The https-only rule is enforced by Validate; the local test server is
	// plain http, so point the client at it and use an https-shaped URL.
	origClient := httpClient
	httpClient = srv.Client()
	httpClient.Transport = rewriteTransport{base: srv.URL}
	defer func() { httpClient = origClient }()

	spec := Spec{
		Tool:      "faketool",
		Installer: InstallerBinary,
		URL:       "https://example.test/faketool",
		SHA256:    hex.EncodeToString(sum[:]),
	}
	res, err := Install(context.Background(), spec, root, ManifestEntry{RequestID: "req-1", GoalID: "goal-1"})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if res.Path != filepath.Join(root, "bin", "faketool") {
		t.Fatalf("path = %q", res.Path)
	}
	if !strings.Contains(res.VersionOutput, "fake-tool 9.9") {
		t.Fatalf("version output = %q", res.VersionOutput)
	}

	list, err := List(root)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Tool != "faketool" || list[0].Broken || list[0].RequestID != "req-1" {
		t.Fatalf("list = %+v", list)
	}

	// Checksum mismatch discards the download.
	bad := spec
	bad.Tool = "evil"
	bad.SHA256 = strings.Repeat("00", 32)
	if _, err := Install(context.Background(), bad, root, ManifestEntry{}); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("bad checksum err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "bin", "evil")); err == nil {
		t.Fatalf("mismatched download should not be installed")
	}

	// Out-of-band removal is reported as broken, not dropped.
	if err := os.Remove(res.Path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	list, _ = List(root)
	if len(list) != 1 || !list[0].Broken {
		t.Fatalf("expected broken entry, got %+v", list)
	}

	// Uninstall removes the manifest entry.
	if err := Uninstall(context.Background(), root, "faketool"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if list, _ = List(root); len(list) != 0 {
		t.Fatalf("after uninstall list = %+v", list)
	}
	if err := Uninstall(context.Background(), root, "faketool"); err == nil {
		t.Fatalf("double uninstall should fail")
	}
}

// TestInstallerVerificationRejectsMissingExecutable stubs the installer
// command with a no-op: install "succeeds" but produces no executable, so
// verification must fail.
func TestInstallerVerificationRejectsMissingExecutable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tools")
	orig := execCommand
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "true")
	}
	defer func() { execCommand = orig }()

	spec := Spec{Tool: "ghost", Installer: InstallerGo, Package: "example.com/ghost"}
	if _, err := Install(context.Background(), spec, root, ManifestEntry{}); err == nil || !strings.Contains(err.Error(), "did not appear") {
		t.Fatalf("err = %v, want verification failure", err)
	}
	if list, _ := List(root); len(list) != 0 {
		t.Fatalf("failed install must not land in the manifest: %+v", list)
	}
}

// TestStubbedInstallerSuccess simulates an installer that drops the expected
// executable into the agent bin, exercising the full success path without npm.
func TestStubbedInstallerSuccess(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tools")
	orig := execCommand
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "npm" {
			// Simulate the install by writing the executable where npm would.
			script := "mkdir -p " + filepath.Join(root, "npm", "bin") +
				" && printf '#!/bin/sh\\necho lychee 0.15\\n' > " + filepath.Join(root, "npm", "bin", "lychee") +
				" && chmod +x " + filepath.Join(root, "npm", "bin", "lychee")
			return exec.CommandContext(ctx, "sh", "-c", script)
		}
		return exec.CommandContext(ctx, name, args...)
	}
	defer func() { execCommand = orig }()

	res, err := Install(context.Background(), Spec{Tool: "lychee", Installer: InstallerNPM, Package: "lychee"}, root, ManifestEntry{})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !strings.Contains(res.VersionOutput, "lychee 0.15") {
		t.Fatalf("version = %q", res.VersionOutput)
	}
	if list, _ := List(root); len(list) != 1 || list[0].Installer != "npm" {
		t.Fatalf("manifest = %+v", list)
	}
}

// rewriteTransport sends every request to the test server regardless of URL.
type rewriteTransport struct{ base string }

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(t.base, "http://")
	return http.DefaultTransport.RoundTrip(req)
}
