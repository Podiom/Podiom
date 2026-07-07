// Package tools installs command-line tools into a per-agent tool directory
// on user approval of a cli_tool access request, and manages the manifest
// that records what was installed (see docs/requirements/
// workspace-tool-installs.md).
//
// The load-bearing security property: what the user approves is exactly what
// runs. Install commands are derived mechanically from a declarative,
// validated Spec and executed as argv arrays — never through a shell — by the
// same code that renders them for display.
package tools

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Installer names one supported installation mechanism.
type Installer string

const (
	// InstallerNPM installs an npm package into the agent's npm prefix.
	InstallerNPM Installer = "npm"
	// InstallerUV installs a Python tool via uv into agent-scoped tool dirs.
	InstallerUV Installer = "uv"
	// InstallerGo runs `go install` with GOBIN pointed at the agent's bin.
	InstallerGo Installer = "go"
	// InstallerBinary downloads a checksum-pinned executable over https.
	InstallerBinary Installer = "binary"
)

// Spec is the declarative description of one workspace tool install, parsed
// from a cli_tool access-request payload. Installer == "" means the request
// is host-only (acknowledge-only) and nothing here executes.
type Spec struct {
	// Tool is the executable name the agent will invoke.
	Tool      string
	Installer Installer
	// Package is the npm/uv package or Go module path. Version rides
	// separately and defaults to latest.
	Package string
	Version string
	// URL + SHA256 pin a binary download (https only).
	URL    string
	SHA256 string
}

// Dirs is the on-disk layout of one agent's tool directory.
type Dirs struct {
	Root     string
	Bin      string // binary downloads, go installs, uv shims
	NPM      string // npm prefix; executables land in NPM/bin
	UV       string // uv tool environments
	Manifest string
}

// DirsFor derives the layout from an agent's tools root
// (agents/<name>/tools).
func DirsFor(root string) Dirs {
	return Dirs{
		Root:     root,
		Bin:      filepath.Join(root, "bin"),
		NPM:      filepath.Join(root, "npm"),
		UV:       filepath.Join(root, "uv"),
		Manifest: filepath.Join(root, "manifest.json"),
	}
}

// PathDirs lists the directories an agent's subprocess PATH must include so
// installed tools resolve. Shared by adapter env construction and install
// verification, so the two can never disagree.
func PathDirs(root string) []string {
	d := DirsFor(root)
	return []string{d.Bin, filepath.Join(d.NPM, "bin")}
}

// SpecFromPayload extracts the installer fields from an access-request
// payload map. Absent fields stay zero; call Validate before use.
func SpecFromPayload(payload map[string]string) Spec {
	return Spec{
		Tool:      strings.TrimSpace(payload["tool"]),
		Installer: Installer(strings.TrimSpace(payload["installer"])),
		Package:   strings.TrimSpace(payload["package"]),
		Version:   strings.TrimSpace(payload["version"]),
		URL:       strings.TrimSpace(payload["url"]),
		SHA256:    strings.ToLower(strings.TrimSpace(payload["sha256"])),
	}
}

// Installable reports whether the spec asks Podiom to perform the install
// (vs. a host-only acknowledge-only request).
func (s Spec) Installable() bool { return s.Installer != "" }

var (
	toolNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	// packageRe is defense-in-depth: inputs are passed as single argv
	// elements (never a shell), so this only needs to exclude the absurd.
	packageRe = regexp.MustCompile(`^[A-Za-z0-9@/:._+-]+$`)
	sha256Re  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Validate enforces the §3 payload rules. A host-only spec (no installer)
// only needs a tool name.
func (s Spec) Validate() error {
	if s.Tool == "" {
		return fmt.Errorf("cli_tool request needs payload field %q", "tool")
	}
	if !toolNameRe.MatchString(s.Tool) {
		return fmt.Errorf("tool %q must be a bare executable name (letters, numbers, dot, dash, underscore)", s.Tool)
	}
	switch s.Installer {
	case "": // host-only
		return nil
	case InstallerNPM, InstallerUV:
		if s.Package == "" {
			return fmt.Errorf("%s install needs payload field %q", s.Installer, "package")
		}
		if !packageRe.MatchString(s.Package) {
			return fmt.Errorf("package %q contains characters outside the allowed set", s.Package)
		}
		if s.Version != "" && !packageRe.MatchString(s.Version) {
			return fmt.Errorf("version %q contains characters outside the allowed set", s.Version)
		}
		return nil
	case InstallerGo:
		if s.Package == "" {
			return fmt.Errorf("go install needs payload field %q (a module path)", "package")
		}
		if !packageRe.MatchString(s.Package) {
			return fmt.Errorf("package %q contains characters outside the allowed set", s.Package)
		}
		return nil
	case InstallerBinary:
		if !strings.HasPrefix(s.URL, "https://") {
			return fmt.Errorf("binary install needs an https payload field %q", "url")
		}
		if !sha256Re.MatchString(s.SHA256) {
			return fmt.Errorf("binary install needs payload field %q: 64 hex characters", "sha256")
		}
		return nil
	default:
		return fmt.Errorf("unknown installer %q: use npm, uv, go, or binary", s.Installer)
	}
}

// Command returns the argv and extra environment for an install. The same
// function backs display and execution (§1). InstallerBinary returns a nil
// argv — its install is the pure-Go download path in Install.
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
	default:
		return nil, nil
	}
}

// UninstallCommand returns the argv/env that reverses an install, or nil when
// removal is plain file deletion (go, binary).
func UninstallCommand(e ManifestEntry, d Dirs) (argv []string, env []string) {
	switch Installer(e.Installer) {
	case InstallerNPM:
		return []string{"npm", "uninstall", "-g", "--prefix", d.NPM, npmPackageName(e.Package)}, nil
	case InstallerUV:
		return []string{"uv", "tool", "uninstall", uvPackageName(e.Package)},
			[]string{"UV_TOOL_DIR=" + d.UV, "UV_TOOL_BIN_DIR=" + d.Bin}
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
