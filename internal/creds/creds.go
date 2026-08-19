// Package creds stores user-supplied credentials (API tokens and similar
// secrets) that agents requested via env_var access requests. Values live in
// credentials.yaml under the storage root (0600, atomic writes — the same
// trust model as mcp.yaml) and are injected into agent CLI subprocess
// environments at spawn time. Values must never leave the daemon: API
// responses and logs carry credential names only.
package creds

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Credential is one stored secret. Name is the environment variable name the
// agent asked for; Purpose and GoalID are display metadata from the access
// request that produced it. CreatedByAgent/CreatedBySession carry provenance
// when an agent stored it itself — empty means the user did, matching the
// attribution rule roadmap tasks and schedules already follow.
type Credential struct {
	Name             string `yaml:"name"`
	Value            string `yaml:"value"`
	Purpose          string `yaml:"purpose,omitempty"`
	GoalID           string `yaml:"goal_id,omitempty"`
	CreatedAt        string `yaml:"created_at,omitempty"`
	CreatedByAgent   string `yaml:"created_by_agent,omitempty"`
	CreatedBySession string `yaml:"created_by_session,omitempty"`
}

// reservedNames are adapter-managed variables that stored credentials must
// never shadow: overriding these would break subprocess spawning itself.
var reservedNames = map[string]bool{
	"PATH":              true,
	"HOME":              true,
	"PODIOM_HOME":       true,
	"CLAUDE_CONFIG_DIR": true,
	"CODEX_HOME":        true,
}

// Store reads and writes the credentials file. Reads re-load the file on each
// call: the file is tiny, and per-call freshness means a credential stored
// mid-flight reaches the next spawned agent subprocess without any daemon
// restart or cache invalidation.
type Store struct {
	path string
	mu   sync.Mutex
}

func New(path string) *Store {
	return &Store{path: path}
}

type yamlFile struct {
	Credentials []Credential `yaml:"credentials"`
}

// Set validates and upserts a credential by name.
func (s *Store) Set(c Credential) error {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return fmt.Errorf("credential name is required")
	}
	if strings.ContainsAny(c.Name, "= \t") {
		return fmt.Errorf("credential name must be a bare variable name like GITHUB_TOKEN")
	}
	if reservedNames[c.Name] {
		return fmt.Errorf("credential name %s is reserved", c.Name)
	}
	// Strip one trailing newline — the usual paste artifact — but preserve
	// any other whitespace the secret may legitimately contain.
	c.Value = strings.TrimSuffix(strings.TrimSuffix(c.Value, "\n"), "\r")
	if strings.TrimSpace(c.Value) == "" {
		return fmt.Errorf("credential value is required")
	}
	if c.CreatedAt == "" {
		c.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return err
	}
	replaced := false
	for i := range list {
		if list[i].Name == c.Name {
			list[i] = c
			replaced = true
			break
		}
	}
	if !replaced {
		list = append(list, c)
	}
	return s.save(list)
}

// Delete removes a credential by name. Deleting a name that does not exist is
// an error so the API surface can report a miss.
func (s *Store) Delete(name string) error {
	name = strings.TrimSpace(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return err
	}
	for i := range list {
		if list[i].Name == name {
			return s.save(append(list[:i], list[i+1:]...))
		}
	}
	return fmt.Errorf("credential %s not found", name)
}

// List returns all stored credentials sorted by name. Callers must not ship
// Value outward — serialize a value-free view instead.
func (s *Store) List() ([]Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// EnvPairs returns the stored credentials as NAME=value pairs for subprocess
// environments. Errors degrade to an empty slice: a missing or unreadable
// file must never block an agent turn.
func (s *Store) EnvPairs() []string {
	list, err := s.List()
	if err != nil {
		return nil
	}
	pairs := make([]string, 0, len(list))
	for _, c := range list {
		pairs = append(pairs, c.Name+"="+c.Value)
	}
	return pairs
}

func (s *Store) load() ([]Credential, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var f yamlFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(s.path), err)
	}
	return f.Credentials, nil
}

func (s *Store) save(list []Credential) error {
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	raw, err := yaml.Marshal(yamlFile{Credentials: list})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(s.path, raw, 0o600)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
