package github

import (
	"archive/zip"
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/projects"
)

func TestExtractZipSnapshotStripsGitHubTopLevel(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("owner-repo-sha/dir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	if err := extractZipSnapshot(bytes.NewReader(buf.Bytes()), int64(buf.Len()), dest); err != nil {
		t.Fatalf("extract: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "dir", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("file content = %q", got)
	}
}

func TestExtractZipSnapshotRejectsUnsafePath(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if _, err := zw.Create("../escape.txt"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractZipSnapshot(bytes.NewReader(buf.Bytes()), int64(buf.Len()), t.TempDir()); err == nil {
		t.Fatal("expected unsafe path error")
	}
}

func TestSyncProjectStoresSourceInRepoDirAndManifestInProjectDir(t *testing.T) {
	archive := githubArchive(t, map[string]string{
		"owner-repo-sha/app/main.go": "package main\n",
	})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widget/commits/main":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"sha":"abc123"}`))
		case "/repos/acme/widget/zipball/main":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	home := t.TempDir()
	var logs bytes.Buffer
	svc := New(Options{
		Config: config.GitHub{APIBase: api.URL, WebBase: "https://github.example", LoginBase: "https://github.example/login"},
		Home:   home,
		Client: api.Client(),
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
	})
	if err := svc.saveToken(tokenFile{AccessToken: "SUPERSECRET", UpdatedAt: time.Now().UTC().Format(time.RFC3339)}); err != nil {
		t.Fatalf("save token: %v", err)
	}

	repo := projects.SnapshotRepo("acme", "widget", "https://github.example/acme/widget", "main", "main")
	res, err := svc.SyncProject(context.Background(), SyncRequest{
		Project: projects.Project{ID: "mission-control", Path: "mission-control"},
		Repo:    repo,
	})
	if err != nil {
		t.Fatalf("sync project: %v", err)
	}

	projectRoot := filepath.Join(home, "projects", "mission-control")
	repoRoot := filepath.Join(projectRoot, "repo")
	if res.Path != repoRoot {
		t.Fatalf("result path = %q, want %q", res.Path, repoRoot)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "app", "main.go")); err != nil {
		t.Fatalf("repo source missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".podiom-source.json")); err != nil {
		t.Fatalf("project manifest missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".podiom-source.json")); !os.IsNotExist(err) {
		t.Fatalf("repo manifest should not exist, stat err = %v", err)
	}
	gotLogs := logs.String()
	for _, want := range []string{
		`event=github`,
		`msg="github project sync requested"`,
		`msg="github archive download started"`,
		`msg="github archive extraction finished"`,
		`msg="github project sync finished"`,
		`repo=acme/widget`,
		`project=mission-control`,
	} {
		if !strings.Contains(gotLogs, want) {
			t.Fatalf("logs missing %q:\n%s", want, gotLogs)
		}
	}
	if strings.Contains(gotLogs, "SUPERSECRET") || strings.Contains(gotLogs, "/zipball/main?") {
		t.Fatalf("github logs leaked token or archive URL:\n%s", gotLogs)
	}
}

func TestMigrateLegacyRootSnapshotBacksUpOldRootContents(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, ".podiom-source.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := migrateLegacyRootSnapshot(projectRoot); err != nil {
		t.Fatalf("migrate legacy snapshot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectRoot, "main.go")); !os.IsNotExist(err) {
		t.Fatalf("legacy source should be moved out of project root, stat err = %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(projectRoot, ".podiom-backups", "*", "legacy-root", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("legacy backup matches = %v, want one backed up main.go", matches)
	}
}

func githubArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
