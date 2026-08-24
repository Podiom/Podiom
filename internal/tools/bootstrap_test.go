package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// refusingTransport fails every request, proving a code path did not make one.
type refusingTransport struct{}

func (refusingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("no download expected here")
}

// withPin swaps the bootstrap table for one pinned to a local test archive.
func withPin(t *testing.T, name string, payload []byte) {
	t.Helper()
	digest := serveArchive(t, payload)
	orig := bootstrapPins
	bootstrapPins = map[string]map[string]bootstrapPin{
		name: {runtime.GOOS + "/" + runtime.GOARCH: {
			Version: "9.9.9",
			URL:     "https://example.test/" + name + ".tar.gz",
			SHA256:  digest,
		}},
	}
	t.Cleanup(func() { bootstrapPins = orig })
}

func TestBootstrapProvisionsMissingInstaller(t *testing.T) {
	d := DirsFor(filepath.Join(t.TempDir(), "toolset"))
	payload := makeTarGz(t, []tarEntry{
		{name: "fakeuv-9.9.9/fakeuv", body: "#!/bin/sh\necho fakeuv 9.9.9\n", mode: 0o755},
	})
	withPin(t, "fakeuv", payload)

	exe, err := bootstrapInstaller(context.Background(), "fakeuv", d)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if !strings.HasPrefix(exe, filepath.Join(d.Boot, "fakeuv-9.9.9")) {
		t.Fatalf("bootstrapped to %q, want a versioned dir under %q", exe, d.Boot)
	}
	out, err := exec.Command(exe).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "fakeuv 9.9.9") {
		t.Fatalf("bootstrapped binary does not run: %q %v", out, err)
	}

	// boot/ is Podiom's own cache, never an agent PATH entry — otherwise a
	// bootstrapped uv would shadow the host's uv for every agent command.
	for _, dir := range PathDirs(d.Root) {
		if strings.HasPrefix(exe, dir) {
			t.Fatalf("bootstrapped installer %q is on the agent PATH dir %q", exe, dir)
		}
	}

	// A second call reuses the extracted copy rather than downloading again —
	// so a client that refuses every request must not be reached.
	restore := SetHTTPClientForTest(&http.Client{Transport: refusingTransport{}})
	defer restore()
	again, err := bootstrapInstaller(context.Background(), "fakeuv", d)
	if err != nil || again != exe {
		t.Fatalf("reuse: %q %v", again, err)
	}
}

func TestBootstrapRejectsChecksumMismatch(t *testing.T) {
	d := DirsFor(filepath.Join(t.TempDir(), "toolset"))
	payload := makeTarGz(t, []tarEntry{{name: "fakeuv", body: "x", mode: 0o755}})
	withPin(t, "fakeuv", payload)
	// Corrupt the pin so the served bytes no longer match.
	pin := bootstrapPins["fakeuv"][runtime.GOOS+"/"+runtime.GOARCH]
	bad := sha256.Sum256([]byte("something else"))
	pin.SHA256 = hex.EncodeToString(bad[:])
	bootstrapPins["fakeuv"][runtime.GOOS+"/"+runtime.GOARCH] = pin

	_, err := bootstrapInstaller(context.Background(), "fakeuv", d)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want a checksum refusal", err)
	}
	if entries, _ := os.ReadDir(d.Boot); len(entries) != 0 {
		t.Fatalf("a rejected bootstrap must leave nothing behind: %+v", entries)
	}
}

// TestMissingInstallerWithoutPinExplainsItself checks that the installers
// Podiom will not bootstrap fail with something the user can act on, rather
// than a bare lookup error.
func TestMissingInstallerWithoutPinExplainsItself(t *testing.T) {
	d := DirsFor(filepath.Join(t.TempDir(), "toolset"))
	orig := bootstrapPins
	bootstrapPins = map[string]map[string]bootstrapPin{}
	defer func() { bootstrapPins = orig }()

	for _, tc := range []struct{ name, want string }{
		{"go", "toolchain"},
		{"cargo", "toolchain"},
		{"npm", "Node"},
	} {
		_, err := bootstrapInstaller(context.Background(), tc.name, d)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want mention of %q", tc.name, err, tc.want)
		}
	}
}

// TestUnpinnedPlatformIsNamed covers the pinned-but-not-for-this-platform case,
// which otherwise reads as "uv is missing" with no hint that the build simply
// is not published.
func TestUnpinnedPlatformIsNamed(t *testing.T) {
	d := DirsFor(filepath.Join(t.TempDir(), "toolset"))
	orig := bootstrapPins
	bootstrapPins = map[string]map[string]bootstrapPin{"fakeuv": {"plan9/mips": {}}}
	defer func() { bootstrapPins = orig }()

	_, err := bootstrapInstaller(context.Background(), "fakeuv", d)
	if err == nil || !strings.Contains(err.Error(), runtime.GOOS+"/"+runtime.GOARCH) {
		t.Fatalf("err = %v, want the current platform named", err)
	}
}
