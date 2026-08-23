package tools

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// Extraction caps. Generous enough for any real CLI distribution, small enough
// that a decompression bomb fills neither the disk nor an SD card — which is
// what a Home Assistant install usually runs from.
const (
	maxArchiveBytes   = 512 << 20 // 512 MiB of extracted content
	maxArchiveEntries = 20000
)

// checkArchivePath validates a slash-separated path naming a file inside an
// archive. Used both for the caller-supplied Spec.Path and for every entry an
// archive declares, so an archive can never write outside its own directory.
func checkArchivePath(p string) error {
	if p == "" {
		return fmt.Errorf("is empty")
	}
	if strings.Contains(p, `\`) {
		return fmt.Errorf("must use forward slashes")
	}
	if path.IsAbs(p) || strings.HasPrefix(p, "/") {
		return fmt.Errorf("must be relative, not absolute")
	}
	// Windows drive letters ("C:foo") are absolute there but not caught above.
	if len(p) > 1 && p[1] == ':' {
		return fmt.Errorf("must be relative, not absolute")
	}
	clean := path.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("escapes the archive directory")
	}
	return nil
}

// extractArchive unpacks src into dest, which it creates. The format is taken
// from the file's magic bytes rather than the URL, so a mislabelled download
// fails cleanly instead of extracting as something else. On any error the
// partially extracted dest is removed — a surviving directory must always mean
// a complete extraction.
func extractArchive(src, dest string) error {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	magic := make([]byte, 4)
	if _, err := io.ReadFull(f, magic); err != nil {
		return fmt.Errorf("read archive header: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind archive: %w", err)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("create extract dir: %w", err)
	}

	switch {
	case bytes.HasPrefix(magic, []byte{0x1f, 0x8b}):
		err = extractTarGz(f, dest)
	case bytes.HasPrefix(magic, []byte("PK\x03\x04")):
		err = extractZip(f, dest)
	default:
		err = fmt.Errorf("unsupported archive format: expected .tar.gz or .zip (xz and bzip2 are not supported)")
	}
	if err != nil {
		_ = os.RemoveAll(dest)
		return err
	}
	return nil
}

func extractTarGz(f *os.File, dest string) error {
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("read gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	budget := int64(maxArchiveBytes)
	entries := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		entries++
		if entries > maxArchiveEntries {
			return fmt.Errorf("archive has more than %d entries", maxArchiveEntries)
		}
		target, err := resolveEntry(dest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create %s: %w", hdr.Name, err)
			}
		case tar.TypeReg:
			n, err := writeEntry(target, tr, os.FileMode(hdr.Mode).Perm(), budget)
			if err != nil {
				return fmt.Errorf("extract %s: %w", hdr.Name, err)
			}
			budget -= n
		case tar.TypeSymlink:
			if err := writeSymlink(dest, hdr.Name, hdr.Linkname, target); err != nil {
				return err
			}
		default:
			// Devices, fifos, hard links: nothing a CLI tool distribution
			// legitimately needs, and each is its own escape route.
			return fmt.Errorf("archive entry %q has unsupported type %q", hdr.Name, string(hdr.Typeflag))
		}
	}
}

func extractZip(f *os.File, dest string) error {
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat archive: %w", err)
	}
	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		return fmt.Errorf("read zip: %w", err)
	}
	if len(zr.File) > maxArchiveEntries {
		return fmt.Errorf("archive has more than %d entries", maxArchiveEntries)
	}

	budget := int64(maxArchiveBytes)
	for _, entry := range zr.File {
		target, err := resolveEntry(dest, entry.Name)
		if err != nil {
			return err
		}
		mode := entry.Mode()
		switch {
		case entry.FileInfo().IsDir():
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create %s: %w", entry.Name, err)
			}
		case mode&os.ModeSymlink != 0:
			rc, err := entry.Open()
			if err != nil {
				return fmt.Errorf("extract %s: %w", entry.Name, err)
			}
			// A symlink's content is its target; cap the read so a huge
			// "symlink" cannot be used to sidestep the byte budget.
			link, err := io.ReadAll(io.LimitReader(rc, 4096))
			rc.Close()
			if err != nil {
				return fmt.Errorf("extract %s: %w", entry.Name, err)
			}
			if err := writeSymlink(dest, entry.Name, string(link), target); err != nil {
				return err
			}
		case mode.IsRegular():
			rc, err := entry.Open()
			if err != nil {
				return fmt.Errorf("extract %s: %w", entry.Name, err)
			}
			n, err := writeEntry(target, rc, mode.Perm(), budget)
			rc.Close()
			if err != nil {
				return fmt.Errorf("extract %s: %w", entry.Name, err)
			}
			budget -= n
		default:
			return fmt.Errorf("archive entry %q has unsupported type", entry.Name)
		}
	}
	return nil
}

// resolveEntry validates one archive entry name and returns where it lands on
// disk. This is the zip-slip barrier: it runs for every entry of every format.
func resolveEntry(dest, name string) (string, error) {
	if err := checkArchivePath(name); err != nil {
		return "", fmt.Errorf("archive entry %q: %w", name, err)
	}
	return filepath.Join(dest, filepath.FromSlash(path.Clean(name))), nil
}

// writeEntry copies one regular file out of an archive, refusing to exceed the
// remaining byte budget.
func writeEntry(target string, r io.Reader, perm os.FileMode, budget int64) (int64, error) {
	if budget <= 0 {
		return 0, fmt.Errorf("archive exceeds the %d MiB extraction limit", maxArchiveBytes>>20)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, err
	}
	if perm == 0 {
		perm = 0o644
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	// budget+1 so a file that exactly consumes the rest is still detectable as
	// an overrun rather than silently truncated.
	n, err := io.Copy(out, io.LimitReader(r, budget+1))
	if err != nil {
		return n, err
	}
	if n > budget {
		return n, fmt.Errorf("archive exceeds the %d MiB extraction limit", maxArchiveBytes>>20)
	}
	return n, out.Close()
}

// writeSymlink creates an in-archive symlink after proving its target stays
// inside the extraction directory. A link pointing outside would otherwise let
// a later entry — or the tool itself at runtime — write anywhere.
func writeSymlink(dest, name, link, target string) error {
	if link == "" {
		return fmt.Errorf("archive entry %q is a symlink with no target", name)
	}
	if path.IsAbs(link) || strings.HasPrefix(link, "/") {
		return fmt.Errorf("archive entry %q points outside the archive (absolute target %q)", name, link)
	}
	resolved := path.Join(path.Dir(path.Clean(name)), link)
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return fmt.Errorf("archive entry %q points outside the archive (target %q)", name, link)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := os.Symlink(filepath.FromSlash(link), target); err != nil {
		return fmt.Errorf("link %s: %w", name, err)
	}
	return nil
}

// findArchiveExecutable locates the tool inside an extracted archive: at the
// caller-named path when given, otherwise by searching the tree for a regular
// file with that name.
func findArchiveExecutable(dir, tool, named string) (string, error) {
	if named != "" {
		p := filepath.Join(dir, filepath.FromSlash(named))
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("archive has no file at path %q", named)
		}
		return p, nil
	}

	var found string
	err := filepath.WalkDir(dir, func(p string, e os.DirEntry, err error) error {
		if err != nil || found != "" || e.IsDir() || e.Name() != tool {
			return err
		}
		if info, ierr := e.Info(); ierr == nil && info.Mode().IsRegular() {
			found = p
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("search archive: %w", err)
	}
	if found == "" {
		return "", fmt.Errorf("archive contains no file named %q — pass `path` to name the executable inside it", tool)
	}
	return found, nil
}

// linkExecutable exposes an extracted executable as <bin>/<tool>. A symlink
// keeps the binary running from its own directory, so tools that load adjacent
// files still find them; Windows has no dependable unprivileged symlink, so it
// gets a copy.
func linkExecutable(src, dest string) error {
	if err := os.Chmod(src, 0o755); err != nil {
		return fmt.Errorf("chmod %s: %w", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("replace %s: %w", dest, err)
	}
	if runtime.GOOS == "windows" {
		return copyFile(src, dest)
	}
	if err := os.Symlink(src, dest); err != nil {
		return fmt.Errorf("link %s: %w", dest, err)
	}
	return nil
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
