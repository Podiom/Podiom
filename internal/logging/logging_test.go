package logging

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
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

func TestDurationMS(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     int64
	}{
		{name: "zero", duration: 0, want: 0},
		{name: "sub-millisecond", duration: 500 * time.Microsecond, want: 0},
		{name: "millisecond", duration: 150 * time.Millisecond, want: 150},
		{name: "whole seconds", duration: 2 * time.Second, want: 2000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "duration_ms"
			attr := DurationMS(key, tt.duration)

			if attr.Key != key {
				t.Errorf("Got key = %q, want %q", attr.Key, key)
			}
			if attr.Value.Kind() != slog.KindInt64 {
				t.Errorf("Kind = %v, want Int64", attr.Value.Kind())
			}
			if got := attr.Value.Int64(); got != tt.want {
				t.Errorf("Int64() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestChangedFields(t *testing.T) {
	tests := []struct {
		name   string
		before map[string]string
		after  map[string]string
		want   []string
	}{
		{name: "Same", before: map[string]string{"a": "a", "b": "b"}, after: map[string]string{"a": "a", "b": "b"}, want: []string{}},
		{name: "Different", before: map[string]string{"a": "a", "b": "b"}, after: map[string]string{"a": "c", "b": "e"}, want: []string{"a", "b"}},
		{name: "Key only in before", before: map[string]string{"a": "a"}, after: map[string]string{}, want: []string{"a"}},
		{name: "Key only in after", before: map[string]string{}, after: map[string]string{"a": "a"}, want: []string{"a"}},
		{name: "Check sorting", before: map[string]string{"b": "b", "a": "a"}, after: map[string]string{"b": "e", "a": "c"}, want: []string{"a", "b"}},
		{name: "Empty maps", before: map[string]string{}, after: map[string]string{}, want: []string{}},
		{name: "Nil maps", before: nil, after: nil, want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ChangedFields(tt.before, tt.after)
			if !slices.Equal(got, tt.want) {
				t.Errorf("Got slice: %v, Want slice: %v", got, tt.want)
			}
		})
	}
}

func TestCountStrings(t *testing.T) {
	tests := []struct {
		name       string
		sliceValue []string
		wantLen    int
	}{
		{name: "Populated slice", sliceValue: []string{"a", "b", "c", "d"}, wantLen: 4},
		{name: "Lenght one", sliceValue: []string{"a"}, wantLen: 1},
		{name: "Empty slice", sliceValue: []string{}, wantLen: 0},
		{name: "Nil slice", sliceValue: nil, wantLen: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLen := Count(tt.sliceValue)

			if gotLen != tt.wantLen {
				t.Errorf("Got length: %d, want length: %d", gotLen, tt.wantLen)
			}
		})
	}
}

func TestCountInts(t *testing.T) {
	tests := []struct {
		name       string
		sliceValue []int
		wantLen    int
	}{
		{name: "Populated slice", sliceValue: []int{0, 1, 2, 3, 4, 5, 6}, wantLen: 7},
		{name: "Lenght one", sliceValue: []int{0}, wantLen: 1},
		{name: "Empty slice", sliceValue: []int{}, wantLen: 0},
		{name: "Nil slice", sliceValue: nil, wantLen: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLen := Count(tt.sliceValue)

			if gotLen != tt.wantLen {
				t.Errorf("Got length: %d, want length: %d", gotLen, tt.wantLen)
			}
		})
	}
}
