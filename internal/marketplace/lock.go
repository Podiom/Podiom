package marketplace

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/Podiom/Podiom/internal/skills"
)

// lockFileName is the Podiom-managed record living in the shared skills root
// (SourceAgents from skills.DefaultRoots). Its presence for a given skill name is
// what distinguishes a Podiom-managed install from a hand-placed (Unmanaged)
// directory (FR-15/16).
const lockFileName = ".podiom-lock.json"

// LockEntry records one managed install (FR-15). The pinned SHA (SEC-3) makes an
// install reproducible and change-detectable.
type LockEntry struct {
	Name          string     `json:"name"`
	Registry      RegistryID `json:"registry"`
	Owner         string     `json:"owner"`
	Repo          string     `json:"repo"`
	Path          string     `json:"path"`
	SHA           string     `json:"sha"`
	InstalledAt   string     `json:"installed_at"`
	PodiomVersion string     `json:"podiom_version"`
}

// lockData is the on-disk shape: schema version + entries keyed by skill name.
type lockData struct {
	Version int                  `json:"version"`
	Skills  map[string]LockEntry `json:"skills"`
}

// lockStore reads and writes the lockfile with a process-level mutex so
// concurrent installs don't clobber each other's entries.
type lockStore struct {
	mu   sync.Mutex
	path string
}

// newLockStore locates the lockfile inside the SourceAgents skills root.
func newLockStore() (*lockStore, error) {
	roots, err := skills.DefaultRoots()
	if err != nil {
		return nil, err
	}
	return &lockStore{path: filepath.Join(roots.Agents, lockFileName)}, nil
}

// agentsRoot returns the directory installs land in (~/.agents/skills).
func (l *lockStore) agentsRoot() string { return filepath.Dir(l.path) }

func (l *lockStore) load() (lockData, error) {
	raw, err := os.ReadFile(l.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return lockData{Version: 1, Skills: map[string]LockEntry{}}, nil
		}
		return lockData{}, err
	}
	var data lockData
	if err := json.Unmarshal(raw, &data); err != nil {
		return lockData{}, err
	}
	if data.Skills == nil {
		data.Skills = map[string]LockEntry{}
	}
	if data.Version == 0 {
		data.Version = 1
	}
	return data, nil
}

func (l *lockStore) save(data lockData) error {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, l.path)
}

// Entries returns a snapshot of all managed entries by name.
func (l *lockStore) Entries() (map[string]LockEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	data, err := l.load()
	if err != nil {
		return nil, err
	}
	return data.Skills, nil
}

// Get returns the managed entry for a skill name, if present.
func (l *lockStore) Get(name string) (LockEntry, bool, error) {
	entries, err := l.Entries()
	if err != nil {
		return LockEntry{}, false, err
	}
	e, ok := entries[name]
	return e, ok, nil
}

// Put records (or replaces) a managed entry.
func (l *lockStore) Put(entry LockEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	data, err := l.load()
	if err != nil {
		return err
	}
	data.Skills[entry.Name] = entry
	return l.save(data)
}

// Delete removes a managed entry (used on uninstall). Missing is not an error.
func (l *lockStore) Delete(name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	data, err := l.load()
	if err != nil {
		return err
	}
	delete(data.Skills, name)
	return l.save(data)
}
