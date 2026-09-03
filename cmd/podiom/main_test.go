package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfirmAgentDeletionRequiresExactName(t *testing.T) {
	var out bytes.Buffer
	confirmation, ok := confirmAgentDeletion(strings.NewReader("wrong\n"), &out, "atlas")
	if ok {
		t.Fatal("expected mismatched confirmation to abort")
	}
	if confirmation != "wrong" {
		t.Fatalf("confirmation = %q, want wrong", confirmation)
	}
	if !strings.Contains(out.String(), "agent deletion aborted") {
		t.Fatalf("missing abort message: %q", out.String())
	}

	out.Reset()
	confirmation, ok = confirmAgentDeletion(strings.NewReader("atlas\n"), &out, "atlas")
	if !ok {
		t.Fatal("expected exact confirmation to proceed")
	}
	if confirmation != "atlas" {
		t.Fatalf("confirmation = %q, want atlas", confirmation)
	}
	if strings.Contains(out.String(), "agent deletion aborted") {
		t.Fatalf("unexpected abort message: %q", out.String())
	}
}

func TestConfirmOverwriteDefaultsNo(t *testing.T) {
	var out bytes.Buffer
	if confirmOverwrite(strings.NewReader("\n"), &out, "Overwrite?") {
		t.Fatal("empty answer should abort")
	}
	if !strings.Contains(out.String(), "aborted") {
		t.Fatalf("missing abort message: %q", out.String())
	}

	out.Reset()
	if !confirmOverwrite(strings.NewReader("yes\n"), &out, "Overwrite?") {
		t.Fatal("yes should confirm")
	}
}

func TestAgentsUpdateRejectsYesWithoutGenerateSoul(t *testing.T) {
	addr := "127.0.0.1:1"
	cmd := newAgentsUpdateCmd(&addr)
	cmd.SetArgs([]string{"juno", "--yes", "--model", "sonnet"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected --yes without --generate-soul to fail")
	}
	if !strings.Contains(err.Error(), "--yes only applies with --generate-soul") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAgentsListPrintsEmptyState(t *testing.T) {
	t.Setenv("PODIOM_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents" {
			t.Fatalf("request path = %q, want /api/agents", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "[]")
	}))
	t.Cleanup(srv.Close)

	addr := strings.TrimPrefix(srv.URL, "http://")
	cmd := newAgentsListCmd(&addr)
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "no agents yet\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestActiveLogPathUsesPodiomHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PODIOM_HOME", home)
	got, err := activeLogPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "logs", "podiomd.log")
	if got != want {
		t.Fatalf("activeLogPath() = %q, want %q", got, want)
	}
}
