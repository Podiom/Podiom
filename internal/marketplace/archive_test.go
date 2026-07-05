package marketplace

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// zipEntry describes one file to place in a synthetic GitHub-style zipball.
type zipEntry struct {
	name string // path WITHIN the repo (the wrapper dir is added automatically)
	body string
	mode os.FileMode
}

// buildZipball builds a zip mirroring GitHub's layout: a single top-level
// "<repo>-<sha>/" wrapper directory containing every entry.
func buildZipball(t *testing.T, wrapper string, entries []zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: wrapper + "/" + e.name}
		if e.mode != 0 {
			hdr.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func extractInto(t *testing.T, zipBytes []byte, sub string, maxBytes int64) (string, error) {
	t.Helper()
	dest := t.TempDir()
	err := extractSubtree(bytes.NewReader(zipBytes), int64(len(zipBytes)), sub, dest, maxBytes)
	return dest, err
}

func TestExtractSubtree_ReRootsSubdir(t *testing.T) {
	zb := buildZipball(t, "repo-abc123", []zipEntry{
		{name: "README.md", body: "top"},
		{name: "skills/hello/SKILL.md", body: "---\nname: hello\n---\nbody"},
		{name: "skills/hello/scripts/run.sh", body: "echo hi", mode: 0o755},
		{name: "skills/other/SKILL.md", body: "other"},
	})
	dest, err := extractInto(t, zb, "skills/hello", defaultMaxSkillBytes)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "SKILL.md")); err != nil || string(b) != "---\nname: hello\n---\nbody" {
		t.Fatalf("SKILL.md not re-rooted: %v %q", err, b)
	}
	if _, err := os.Stat(filepath.Join(dest, "scripts", "run.sh")); err != nil {
		t.Fatalf("nested script missing: %v", err)
	}
	// The sibling skill must NOT be extracted.
	if _, err := os.Stat(filepath.Join(dest, "other")); !os.IsNotExist(err) {
		t.Fatalf("sibling skill leaked into subtree")
	}
	// Written files must be user-only 0600, never executable (SEC-4).
	info, _ := os.Stat(filepath.Join(dest, "scripts", "run.sh"))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600, got %o", info.Mode().Perm())
	}
}

func TestExtractSubtree_RejectsTraversal(t *testing.T) {
	zb := buildZipball(t, "repo-x", []zipEntry{
		{name: "SKILL.md", body: "ok"},
		{name: "../evil.txt", body: "escape"},
	})
	if _, err := extractInto(t, zb, "", defaultMaxSkillBytes); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("expected ErrUnsafePath, got %v", err)
	}
}

func TestExtractSubtree_RejectsSymlink(t *testing.T) {
	zb := buildZipball(t, "repo-x", []zipEntry{
		{name: "SKILL.md", body: "ok"},
		{name: "link", body: "/etc/passwd", mode: os.ModeSymlink | 0o777},
	})
	if _, err := extractInto(t, zb, "", defaultMaxSkillBytes); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("expected ErrUnsafePath for symlink, got %v", err)
	}
}

func TestExtractSubtree_RejectsOversize(t *testing.T) {
	big := make([]byte, 4096)
	zb := buildZipball(t, "repo-x", []zipEntry{
		{name: "SKILL.md", body: "ok"},
		{name: "blob.bin", body: string(big)},
	})
	if _, err := extractInto(t, zb, "", 1024); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected ErrTooLarge, got %v", err)
	}
}

func TestExtractSubtree_RejectsAbsolutePath(t *testing.T) {
	zb := buildZipball(t, "repo-x", []zipEntry{
		{name: "SKILL.md", body: "ok"},
	})
	// Manually craft an entry with an absolute name that bypasses the wrapper.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("repo-x/SKILL.md")
	_, _ = w.Write([]byte("ok"))
	w2, _ := zw.Create("/etc/cron.d/evil")
	_, _ = w2.Write([]byte("x"))
	_ = zw.Close()
	if _, err := extractInto(t, buf.Bytes(), "", defaultMaxSkillBytes); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("expected ErrUnsafePath for absolute path, got %v", err)
	}
	_ = zb
}

func TestExtractSubtree_MissingSubtreeErrors(t *testing.T) {
	zb := buildZipball(t, "repo-x", []zipEntry{{name: "SKILL.md", body: "ok"}})
	if _, err := extractInto(t, zb, "does/not/exist", defaultMaxSkillBytes); err == nil {
		t.Fatalf("expected error for missing subtree")
	}
}
