package marketplace

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ErrUnsafePath is returned when an archive entry would escape the destination
// (SEC-5). It carries no partial write — callers delete the temp dir on any error.
var ErrUnsafePath = errors.New("unsafe archive path")

// ErrTooLarge is returned when the extracted subtree exceeds the configured size
// cap (FR-14).
var ErrTooLarge = errors.New("skill exceeds size cap")

// extractSubtree extracts the files under subPath from a GitHub repo zipball into
// dest, re-rooted so subPath's contents sit directly at dest. GitHub zipballs
// wrap everything in a single top-level "<repo>-<sha>/" directory which is
// stripped first (matching internal/github's archiveRelativePath).
//
// It is the security boundary for install (SEC-4/SEC-5): it rejects "..",
// absolute paths, backslash paths, and ANY symlink (skills never need one, so a
// symlink is always treated as an escape attempt), and it aborts once the running
// total exceeds maxBytes. Files are written 0600, directories 0700 — user-only,
// never executable (SEC-4). On any error the caller removes dest wholesale.
func extractSubtree(r io.ReaderAt, size int64, subPath, dest string, maxBytes int64) error {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	sub := normalizeSubPath(subPath)
	cleanDest := filepath.Clean(dest)
	var total int64
	wrote := false
	for _, f := range zr.File {
		rel, ok, err := archiveRelPath(f.Name)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		// Re-root: keep only entries within the requested subdirectory.
		rooted, ok := reRoot(rel, sub)
		if !ok {
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symlink %q", ErrUnsafePath, f.Name)
		}
		target := filepath.Join(cleanDest, filepath.FromSlash(rooted))
		if target != cleanDest && !strings.HasPrefix(target, cleanDest+string(os.PathSeparator)) {
			return fmt.Errorf("%w: %q", ErrUnsafePath, f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		total += int64(f.UncompressedSize64)
		if maxBytes > 0 && total > maxBytes {
			return ErrTooLarge
		}
		if err := writeZipFile(f, target, maxBytes-total); err != nil {
			return err
		}
		wrote = true
	}
	if !wrote {
		return fmt.Errorf("no files found under %q in archive", subPath)
	}
	return nil
}

func writeZipFile(f *zip.File, target string, remaining int64) error {
	src, err := f.Open()
	if err != nil {
		return err
	}
	dst, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		_ = src.Close()
		return err
	}
	// Guard against zip-bomb lies in the header: cap the actual copy too.
	limit := remaining
	if limit < 0 {
		limit = 0
	}
	_, copyErr := io.Copy(dst, io.LimitReader(src, limit+1))
	closeErr := errors.Join(src.Close(), dst.Close())
	return errors.Join(copyErr, closeErr)
}

// archiveRelPath strips the GitHub zipball's single top-level directory and
// rejects unsafe names, returning the repo-relative path. ok=false means the
// entry is the top-level dir itself (skip it).
func archiveRelPath(name string) (string, bool, error) {
	if filepath.IsAbs(name) || strings.Contains(name, "\\") {
		return "", false, fmt.Errorf("%w: %q", ErrUnsafePath, name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", false, fmt.Errorf("%w: %q", ErrUnsafePath, name)
		}
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false, fmt.Errorf("%w: %q", ErrUnsafePath, name)
	}
	parts := strings.Split(clean, "/")
	if len(parts) <= 1 {
		return "", false, nil // the top-level "<repo>-<sha>/" wrapper
	}
	rel := path.Join(parts[1:]...)
	if rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
		return "", false, fmt.Errorf("%w: %q", ErrUnsafePath, name)
	}
	return rel, true, nil
}

// reRoot keeps rel only if it lives under sub, returning the path with sub's
// prefix removed. sub == "" keeps everything as-is.
func reRoot(rel, sub string) (string, bool) {
	if sub == "" {
		return rel, true
	}
	if rel == sub {
		return "", false // the subdir entry itself
	}
	prefix := sub + "/"
	if !strings.HasPrefix(rel, prefix) {
		return "", false
	}
	return strings.TrimPrefix(rel, prefix), true
}

// normalizeSubPath cleans a skill subpath into forward-slash form with no leading
// or trailing slash. Traversal segments collapse harmlessly; the extractor's
// per-entry guard is the real boundary.
func normalizeSubPath(p string) string {
	p = strings.Trim(strings.TrimSpace(p), "/")
	if p == "" {
		return ""
	}
	clean := path.Clean(p)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return ""
	}
	return clean
}
