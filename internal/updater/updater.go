// Package updater checks GitHub releases and installs verified Podiom updates.
package updater

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultAPIBase = "https://api.github.com/repos/Podiom/Podiom/releases"
	DefaultWebBase = "https://github.com/Podiom/Podiom/releases"
)

// Status describes whether an update is available and installable.
type Status struct {
	CurrentVersion  string `json:"current_version"`
	CurrentCommit   string `json:"current_commit"`
	LatestVersion   string `json:"latest_version"`
	LatestCommitish string `json:"latest_commitish"`
	UpdateAvailable bool   `json:"update_available"`
	AssetName       string `json:"asset_name"`
	AssetURL        string `json:"asset_url"`
	ChecksumURL     string `json:"checksum_url"`
	ReleaseURL      string `json:"release_url"`
	ReleaseNotes    string `json:"release_notes"`
	BlockingReason  string `json:"blocking_reason,omitempty"`
}

// ApplyResult reports what happened after an apply request.
type ApplyResult struct {
	Status          Status `json:"status"`
	Installed       bool   `json:"installed"`
	HelperStarted   bool   `json:"helper_started"`
	RestartRequired bool   `json:"restart_required"`
	Message         string `json:"message"`
}

// Options configures update checks and applies.
type Options struct {
	CurrentVersion string
	CurrentCommit  string
	Version        string // "latest" or a tag.
	InstallDir     string
	Home           string
	Force          bool
	RestartDaemon  bool
	APIBase        string
	HTTPClient     *http.Client
	GOOS           string
	GOARCH         string
	HelperPath     string
}

type release struct {
	TagName         string  `json:"tag_name"`
	TargetCommitish string  `json:"target_commitish"`
	HTMLURL         string  `json:"html_url"`
	Body            string  `json:"body"`
	Assets          []asset `json:"assets"`
}

type asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Check fetches release metadata and compares it against the current build.
func Check(ctx context.Context, opts Options) (Status, error) {
	opts = opts.withDefaults()
	rel, err := fetchRelease(ctx, opts)
	if err != nil {
		return Status{}, err
	}
	goos, goarch := opts.platform()
	archive := ArchiveName(rel.TagName, goos, goarch)
	archiveAsset, ok := findAsset(rel.Assets, archive)
	if !ok {
		archive = LatestArchiveName(goos, goarch)
		archiveAsset, ok = findAsset(rel.Assets, archive)
		if !ok {
			return Status{}, fmt.Errorf("release %s has no asset for %s/%s", rel.TagName, goos, goarch)
		}
	}
	sumAsset, ok := findAsset(rel.Assets, "SHA256SUMS")
	if !ok {
		return Status{}, fmt.Errorf("release %s has no SHA256SUMS asset", rel.TagName)
	}
	status := Status{
		CurrentVersion:  opts.CurrentVersion,
		CurrentCommit:   opts.CurrentCommit,
		LatestVersion:   rel.TagName,
		LatestCommitish: rel.TargetCommitish,
		AssetName:       archive,
		AssetURL:        archiveAsset.BrowserDownloadURL,
		ChecksumURL:     sumAsset.BrowserDownloadURL,
		ReleaseURL:      rel.HTMLURL,
		ReleaseNotes:    rel.Body,
	}
	status.UpdateAvailable = updateAvailable(opts.CurrentVersion, rel.TagName)
	if IsUnreleasedBuild(opts.CurrentVersion) {
		status.BlockingReason = "current build is dev or dirty; use --force to update it"
	}
	return status, nil
}

// Apply downloads, verifies, extracts, and installs an update.
func Apply(ctx context.Context, opts Options) (ApplyResult, error) {
	opts = opts.withDefaults()
	status, err := Check(ctx, opts)
	if err != nil {
		return ApplyResult{}, err
	}
	if status.BlockingReason != "" && !opts.Force {
		return ApplyResult{Status: status, Message: status.BlockingReason}, errors.New(status.BlockingReason)
	}
	if !status.UpdateAvailable && !opts.Force {
		return ApplyResult{Status: status, Message: "already up to date"}, nil
	}
	unlock, err := acquireLock(opts.Home)
	if err != nil {
		return ApplyResult{Status: status}, err
	}
	defer unlock()

	stage, err := os.MkdirTemp("", "podiom-update-*")
	if err != nil {
		return ApplyResult{Status: status}, err
	}
	// Do not remove the stage on Windows helper flow; the helper owns it.
	removeStage := true
	defer func() {
		if removeStage {
			_ = os.RemoveAll(stage)
		}
	}()

	archivePath := filepath.Join(stage, status.AssetName)
	sumPath := filepath.Join(stage, "SHA256SUMS")
	if err := download(ctx, opts.httpClient(), status.AssetURL, archivePath); err != nil {
		return ApplyResult{Status: status}, err
	}
	if err := download(ctx, opts.httpClient(), status.ChecksumURL, sumPath); err != nil {
		return ApplyResult{Status: status}, err
	}
	if err := VerifyChecksum(status.AssetName, archivePath, sumPath); err != nil {
		return ApplyResult{Status: status}, err
	}
	extractDir := filepath.Join(stage, "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return ApplyResult{Status: status}, err
	}
	if err := ExtractArchive(archivePath, extractDir); err != nil {
		return ApplyResult{Status: status}, err
	}
	installDir, err := ResolveInstallDir(opts.InstallDir)
	if err != nil {
		return ApplyResult{Status: status}, err
	}
	if runtime.GOOS == "windows" {
		helper, err := startWindowsHelper(opts, extractDir, installDir)
		if err != nil {
			return ApplyResult{Status: status}, err
		}
		removeStage = false
		return ApplyResult{
			Status:          status,
			HelperStarted:   true,
			RestartRequired: opts.RestartDaemon,
			Message:         "update helper started: " + helper,
		}, nil
	}
	if err := InstallExtracted(extractDir, installDir); err != nil {
		return ApplyResult{Status: status}, err
	}
	return ApplyResult{
		Status:          status,
		Installed:       true,
		RestartRequired: opts.RestartDaemon,
		Message:         "updated to " + status.LatestVersion,
	}, nil
}

// ArchiveName returns the versioned release asset name for a platform.
func ArchiveName(version, goos, goarch string) string {
	if goos == "windows" {
		return fmt.Sprintf("podiom_%s_windows_%s.zip", version, goarch)
	}
	return fmt.Sprintf("podiom_%s_%s_%s.tar.gz", version, goos, goarch)
}

// LatestArchiveName returns the latest-compatible unversioned asset name.
func LatestArchiveName(goos, goarch string) string {
	if goos == "windows" {
		return fmt.Sprintf("podiom_windows_%s.zip", goarch)
	}
	return fmt.Sprintf("podiom_%s_%s.tar.gz", goos, goarch)
}

// IsUnreleasedBuild reports dev and dirty builds that should not update unless forced.
func IsUnreleasedBuild(version string) bool {
	v := strings.TrimSpace(version)
	return v == "" || v == "dev" || strings.Contains(v, "-dirty")
}

func updateAvailable(current, latest string) bool {
	c, cok := releaseNumber(current)
	l, lok := releaseNumber(latest)
	if cok && lok {
		return l > c
	}
	if IsUnreleasedBuild(current) {
		return latest != ""
	}
	return current != latest && latest != ""
}

func releaseNumber(v string) (int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v0.1.")
	n, err := strconv.Atoi(v)
	return n, err == nil
}

func fetchRelease(ctx context.Context, opts Options) (release, error) {
	endpoint := strings.TrimRight(opts.APIBase, "/") + "/latest"
	if opts.Version != "" && opts.Version != "latest" {
		endpoint = strings.TrimRight(opts.APIBase, "/") + "/tags/" + opts.Version
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := opts.httpClient().Do(req)
	if err != nil {
		return release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return release{}, fmt.Errorf("fetch release status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var rel release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return release{}, err
	}
	if rel.TagName == "" {
		return release{}, errors.New("release response missing tag_name")
	}
	return rel, nil
}

func findAsset(assets []asset, name string) (asset, bool) {
	for _, a := range assets {
		if a.Name == name && a.BrowserDownloadURL != "" {
			return a, true
		}
	}
	return asset{}, false
}

func download(ctx context.Context, hc *http.Client, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s status %d", url, resp.StatusCode)
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// VerifyChecksum verifies archivePath against a SHA256SUMS file.
func VerifyChecksum(assetName, archivePath, sumsPath string) error {
	raw, err := os.ReadFile(sumsPath)
	if err != nil {
		return err
	}
	expected := ""
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == assetName {
			expected = strings.ToLower(fields[0])
			break
		}
	}
	if expected == "" {
		return fmt.Errorf("SHA256SUMS has no entry for %s", assetName)
	}
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if actual != expected {
		return fmt.Errorf("checksum mismatch for %s", assetName)
	}
	return nil
}

// ExtractArchive extracts a Podiom release archive into dest.
func ExtractArchive(archivePath, dest string) error {
	if strings.HasSuffix(archivePath, ".zip") {
		return extractZip(archivePath, dest)
	}
	return extractTarGz(archivePath, dest)
}

func extractTarGz(archivePath, dest string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		target, err := safeJoin(dest, h.Name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, tr)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
}

func extractZip(archivePath, dest string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		target, err := safeJoin(dest, f.Name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			_ = in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeInErr := in.Close()
		closeOutErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeInErr != nil {
			return closeInErr
		}
		if closeOutErr != nil {
			return closeOutErr
		}
	}
	return nil
}

func safeJoin(root, name string) (string, error) {
	clean := filepath.Clean(name)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	return filepath.Join(root, clean), nil
}

// ResolveInstallDir returns the directory containing the running binary unless overridden.
func ResolveInstallDir(override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

// InstallExtracted replaces podiom and podiomd from an extracted archive.
func InstallExtracted(extractDir, installDir string) error {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	for _, name := range []string{"podiom", "podiomd"} {
		src := filepath.Join(extractDir, name+ext)
		dst := filepath.Join(installDir, name+ext)
		if err := installBinary(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func installBinary(src, dst string) error {
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("release archive missing %s: %w", filepath.Base(src), err)
	}
	tmp := dst + ".new"
	if err := copyFile(src, tmp, 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		_ = os.Remove(dst)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", dst, err)
	}
	return nil
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func acquireLock(home string) (func(), error) {
	if home == "" {
		home = os.TempDir()
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(home, "update.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil, errors.New("another update is already running")
		}
		return nil, err
	}
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	_ = f.Close()
	return func() { _ = os.Remove(path) }, nil
}

func (o Options) withDefaults() Options {
	if o.Version == "" {
		o.Version = "latest"
	}
	if o.APIBase == "" {
		o.APIBase = DefaultAPIBase
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.GOARCH == "" {
		o.GOARCH = runtime.GOARCH
	}
	return o
}

func (o Options) platform() (string, string) { return o.GOOS, o.GOARCH }

func (o Options) httpClient() *http.Client {
	if o.HTTPClient != nil {
		return o.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func startWindowsHelper(opts Options, extractDir, installDir string) (string, error) {
	helperSrc := opts.HelperPath
	if helperSrc == "" {
		exe, _ := os.Executable()
		if strings.EqualFold(filepath.Base(exe), "podiom.exe") {
			helperSrc = exe
		} else {
			helperSrc = filepath.Join(filepath.Dir(exe), "podiom.exe")
		}
	}
	if _, err := os.Stat(helperSrc); err != nil {
		return "", fmt.Errorf("find update helper %s: %w", helperSrc, err)
	}
	helper := filepath.Join(os.TempDir(), fmt.Sprintf("podiom-update-helper-%d.exe", time.Now().UnixNano()))
	if err := copyFile(helperSrc, helper, 0o755); err != nil {
		return "", err
	}
	args := []string{
		"update", "helper",
		"--stage-dir", extractDir,
		"--install-dir", installDir,
		"--parent-pid", strconv.Itoa(os.Getpid()),
	}
	if opts.RestartDaemon {
		args = append(args, "--restart-daemon")
	}
	cmd := exec.Command(helper, args...)
	if err := cmd.Start(); err != nil {
		return "", err
	}
	return helper, nil
}

// RunHelper performs staged replacement for platforms that lock running binaries.
func RunHelper(stageDir, installDir string, parentPID int, restartDaemon bool) error {
	if parentPID > 0 {
		time.Sleep(2 * time.Second)
	}
	if err := InstallExtracted(stageDir, installDir); err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Dir(stageDir))
	if restartDaemon {
		_, err := StartDaemon(installDir)
		return err
	}
	return nil
}

// StartDaemon starts podiomd from installDir.
func StartDaemon(installDir string) (*exec.Cmd, error) {
	name := "podiomd"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	cmd := exec.Command(filepath.Join(installDir, name))
	cmd.Dir = installDir
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// ScheduleUnixDaemonRestart starts a detached shell that launches podiomd after a delay.
func ScheduleUnixDaemonRestart(installDir string) error {
	path := filepath.Join(installDir, "podiomd")
	cmd := exec.Command("sh", "-c", "sleep 1; exec \"$1\"", "podiom-restart", path)
	return cmd.Start()
}
