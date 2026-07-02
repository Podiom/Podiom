package updater

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveNames(t *testing.T) {
	if got := ArchiveName("v0.1.42", "darwin", "arm64"); got != "podiom_v0.1.42_darwin_arm64.tar.gz" {
		t.Fatalf("ArchiveName darwin = %q", got)
	}
	if got := ArchiveName("v0.1.42", "windows", "amd64"); got != "podiom_v0.1.42_windows_amd64.zip" {
		t.Fatalf("ArchiveName windows = %q", got)
	}
	if got := LatestArchiveName("linux", "arm64"); got != "podiom_linux_arm64.tar.gz" {
		t.Fatalf("LatestArchiveName = %q", got)
	}
}

func TestUnreleasedBuilds(t *testing.T) {
	for _, v := range []string{"", "dev", "v0.1.7-dirty", "4fac018-dirty"} {
		if !IsUnreleasedBuild(v) {
			t.Fatalf("%q should be unreleased", v)
		}
	}
	if IsUnreleasedBuild("v0.1.7") {
		t.Fatal("release tag should not be unreleased")
	}
}

func TestCheckSelectsVersionedAssetAndBlocksDirty(t *testing.T) {
	srv := fakeReleaseServer(t, "v0.1.9", map[string][]byte{
		"podiom_v0.1.9_windows_arm64.zip": []byte("zip-ish"),
	})
	status, err := Check(context.Background(), Options{
		CurrentVersion: "v0.1.8-dirty",
		CurrentCommit:  "abc123",
		APIBase:        srv.URL,
		GOOS:           "windows",
		GOARCH:         "arm64",
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.AssetName != "podiom_v0.1.9_windows_arm64.zip" {
		t.Fatalf("asset = %q", status.AssetName)
	}
	if !status.UpdateAvailable {
		t.Fatal("expected update to be available")
	}
	if status.BlockingReason == "" {
		t.Fatal("dirty build should have blocking reason")
	}
	if !strings.Contains(status.ReleaseNotes, "- Add release notes (`abc1234`)") {
		t.Fatalf("release notes = %q", status.ReleaseNotes)
	}
}

func TestVerifyChecksum(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "podiom_linux_amd64.tar.gz")
	data := []byte("archive")
	if err := os.WriteFile(archive, data, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	sums := filepath.Join(dir, "SHA256SUMS")
	if err := os.WriteFile(sums, []byte(fmt.Sprintf("%x  podiom_linux_amd64.tar.gz\n", sum)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyChecksum("podiom_linux_amd64.tar.gz", archive, sums); err != nil {
		t.Fatal(err)
	}
	if err := VerifyChecksum("missing.tar.gz", archive, sums); err == nil {
		t.Fatal("missing checksum entry should fail")
	}
}

func TestApplyFromFakeReleaseServer(t *testing.T) {
	archive := makeTarGz(t, map[string]string{
		"podiom":  "new podiom",
		"podiomd": "new podiomd",
	})
	srv := fakeReleaseServer(t, "v0.1.2", map[string][]byte{
		"podiom_v0.1.2_darwin_amd64.tar.gz": archive,
	})
	installDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(installDir, "podiom"), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "podiomd"), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := Apply(context.Background(), Options{
		CurrentVersion: "v0.1.1",
		CurrentCommit:  "abc123",
		APIBase:        srv.URL,
		GOOS:           "darwin",
		GOARCH:         "amd64",
		InstallDir:     installDir,
		Home:           t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Installed {
		t.Fatalf("result = %+v, want installed", result)
	}
	got, err := os.ReadFile(filepath.Join(installDir, "podiom"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new podiom" {
		t.Fatalf("podiom content = %q", got)
	}
}

func fakeReleaseServer(t *testing.T, tag string, files map[string][]byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/latest") || strings.Contains(r.URL.Path, "/tags/"):
			var assets []string
			for name := range files {
				assets = append(assets, fmt.Sprintf(`{"name":%q,"browser_download_url":"%s/assets/%s"}`, name, externalURL(r), name))
			}
			assets = append(assets, fmt.Sprintf(`{"name":"SHA256SUMS","browser_download_url":"%s/assets/SHA256SUMS"}`, externalURL(r)))
			body := fmt.Sprintf("# Podiom %s\n\n- Add release notes (`abc1234`)", tag)
			fmt.Fprintf(w, `{"tag_name":%q,"target_commitish":"abc123","html_url":"%s/releases/%s","body":%q,"assets":[%s]}`, tag, externalURL(r), tag, body, strings.Join(assets, ","))
		case strings.HasPrefix(r.URL.Path, "/assets/"):
			name := strings.TrimPrefix(r.URL.Path, "/assets/")
			if name == "SHA256SUMS" {
				for file, data := range files {
					sum := sha256.Sum256(data)
					fmt.Fprintf(w, "%x  %s\n", sum, file)
				}
				return
			}
			data, ok := files[name]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func externalURL(r *http.Request) string {
	return "http://" + r.Host
}

func makeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b strings.Builder
	gz := gzip.NewWriter(&builderWriter{b: &b})
	tw := tar.NewWriter(gz)
	for name, content := range files {
		body := []byte(content)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return []byte(b.String())
}

type builderWriter struct{ b *strings.Builder }

func (w *builderWriter) Write(p []byte) (int, error) { return w.b.Write(p) }
