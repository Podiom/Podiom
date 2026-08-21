package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// bootstrapPin is one pinned download of an installer Podiom can provision for
// itself. Pins live here rather than in ha/versions.env because bootstrapping
// has to work on a standalone host too, where there is no image build.
type bootstrapPin struct {
	Version string
	URL     string
	SHA256  string
}

// bootstrapPins maps installer name → GOOS/GOARCH → pinned release.
//
// uv is the only entry, and deliberately so: it is a dependency-free static
// binary of a few tens of MB that unlocks both the `uv` installer and CPython
// itself (`uv python install`). go and cargo are full toolchains measured in
// hundreds of MB — the Home Assistant app already has a first-class path for
// those (the `toolchains` option), and a standalone user owns their host. They
// get an actionable error instead of a surprise download.
//
// Checksums are the .sha256 files published alongside each release asset.
// Verified 2026-08-21 against github.com/astral-sh/uv releases.
var bootstrapPins = map[string]map[string]bootstrapPin{
	"uv": {
		"darwin/arm64": {
			Version: "0.11.26",
			URL:     "https://github.com/astral-sh/uv/releases/download/0.11.26/uv-aarch64-apple-darwin.tar.gz",
			SHA256:  "8f7fbf1708399b921857bce71e1d60f0d3ccf52a30caebc1c1a2f175dce13ab6",
		},
		"darwin/amd64": {
			Version: "0.11.26",
			URL:     "https://github.com/astral-sh/uv/releases/download/0.11.26/uv-x86_64-apple-darwin.tar.gz",
			SHA256:  "922b460202707dd5f4ccacbadbe7f6a546cc46e82a99bf50ca99a7977a78eddd",
		},
		"linux/amd64": {
			Version: "0.11.26",
			URL:     "https://github.com/astral-sh/uv/releases/download/0.11.26/uv-x86_64-unknown-linux-gnu.tar.gz",
			SHA256:  "6426a73c3837e6e2483ee344cbc00f36394d179afcba6183cb77437e67db4af0",
		},
		"linux/arm64": {
			Version: "0.11.26",
			URL:     "https://github.com/astral-sh/uv/releases/download/0.11.26/uv-aarch64-unknown-linux-gnu.tar.gz",
			SHA256:  "befa1a59c91e96eb601b0fd9a97c03dd666f17baba644b2b4db9c59a767e387e",
		},
		"windows/amd64": {
			Version: "0.11.26",
			URL:     "https://github.com/astral-sh/uv/releases/download/0.11.26/uv-x86_64-pc-windows-msvc.zip",
			SHA256:  "4e1278ede866be6c0bf32d2f466cc6de7a9fb399ecf20c9ce2d186e52424be47",
		},
	},
}

// missingInstallerHelp explains what to do about an installer Podiom will not
// bootstrap. Written for whoever reads the failure — the agent relays it, and
// the user acts on it — so each names the actual fix rather than the symptom.
var missingInstallerHelp = map[string]string{
	"go":    "In the Home Assistant app, tick the `go` toolchain on the app's Configuration page and restart it. On a standalone install, install Go on the host.",
	"cargo": "In the Home Assistant app, tick the `rust` toolchain on the app's Configuration page and restart it. On a standalone install, install Rust on the host.",
	"npm":   "Podiom's provider CLIs run on Node, so npm is normally present — this usually means the daemon was started with an unusual PATH rather than that Node is missing.",
}

// bootstrapInstaller provisions a missing installer into the toolset's private
// boot/ directory and returns the path to run. boot/ is never on an agent's
// PATH: a bootstrapped uv is Podiom's own copy, not a tool the agent installed.
//
// The download is checksum-pinned and extracted through the same guarded
// extractor as any other archive, so a bootstrap is no more trusted than an
// agent-requested install.
func bootstrapInstaller(ctx context.Context, name string, d Dirs) (string, error) {
	platform := runtime.GOOS + "/" + runtime.GOARCH
	pin, ok := bootstrapPins[name][platform]
	if !ok {
		if help := missingInstallerHelp[name]; help != "" {
			return "", fmt.Errorf("%s is not on PATH. %s", name, help)
		}
		if _, known := bootstrapPins[name]; known {
			return "", fmt.Errorf("%s is not on PATH, and Podiom has no pinned %s build for %s — install it on the host", name, name, platform)
		}
		return "", fmt.Errorf("%s is not on PATH; install it on the host", name)
	}

	// Versioned directory: bumping a pin provisions a fresh copy rather than
	// silently reusing the old binary.
	dir := filepath.Join(d.Boot, name+"-"+pin.Version)
	if exe, err := findArchiveExecutable(dir, name, ""); err == nil {
		return exe, nil
	}

	tmp, err := fetchVerified(ctx, pin.URL, pin.SHA256, d.Boot, "."+name+"-*.archive")
	if err != nil {
		return "", fmt.Errorf("bootstrap %s %s: %w", name, pin.Version, err)
	}
	defer os.Remove(tmp)

	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("bootstrap %s: %w", name, err)
	}
	if err := extractArchive(tmp, dir); err != nil {
		return "", fmt.Errorf("bootstrap %s %s: %w", name, pin.Version, err)
	}
	exe, err := findArchiveExecutable(dir, name, "")
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("bootstrap %s %s: %w", name, pin.Version, err)
	}
	if err := os.Chmod(exe, 0o755); err != nil {
		return "", fmt.Errorf("bootstrap %s: %w", name, err)
	}
	return exe, nil
}
