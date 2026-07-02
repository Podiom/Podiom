// Package autostart configures Podiom's daemon (podiomd) to start on login.
// It mirrors the logic that used to live in scripts/install.sh so the onboarding
// wizard can offer autostart as its final step.
package autostart

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// ErrNoSystemd is returned on Linux when systemd --user isn't available.
var ErrNoSystemd = errors.New("systemd --user is not available")

// ErrUnsupported is returned on platforms without an autostart mechanism.
var ErrUnsupported = errors.New("autostart is not supported on this platform")

// Options configures the autostart install.
type Options struct {
	// PodiomdPath is the absolute path to the podiomd binary. Required.
	PodiomdPath string
	// PodiomHome optionally pins the storage root via PODIOM_HOME. Empty = unset.
	PodiomHome string
}

// Install configures podiomd to launch on login for the current user.
func Install(opts Options) error {
	if opts.PodiomdPath == "" {
		return errors.New("podiomd path is required")
	}
	switch runtime.GOOS {
	case "darwin":
		return installDarwin(opts)
	case "linux":
		return installLinux(opts)
	default:
		return ErrUnsupported
	}
}

func installDarwin(opts Options) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.podiom.podiomd.plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(plistPath, renderPlist(opts, home), 0o644); err != nil {
		return err
	}
	// Reload so changes take effect; ignore unload errors (it may not be loaded).
	_ = exec.Command("launchctl", "unload", plistPath).Run()
	if out, err := exec.Command("launchctl", "load", plistPath).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl load: %w: %s", err, string(out))
	}
	return nil
}

func installLinux(opts Options) error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return ErrNoSystemd
	}
	if err := exec.Command("systemctl", "--user", "show-environment").Run(); err != nil {
		return ErrNoSystemd
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	unitPath := filepath.Join(home, ".config", "systemd", "user", "podiom.service")
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(unitPath, []byte(renderUnit(opts)), 0o644); err != nil {
		return err
	}
	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w: %s", err, string(out))
	}
	if out, err := exec.Command("systemctl", "--user", "enable", "--now", "podiom.service").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl enable: %w: %s", err, string(out))
	}
	return nil
}

// renderPlist builds the launchd plist. Kept side-effect free for testing.
func renderPlist(opts Options, home string) []byte {
	env := ""
	if opts.PodiomHome != "" {
		env = fmt.Sprintf("  <key>EnvironmentVariables</key><dict><key>PODIOM_HOME</key><string>%s</string></dict>\n", opts.PodiomHome)
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>com.podiom.podiomd</string>
  <key>ProgramArguments</key><array><string>%s</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
%s
</dict></plist>
`, opts.PodiomdPath, env)
	return []byte(plist)
}

// renderUnit builds the systemd user unit. Kept side-effect free for testing.
func renderUnit(opts Options) string {
	env := ""
	if opts.PodiomHome != "" {
		env = fmt.Sprintf("Environment=PODIOM_HOME=%s\n", opts.PodiomHome)
	}
	return fmt.Sprintf(`[Unit]
Description=Podiom daemon

[Service]
ExecStart=%s
%sRestart=always

[Install]
WantedBy=default.target
`, opts.PodiomdPath, env)
}
