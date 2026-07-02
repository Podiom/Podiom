package autostart

import (
	"strings"
	"testing"
)

func TestRenderPlistContainsRequiredKeys(t *testing.T) {
	out := string(renderPlist(Options{PodiomdPath: "/usr/local/bin/podiomd"}, "/Users/test"))
	for _, want := range []string{
		"<key>Label</key><string>com.podiom.podiomd</string>",
		"<string>/usr/local/bin/podiomd</string>",
		"<key>RunAtLoad</key><true/>",
		"<key>KeepAlive</key><true/>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plist missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "StandardOutPath") || strings.Contains(out, "StandardErrorPath") {
		t.Errorf("plist should let podiomd own $PODIOM_HOME/logs:\n%s", out)
	}
	if strings.Contains(out, "EnvironmentVariables") {
		t.Errorf("plist should omit EnvironmentVariables when PodiomHome is empty:\n%s", out)
	}
}

func TestRenderPlistIncludesPodiomHomeWhenSet(t *testing.T) {
	out := string(renderPlist(Options{PodiomdPath: "/bin/podiomd", PodiomHome: "/data/podiom"}, "/home/x"))
	if !strings.Contains(out, "<key>EnvironmentVariables</key><dict><key>PODIOM_HOME</key><string>/data/podiom</string></dict>") {
		t.Errorf("plist missing PODIOM_HOME env dict:\n%s", out)
	}
}

func TestRenderUnitContent(t *testing.T) {
	out := renderUnit(Options{PodiomdPath: "/opt/podiomd"})
	for _, want := range []string{
		"Description=Podiom daemon",
		"ExecStart=/opt/podiomd",
		"Restart=always",
		"WantedBy=default.target",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("unit missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "Environment=PODIOM_HOME") {
		t.Errorf("unit should omit PODIOM_HOME when unset:\n%s", out)
	}
}

func TestRenderUnitIncludesPodiomHomeWhenSet(t *testing.T) {
	out := renderUnit(Options{PodiomdPath: "/opt/podiomd", PodiomHome: "/srv/podiom"})
	if !strings.Contains(out, "Environment=PODIOM_HOME=/srv/podiom") {
		t.Errorf("unit missing PODIOM_HOME line:\n%s", out)
	}
}
