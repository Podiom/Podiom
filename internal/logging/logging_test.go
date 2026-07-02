package logging

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRotatingWriterRotatesDailyAndCleansRetention(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 1, 10, 0, 0, 0, time.Local)
	old := filepath.Join(dir, "podiomd-2026-06-20.log")
	if err := os.WriteFile(old, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := NewRotatingWriter(dir, 7, func() time.Time { return now })
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	if _, err := w.Write([]byte("day one\n")); err != nil {
		t.Fatalf("write day one: %v", err)
	}
	now = now.AddDate(0, 0, 1)
	if _, err := w.Write([]byte("day two\n")); err != nil {
		t.Fatalf("write day two: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	rotated, err := os.ReadFile(filepath.Join(dir, "podiomd-2026-07-01.log"))
	if err != nil {
		t.Fatalf("read rotated: %v", err)
	}
	if string(rotated) != "day one\n" {
		t.Fatalf("rotated = %q", rotated)
	}
	active, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatalf("read active: %v", err)
	}
	if string(active) != "day two\n" {
		t.Fatalf("active = %q", active)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old rotated log should be removed, stat err=%v", err)
	}
}

func TestTailAndFollowReopensAfterRotation(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir)
	if err := os.WriteFile(path, []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tail, err := Tail(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 1 || tail[0] != "a" {
		t.Fatalf("tail = %#v", tail)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := Follow(ctx, path, 1, 10*time.Millisecond)
	if got := nextEvent(t, events); got.Type != "line" || got.Line != "a" {
		t.Fatalf("first event = %+v", got)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("b\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if got := nextEvent(t, events); got.Type != "line" || got.Line != "b" {
		t.Fatalf("append event = %+v", got)
	}

	if err := os.Rename(path, filepath.Join(dir, "podiomd-2026-07-01.log")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := nextEvent(t, events); got.Type != "reopen" {
		t.Fatalf("reopen event = %+v", got)
	}
	if got := nextEvent(t, events); got.Type != "line" || got.Line != "c" {
		t.Fatalf("new file event = %+v", got)
	}
}

func TestRedactTail(t *testing.T) {
	got := RedactTail(`Authorization: Bearer abc123 token=secret api_key=OPENAIKEY sk-proj-1234567890 sk-ant-1234567890 https://api.github.com/repos/a/b/zipball/main?token=SUPERSECRET`, 500)
	for _, leaked := range []string{"abc123", "secret", "OPENAIKEY", "sk-proj-1234567890", "sk-ant-1234567890", "SUPERSECRET"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("redacted text leaked %q: %s", leaked, got)
		}
	}
}

func nextEvent(t *testing.T, events <-chan FollowEvent) FollowEvent {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for follow event")
		return FollowEvent{}
	}
}
