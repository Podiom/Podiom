package creds

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "credentials.yaml"))
}

func TestSetGetRoundtrip(t *testing.T) {
	s := newTestStore(t)
	if err := s.Set(Credential{Name: "GITHUB_TOKEN", Value: "tok_123", Purpose: "gh API", GoalID: "g1"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 credential, got %d", len(list))
	}
	c := list[0]
	if c.Name != "GITHUB_TOKEN" || c.Value != "tok_123" || c.Purpose != "gh API" || c.GoalID != "g1" {
		t.Fatalf("unexpected credential: %+v", c)
	}
	if c.CreatedAt == "" {
		t.Fatal("CreatedAt should be stamped")
	}
}

func TestSetUpsertsByName(t *testing.T) {
	s := newTestStore(t)
	if err := s.Set(Credential{Name: "TOKEN", Value: "old"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Set(Credential{Name: "TOKEN", Value: "new"}); err != nil {
		t.Fatalf("Set update: %v", err)
	}
	list, _ := s.List()
	if len(list) != 1 || list[0].Value != "new" {
		t.Fatalf("upsert failed: %+v", list)
	}
}

func TestSetValidation(t *testing.T) {
	s := newTestStore(t)
	cases := []struct {
		name string
		cred Credential
	}{
		{"empty name", Credential{Name: "", Value: "v"}},
		{"name with equals", Credential{Name: "A=B", Value: "v"}},
		{"name with space", Credential{Name: "A B", Value: "v"}},
		{"empty value", Credential{Name: "TOKEN", Value: ""}},
		{"whitespace value", Credential{Name: "TOKEN", Value: "  \n"}},
		{"reserved PATH", Credential{Name: "PATH", Value: "v"}},
		{"reserved CLAUDE_CONFIG_DIR", Credential{Name: "CLAUDE_CONFIG_DIR", Value: "v"}},
	}
	for _, tc := range cases {
		if err := s.Set(tc.cred); err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
		}
	}
}

func TestSetStripsTrailingNewline(t *testing.T) {
	s := newTestStore(t)
	if err := s.Set(Credential{Name: "TOKEN", Value: "secret\r\n"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	list, _ := s.List()
	if list[0].Value != "secret" {
		t.Fatalf("want stripped value, got %q", list[0].Value)
	}
}

func TestDelete(t *testing.T) {
	s := newTestStore(t)
	if err := s.Set(Credential{Name: "TOKEN", Value: "v"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Delete("TOKEN"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	list, _ := s.List()
	if len(list) != 0 {
		t.Fatalf("want empty store, got %+v", list)
	}
	if err := s.Delete("TOKEN"); err == nil {
		t.Fatal("deleting a missing credential should error")
	}
}

func TestEnvPairs(t *testing.T) {
	s := newTestStore(t)
	if pairs := s.EnvPairs(); len(pairs) != 0 {
		t.Fatalf("missing file should yield no pairs, got %v", pairs)
	}
	_ = s.Set(Credential{Name: "B_TOKEN", Value: "b"})
	_ = s.Set(Credential{Name: "A_TOKEN", Value: "a"})
	pairs := s.EnvPairs()
	if len(pairs) != 2 || pairs[0] != "A_TOKEN=a" || pairs[1] != "B_TOKEN=b" {
		t.Fatalf("unexpected pairs: %v", pairs)
	}
}

func TestFileModeAndContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.yaml")
	s := New(path)
	if err := s.Set(Credential{Name: "TOKEN", Value: "v"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("want mode 0600, got %v", info.Mode().Perm())
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "TOKEN") {
		t.Fatalf("file should contain the credential name: %s", raw)
	}
}
