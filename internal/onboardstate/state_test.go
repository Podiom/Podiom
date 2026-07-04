package onboardstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadMissingReturnsIncompleteState(t *testing.T) {
	st, err := Read(filepath.Join(t.TempDir(), "onboarding.json"))
	if err != nil {
		t.Fatalf("Read missing: %v", err)
	}
	if st.Completed || !st.CompletedAt.IsZero() {
		t.Fatalf("state = %+v, want incomplete", st)
	}
}

func TestMarkCompleteWritesOwnerOnlyState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "onboarding.json")
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	written, err := MarkComplete(path, now)
	if err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}
	if !written.Completed || !written.CompletedAt.Equal(now.UTC()) {
		t.Fatalf("written = %+v", written)
	}
	st, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !st.Completed || !st.CompletedAt.Equal(now.UTC()) {
		t.Fatalf("read = %+v", st)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 0600", got)
	}
}
