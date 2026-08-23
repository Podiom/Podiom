// Package tools installs command-line tools into Podiom's shared toolset
// directory ($PODIOM_HOME/toolset) and manages the manifest that records what
// was installed (see docs/requirements/toolset.md).
//
// The load-bearing security property: an agent never authors a string that
// Podiom executes. Install commands are derived mechanically from a
// declarative, validated Spec and executed as argv arrays — never through a
// shell — by the same code that renders them for display.
package tools

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Podiom/Podiom/internal/config"
)

// Installer names one supported installation mechanism.
type Installer string

const (
	// InstallerNPM installs an npm package into the toolset's npm prefix.
	InstallerNPM Installer = "npm"
	// InstallerUV installs a Python tool via uv into toolset-scoped dirs.
	InstallerUV Installer = "uv"
	// InstallerGo runs `go install` with GOBIN pointed at the toolset bin.
	InstallerGo Installer = "go"
	// InstallerCargo installs a Rust crate with `cargo install --root`.
	InstallerCargo Installer = "cargo"
	// InstallerBinary downloads a checksum-pinned executable over https.
	InstallerBinary Installer = "binary"
	// InstallerArchive downloads a checksum-pinned tar.gz/zip and extracts it.
	InstallerArchive Installer = "archive"
)

// Spec is the declarative description of one toolset install. Installer == ""
// means the tool is host-only (nothing here executes) — the shape a `cli_tool`
// access request still uses to ask the user to install something themselves.
type Spec struct {
	// Tool is the executable name the agent will invoke.
	Tool      string
	Installer Installer
	// Package is the npm/uv/cargo package or Go module path. Version rides
	// separately and defaults to latest.
	Package string
	Version string
	// URL + SHA256 pin a binary or archive download (https only).
	URL    string
	SHA256 string
	// Path names the executable inside an archive. Empty means "search the
	// extracted tree for a file called Tool".
	Path string
}

// Dirs is the on-disk layout of the toolset directory.
type Dirs struct {
	Root     string
	Bin      string // binary/archive executables, go installs, cargo bins, uv shims
	NPM      string // npm prefix; executables in NPM/bin
	UV       string // uv tool environments
	Pkg      string // archive extractions, one subdirectory per tool
	Boot     string // bootstrapped installers; deliberately NOT on the agent PATH
	Manifest string
}

// DirsFor derives the layout from the toolset root ($PODIOM_HOME/toolset).
func DirsFor(root string) Dirs {
	return Dirs{
		Root:     root,
		Bin:      filepath.Join(root, "bin"),
		NPM:      filepath.Join(root, "npm"),
		UV:       filepath.Join(root, "uv"),
		Pkg:      filepath.Join(root, "pkg"),
		Boot:     filepath.Join(root, "boot"),
		Manifest: filepath.Join(root, "manifest.json"),
	}
}

// PathDirs lists the directories an agent's subprocess PATH must include so
// toolset tools resolve. Shared by adapter env construction and install
// verification, so the two can never disagree. Boot is excluded on purpose: a
// bootstrapped installer is Podiom's, not the agent's.
func PathDirs(root string) []string {
	d := DirsFor(root)
	return []string{d.Bin, filepath.Join(d.NPM, "bin")}
}

// SpecFromPayload extracts the installer fields from a payload map. Absent
// fields stay zero; call Validate before use.
func SpecFromPayload(payload map[string]string) Spec {
	return Spec{
		Tool:      strings.TrimSpace(payload["tool"]),
		Installer: Installer(strings.TrimSpace(payload["installer"])),
		Package:   strings.TrimSpace(payload["package"]),
		Version:   strings.TrimSpace(payload["version"]),
		URL:       strings.TrimSpace(payload["url"]),
		SHA256:    strings.ToLower(strings.TrimSpace(payload["sha256"])),
		Path:      strings.TrimSpace(payload["path"]),
	}
}

// Installable reports whether the spec asks Podiom to perform the install
// (vs. a host-only tool the user installs themselves).
func (s Spec) Installable() bool { return s.Installer != "" }

var (
	toolNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	// packageRe is defense-in-depth: inputs are passed as single argv
	// elements (never a shell), so this only needs to exclude the absurd.
	packageRe = regexp.MustCompile(`^[A-Za-z0-9@/:._+-]+$`)
	sha256Re  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// reservedTools may never be installed into the toolset. The toolset bin sits
// ahead of the inherited PATH and is shared by every agent — and, in the Home
// Assistant app, by the terminal — so a tool of one of these names would
// silently replace the host's copy for everything Podiom runs.
//
// The provider CLI names come from the registry rather than being listed here,
// so a new provider protects its own binary by existing. The rest are the
// runtimes those CLIs resolve through PATH (node above all: the provider CLIs
// are npm shims that exec it), the installers this package runs, Podiom's own
// binaries, and the shell basics a broken PATH would take down with it.
var reservedTools = func() map[string]bool {
	reserved := map[string]bool{
		"bash": true, "cargo": true, "cp": true, "env": true, "git": true,
		"go": true, "ls": true, "mv": true, "node": true, "npm": true,
		"npx": true, "podiom": true, "podiomd": true, "python": true,
		"python3": true, "rm": true, "sh": true, "ssh": true, "sudo": true,
		"uv": true, "uvx": true,
	}
	for _, p := range config.Providers() {
		reserved[strings.ToLower(string(p.ID))] = true
	}
	return reserved
}()

// Validate enforces the payload rules. A host-only spec (no installer) only
// needs a tool name.
func (s Spec) Validate() error {
	if s.Tool == "" {
		return fmt.Errorf("tool install needs field %q", "tool")
	}
	if !toolNameRe.MatchString(s.Tool) {
		return fmt.Errorf("tool %q must be a bare executable name (letters, numbers, dot, dash, underscore)", s.Tool)
	}
	switch s.Installer {
	case "": // host-only
		return nil
	case InstallerNPM, InstallerUV, InstallerCargo:
		if err := s.checkReserved(); err != nil {
			return err
		}
		if s.Package == "" {
			return fmt.Errorf("%s install needs field %q", s.Installer, "package")
		}
		if !packageRe.MatchString(s.Package) {
			return fmt.Errorf("package %q contains characters outside the allowed set", s.Package)
		}
		if s.Version != "" && !packageRe.MatchString(s.Version) {
			return fmt.Errorf("version %q contains characters outside the allowed set", s.Version)
		}
		return nil
	case InstallerGo:
		if err := s.checkReserved(); err != nil {
			return err
		}
		if s.Package == "" {
			return fmt.Errorf("go install needs field %q (a module path)", "package")
		}
		if !packageRe.MatchString(s.Package) {
			return fmt.Errorf("package %q contains characters outside the allowed set", s.Package)
		}
		return nil
	case InstallerBinary, InstallerArchive:
		if err := s.checkReserved(); err != nil {
			return err
		}
		if !strings.HasPrefix(s.URL, "https://") {
			return fmt.Errorf("%s install needs an https field %q", s.Installer, "url")
		}
		if !sha256Re.MatchString(s.SHA256) {
			return fmt.Errorf("%s install needs field %q: 64 hex characters", s.Installer, "sha256")
		}
		if s.Installer == InstallerArchive && s.Path != "" {
			if err := checkArchivePath(s.Path); err != nil {
				return fmt.Errorf("archive path %q: %w", s.Path, err)
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown installer %q: use npm, uv, go, cargo, binary, or archive", s.Installer)
	}
}

// checkReserved rejects tool names that would shadow the host's own copy for
// every agent (see reservedTools).
func (s Spec) checkReserved() error {
	if reservedTools[strings.ToLower(s.Tool)] {
		return fmt.Errorf("%q is a reserved name: the toolset is shared and sits ahead of the system PATH, so installing it would replace the host's %s for every agent and for Podiom itself", s.Tool, s.Tool)
	}
	return nil
}

// Command returns the argv and extra environment for an install. The same
// function backs display and execution. The download-based installers
// (binary, archive) return a nil argv — theirs is the pure-Go path in Install.
func Command(s Spec, d Dirs) (argv []string, env []string) {
	switch s.Installer {
	case InstallerNPM:
		pkg := s.Package
		if s.Version != "" {
			pkg += "@" + s.Version
		}
		return []string{"npm", "install", "-g", "--prefix", d.NPM, pkg}, nil
	case InstallerUV:
		pkg := s.Package
		if s.Version != "" {
			pkg += "==" + s.Version
		}
		return []string{"uv", "tool", "install", pkg},
			[]string{"UV_TOOL_DIR=" + d.UV, "UV_TOOL_BIN_DIR=" + d.Bin}
	case InstallerGo:
		pkg := s.Package
		if !strings.Contains(pkg, "@") {
			version := s.Version
			if version == "" {
				version = "latest"
			}
			pkg += "@" + version
		}
		return []string{"go", "install", pkg}, []string{"GOBIN=" + d.Bin}
	case InstallerCargo:
		// --root puts executables in <root>/bin, which is already the toolset
		// bin — no extra directory and no shim to keep in sync.
		argv = []string{"cargo", "install", "--root", d.Root}
		if s.Version != "" {
			argv = append(argv, "--version", s.Version)
		}
		return append(argv, s.Package), nil
	default:
		return nil, nil
	}
}

// UninstallCommand returns the argv/env that reverses an install, or nil when
// removal is plain file deletion (go, binary, archive).
func UninstallCommand(e ManifestEntry, d Dirs) (argv []string, env []string) {
	switch Installer(e.Installer) {
	case InstallerNPM:
		return []string{"npm", "uninstall", "-g", "--prefix", d.NPM, npmPackageName(e.Package)}, nil
	case InstallerUV:
		return []string{"uv", "tool", "uninstall", uvPackageName(e.Package)},
			[]string{"UV_TOOL_DIR=" + d.UV, "UV_TOOL_BIN_DIR=" + d.Bin}
	case InstallerCargo:
		return []string{"cargo", "uninstall", "--root", d.Root, e.Package}, nil
	default:
		return nil, nil
	}
}

// npmPackageName strips a version suffix from an npm package spec, keeping
// scoped names (@scope/name) intact.
func npmPackageName(pkg string) string {
	if i := strings.LastIndex(pkg, "@"); i > 0 {
		return pkg[:i]
	}
	return pkg
}

func uvPackageName(pkg string) string {
	name, _, _ := strings.Cut(pkg, "==")
	return name
}
