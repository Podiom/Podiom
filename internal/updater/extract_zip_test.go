package updater

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractZipCreatesNestedFiles(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "update.zip")
	writeZip(t, archivePath, map[string]string{
		"bin/podiom": "binary",
		"share/podiom/config/default.yaml": "setting: true\n",
	})
	dest := filepath.Join(root, "extracted")

	if err := extractZip(archivePath, dest); err != nil {
		t.Fatalf("extractZip() error = %v", err)
	}
	for path, want := range map[string]string{
		"bin/podiom": "binary",
		"share/podiom/config/default.yaml": "setting: true\n",
	} {
		got, err := os.ReadFile(filepath.Join(dest, path))
		if err != nil {
			t.Fatalf("read extracted %s: %v", path, err)
		}
		if string(got) != want {
			t.Errorf("content of %s = %q, want %q", path, got, want)
		}
	}
}

func TestExtractZipRejectsUnsafePaths(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "unsafe.zip")
	writeZip(t, archivePath, map[string]string{"../escape.txt": "outside"})

	dest := filepath.Join(root, "extracted")
	err := extractZip(archivePath, dest)
	if err == nil {
		t.Fatal("extractZip() accepted an archive entry that escapes the destination")
	}
	if !strings.Contains(err.Error(), "unsafe archive path") {
		t.Fatalf("extractZip() error = %v, want unsafe archive path error", err)
	}
	if _, err := os.Stat(filepath.Join(root, "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("unsafe entry escaped the destination; stat error = %v", err)
	}
}

func TestExtractZipReturnsInvalidArchiveError(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "not-a-zip.zip")
	if err := os.WriteFile(archivePath, []byte("not a zip file"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := extractZip(archivePath, t.TempDir()); err == nil {
		t.Fatal("extractZip() accepted an invalid zip archive")
	}
}

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			_ = zw.Close()
			_ = f.Close()
			t.Fatalf("create zip entry %q: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			_ = zw.Close()
			_ = f.Close()
			t.Fatalf("write zip entry %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		_ = f.Close()
		t.Fatalf("close zip writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close zip file: %v", err)
	}
}
