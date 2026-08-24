package tools

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tarEntry is one file, directory, or symlink to write into a test archive.
type tarEntry struct {
	name string
	body string
	mode int64
	link string // non-empty makes this a symlink
	dir  bool
}

func makeTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: e.mode}
		switch {
		case e.dir:
			hdr.Typeflag = tar.TypeDir
		case e.link != "":
			hdr.Typeflag = tar.TypeSymlink
			hdr.Linkname = e.link
		default:
			hdr.Typeflag = tar.TypeReg
			hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header %s: %v", e.name, err)
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("tar body %s: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func makeZip(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		hdr.SetMode(os.FileMode(e.mode))
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("zip header %s: %v", e.name, err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatalf("zip body %s: %v", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// serveArchive stands up a test server for one archive and points the download
// client at it, returning the payload's digest.
func serveArchive(t *testing.T, payload []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)
	restore := SetHTTPClientForTest(&http.Client{Transport: rewriteTransport{base: srv.URL}})
	t.Cleanup(restore)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func TestArchiveInstallTarGz(t *testing.T) {
	root := filepath.Join(t.TempDir(), "toolset")
	payload := makeTarGz(t, []tarEntry{
		{name: "rg-14.1.0/", dir: true, mode: 0o755},
		{name: "rg-14.1.0/README.md", body: "docs", mode: 0o644},
		{name: "rg-14.1.0/rg", body: "#!/bin/sh\necho ripgrep 14.1.0\n", mode: 0o755},
	})
	digest := serveArchive(t, payload)

	spec := Spec{Tool: "rg", Installer: InstallerArchive, URL: "https://example.test/rg.tar.gz", SHA256: digest}
	res, err := Install(context.Background(), spec, root, ManifestEntry{InstalledBy: "atlas"})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if res.Path != filepath.Join(root, "bin", "rg") {
		t.Fatalf("path = %q", res.Path)
	}
	if !strings.Contains(res.VersionOutput, "ripgrep 14.1.0") {
		t.Fatalf("version = %q", res.VersionOutput)
	}
	// Adjacent files come along, which is the point of extracting into pkg/
	// rather than lifting the one executable out.
	if _, err := os.Stat(filepath.Join(root, "pkg", "rg", "rg-14.1.0", "README.md")); err != nil {
		t.Fatalf("adjacent file missing: %v", err)
	}

	// Uninstall takes the extraction directory with it.
	if err := Uninstall(context.Background(), root, "rg"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "pkg", "rg")); !os.IsNotExist(err) {
		t.Fatalf("extraction dir survived uninstall: %v", err)
	}
}

func TestArchiveInstallZipWithNamedPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "toolset")
	// Two candidates: only the named path is correct, and it is not the one a
	// name search would find first.
	payload := makeZip(t, []tarEntry{
		{name: "extras/tool", body: "#!/bin/sh\necho wrong\n", mode: 0o755},
		{name: "bin/tool", body: "#!/bin/sh\necho right 2.0\n", mode: 0o755},
	})
	digest := serveArchive(t, payload)

	spec := Spec{
		Tool: "tool", Installer: InstallerArchive,
		URL: "https://example.test/tool.zip", SHA256: digest, Path: "bin/tool",
	}
	res, err := Install(context.Background(), spec, root, ManifestEntry{})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !strings.Contains(res.VersionOutput, "right 2.0") {
		t.Fatalf("version = %q — wrong candidate linked", res.VersionOutput)
	}
	// The spec's path is kept so a reinstall picks the same file again.
	list, _ := List(root)
	if len(list) != 1 || list[0].Path != "bin/tool" {
		t.Fatalf("manifest = %+v", list)
	}
}

func TestArchiveMissingExecutableIsAnError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "toolset")
	payload := makeTarGz(t, []tarEntry{{name: "pkg/other", body: "x", mode: 0o755}})
	digest := serveArchive(t, payload)

	spec := Spec{Tool: "rg", Installer: InstallerArchive, URL: "https://example.test/rg.tar.gz", SHA256: digest}
	_, err := Install(context.Background(), spec, root, ManifestEntry{})
	if err == nil || !strings.Contains(err.Error(), "no file named") {
		t.Fatalf("err = %v, want a missing-executable error", err)
	}
	if _, err := os.Stat(filepath.Join(root, "pkg", "rg")); !os.IsNotExist(err) {
		t.Fatalf("failed install must leave no extraction dir behind")
	}
	if list, _ := List(root); len(list) != 0 {
		t.Fatalf("failed install must record nothing: %+v", list)
	}
}

// TestArchiveExtractionRejectsEscapes is the zip-slip guard: no archive entry
// may write outside its own extraction directory, whatever the format.
func TestArchiveExtractionRejectsEscapes(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.txt")

	cases := []struct {
		name    string
		payload []byte
	}{
		{"tar parent traversal", makeTarGz(t, []tarEntry{{name: "../outside.txt", body: "pwned", mode: 0o644}})},
		{"tar absolute path", makeTarGz(t, []tarEntry{{name: "/tmp/outside.txt", body: "pwned", mode: 0o644}})},
		{"tar escaping symlink", makeTarGz(t, []tarEntry{{name: "link", link: "../../outside.txt", mode: 0o777}})},
		{"tar absolute symlink", makeTarGz(t, []tarEntry{{name: "link", link: "/etc/passwd", mode: 0o777}})},
		{"zip parent traversal", makeZip(t, []tarEntry{{name: "../outside.txt", body: "pwned", mode: 0o644}})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "extract")
			src := filepath.Join(t.TempDir(), "archive")
			if err := os.WriteFile(src, tc.payload, 0o644); err != nil {
				t.Fatalf("write archive: %v", err)
			}
			if err := extractArchive(src, dir); err == nil {
				t.Fatal("escaping entry was accepted")
			}
			if _, err := os.Stat(outside); err == nil {
				t.Fatal("extraction wrote outside its directory")
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("a failed extraction must not leave its directory behind")
			}
		})
	}
}

func TestArchiveRejectsUnsupportedFormat(t *testing.T) {
	src := filepath.Join(t.TempDir(), "archive")
	// xz magic: a real format Podiom deliberately does not decode.
	if err := os.WriteFile(src, []byte("\xfd7zXZ\x00\x00\x00\x00"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	err := extractArchive(src, filepath.Join(t.TempDir(), "extract"))
	if err == nil || !strings.Contains(err.Error(), "unsupported archive format") {
		t.Fatalf("err = %v", err)
	}
}

func TestArchiveEntryCap(t *testing.T) {
	entries := make([]tarEntry, maxArchiveEntries+1)
	for i := range entries {
		entries[i] = tarEntry{name: "f" + strings.Repeat("0", 4) + string(rune('a'+i%26)) + itoa(i), body: "x", mode: 0o644}
	}
	src := filepath.Join(t.TempDir(), "archive")
	if err := os.WriteFile(src, makeTarGz(t, entries), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	err := extractArchive(src, filepath.Join(t.TempDir(), "extract"))
	if err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("err = %v, want an entry-count refusal", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
