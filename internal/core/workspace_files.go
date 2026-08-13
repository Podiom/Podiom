package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/Podiom/Podiom/internal/store"
	"github.com/google/uuid"
)

const maxWorkspaceFileSnapshotBytes = 256 << 10

// WorkspaceFileSnapshotResult contains both the durable snapshot and the exact
// Markdown link an agent can paste into any user-visible prose field.
type WorkspaceFileSnapshotResult struct {
	Snapshot     store.WorkspaceFileSnapshot
	MarkdownLink string `json:"markdown_link"`
}

// SnapshotWorkspaceFile validates and copies a text file from the calling
// session's primary work root. The copy is immutable and independent of the
// source file and creator session after this method returns.
func (c *Core) SnapshotWorkspaceFile(ctx context.Context, sessionID, rawPath, rawLabel string) (WorkspaceFileSnapshotResult, error) {
	sess, err := c.GetSession(ctx, strings.TrimSpace(sessionID))
	if err != nil {
		return WorkspaceFileSnapshotResult{}, err
	}
	root, err := c.workspaceFileRoot(sess)
	if err != nil {
		return WorkspaceFileSnapshotResult{}, err
	}
	rel, resolved, err := validateWorkspaceFilePath(root, rawPath)
	if err != nil {
		return WorkspaceFileSnapshotResult{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return WorkspaceFileSnapshotResult{}, fmt.Errorf("inspect workspace file %q: %w", rel, err)
	}
	if !info.Mode().IsRegular() {
		return WorkspaceFileSnapshotResult{}, fmt.Errorf("workspace file %q is not a regular file", rel)
	}
	f, err := os.Open(resolved)
	if err != nil {
		return WorkspaceFileSnapshotResult{}, fmt.Errorf("open workspace file %q: %w", rel, err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxWorkspaceFileSnapshotBytes+1))
	if err != nil {
		return WorkspaceFileSnapshotResult{}, fmt.Errorf("read workspace file %q: %w", rel, err)
	}
	if len(raw) > maxWorkspaceFileSnapshotBytes {
		return WorkspaceFileSnapshotResult{}, fmt.Errorf("workspace file %q exceeds the %d KiB snapshot limit", rel, maxWorkspaceFileSnapshotBytes>>10)
	}
	if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return WorkspaceFileSnapshotResult{}, fmt.Errorf("workspace file %q is not valid UTF-8 text", rel)
	}
	filename := filepath.Base(rel)
	label := strings.TrimSpace(rawLabel)
	if label == "" {
		label = filename
	}
	label = strings.Join(strings.Fields(label), " ")
	if len(label) > 200 {
		return WorkspaceFileSnapshotResult{}, fmt.Errorf("workspace file label exceeds 200 bytes")
	}
	snapshot, err := c.store.CreateWorkspaceFileSnapshot(ctx, store.WorkspaceFileSnapshot{
		ID:               uuid.NewString(),
		CreatorSessionID: sess.ID,
		CreatorAgent:     sess.AgentName,
		ProjectID:        sess.ProjectID,
		SourcePath:       filepath.ToSlash(rel),
		Filename:         filename,
		Label:            label,
		Content:          string(raw),
		SizeBytes:        int64(len(raw)),
	})
	if err != nil {
		return WorkspaceFileSnapshotResult{}, err
	}
	link := fmt.Sprintf("[%s](api/workspace-files/%s)", escapeMarkdownLinkLabel(snapshot.Label), snapshot.ID)
	return WorkspaceFileSnapshotResult{Snapshot: snapshot, MarkdownLink: link}, nil
}

func (c *Core) GetWorkspaceFileSnapshot(ctx context.Context, id string) (store.WorkspaceFileSnapshot, error) {
	return c.store.GetWorkspaceFileSnapshot(ctx, strings.TrimSpace(id))
}

func (c *Core) workspaceFileRoot(sess store.Session) (string, error) {
	if sess.ProjectID == "" {
		return c.AgentPaths(sess.AgentName).Workspace, nil
	}
	proj, err := c.ledger.Get(sess.ProjectID)
	if err != nil {
		return "", err
	}
	return c.projectCodeDir(proj), nil
}

func validateWorkspaceFilePath(root, rawPath string) (string, string, error) {
	path := strings.TrimSpace(rawPath)
	if path == "" {
		return "", "", fmt.Errorf("workspace file path is required")
	}
	if filepath.IsAbs(path) {
		return "", "", fmt.Errorf("workspace file path must be relative to the current work root")
	}
	rel := filepath.Clean(filepath.FromSlash(path))
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("workspace file path must stay inside the current work root")
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve current work root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(resolvedRoot, rel))
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace file %q: %w", filepath.ToSlash(rel), err)
	}
	within, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil || within == "." || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("workspace file path must stay inside the current work root")
	}
	return filepath.ToSlash(rel), resolved, nil
}

func escapeMarkdownLinkLabel(label string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`)
	return replacer.Replace(label)
}
