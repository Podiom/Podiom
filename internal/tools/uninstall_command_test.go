package tools

import (
	"reflect"
	"testing"
)

// #377: UninstallCommand had 40% coverage — the switch's per-installer
// asymmetries were untested. Every expected output here was checked against
// the real function. The npm/uv package-name helper edge cases are asserted
// through UninstallCommand itself rather than duplicating their own tests.
func TestUninstallCommand(t *testing.T) {
	d := DirsFor("/root")

	t.Run("npm strips a pinned version, cargo keeps it", func(t *testing.T) {
		argv, env := UninstallCommand(ManifestEntry{Installer: "npm", Package: "prettier@3.1.0"}, d)
		want := []string{"npm", "uninstall", "-g", "--prefix", d.NPM, "prettier"}
		if !reflect.DeepEqual(argv, want) {
			t.Errorf("npm argv %v, want %v", argv, want)
		}
		if env != nil {
			t.Errorf("npm env %v: should be nil despite needing --prefix", env)
		}

		argv, _ = UninstallCommand(ManifestEntry{Installer: "cargo", Package: "ripgrep@14.0.0"}, d)
		want = []string{"cargo", "uninstall", "--root", d.Root, "ripgrep@14.0.0"}
		if !reflect.DeepEqual(argv, want) {
			t.Errorf("cargo argv %v, want %v (version still attached)", argv, want)
		}
	})

	t.Run("ManifestEntry.Version is ignored by every branch", func(t *testing.T) {
		argv, _ := UninstallCommand(ManifestEntry{Installer: "npm", Package: "pkg", Version: "9.9.9"}, d)
		if argv[len(argv)-1] != "pkg" {
			t.Errorf("npm: got %v, want plain pkg (contrast Command, which uses the version)", argv)
		}
	})

	t.Run("scoped npm names survive", func(t *testing.T) {
		argv, _ := UninstallCommand(ManifestEntry{Installer: "npm", Package: "@scope/name@1.2.3"}, d)
		if argv[len(argv)-1] != "@scope/name" {
			t.Errorf("scoped pinned: got %v", argv)
		}
		argv, _ = UninstallCommand(ManifestEntry{Installer: "npm", Package: "@scope/name"}, d)
		if argv[len(argv)-1] != "@scope/name" {
			t.Errorf("scoped bare: got %v", argv)
		}
		argv, _ = UninstallCommand(ManifestEntry{Installer: "npm", Package: "a@b@c"}, d)
		if argv[len(argv)-1] != "a@b" {
			t.Errorf("a@b@c: got %v (LastIndex cut)", argv)
		}
	})

	t.Run("uv only understands ==", func(t *testing.T) {
		argv, env := UninstallCommand(ManifestEntry{Installer: "uv", Package: "ruff==1"}, d)
		want := []string{"uv", "tool", "uninstall", "ruff"}
		if !reflect.DeepEqual(argv, want) {
			t.Errorf("uv ==: got %v, want %v", argv, want)
		}
		if !reflect.DeepEqual(env, []string{"UV_TOOL_DIR=" + d.UV, "UV_TOOL_BIN_DIR=" + d.Bin}) {
			t.Errorf("uv env %v: the only branch that returns env", env)
		}
		argv, _ = UninstallCommand(ManifestEntry{Installer: "uv", Package: "ruff>=1"}, d)
		if argv[len(argv)-1] != "ruff>=1" {
			t.Errorf("uv >=: passed through verbatim, got %v", argv)
		}
		argv, _ = UninstallCommand(ManifestEntry{Installer: "uv", Package: "==1"}, d)
		if argv[len(argv)-1] != "" {
			t.Errorf("uv bare ==: yields an empty package name, got %v", argv)
		}
	})

	t.Run("three installers target three different directories", func(t *testing.T) {
		argv, _ := UninstallCommand(ManifestEntry{Installer: "npm", Package: "p"}, d)
		if argv[4] != d.NPM {
			t.Errorf("npm prefix %q, want d.NPM", argv[4])
		}
		argv, env := UninstallCommand(ManifestEntry{Installer: "uv", Package: "p"}, d)
		if env[0] != "UV_TOOL_DIR="+d.UV || env[1] != "UV_TOOL_BIN_DIR="+d.Bin {
			t.Errorf("uv env %v, want d.UV + d.Bin", env)
		}
		argv, _ = UninstallCommand(ManifestEntry{Installer: "cargo", Package: "p"}, d)
		if argv[3] != d.Root {
			t.Errorf("cargo root %q, want d.Root", argv[3])
		}
	})

	t.Run("an unknown or wrong-case installer returns two nil slices, no error", func(t *testing.T) {
		for _, installer := range []string{"NPM", "Npm", " npm", "", "pip"} {
			argv, env := UninstallCommand(ManifestEntry{Installer: installer, Package: "p"}, d)
			if argv != nil || env != nil {
				t.Errorf("installer %q: argv %v env %v, want nil/nil (indistinguishable from the go/binary/archive file-deletion result)", installer, argv, env)
			}
		}
	})
}
