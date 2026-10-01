package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

type readTrackingReader struct {
	reads int
}

func (r *readTrackingReader) Read([]byte) (int, error) {
	r.reads++
	return 0, io.EOF
}

func TestConfirmDeleteYesFlagSkipsInput(t *testing.T) {
	reader := &readTrackingReader{}
	var out bytes.Buffer

	if !confirmDelete(reader, &out, "Delete?", true) {
		t.Fatal("--yes should confirm deletion")
	}
	if reader.reads != 0 {
		t.Fatalf("input reads = %d, want 0", reader.reads)
	}
}

func TestConfirmDeleteAcceptsYesResponses(t *testing.T) {
	for _, answer := range []string{"y\n", "yes\n"} {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			var out bytes.Buffer
			if !confirmDelete(strings.NewReader(answer), &out, "Delete?", false) {
				t.Fatalf("answer %q should confirm deletion", answer)
			}
		})
	}
}

func TestConfirmDeleteAbortsEmptyAndNegativeResponses(t *testing.T) {
	for _, answer := range []string{"", "no\n"} {
		t.Run("answer_"+strings.TrimSpace(answer), func(t *testing.T) {
			var out bytes.Buffer
			if confirmDelete(strings.NewReader(answer), &out, "Delete?", false) {
				t.Fatalf("answer %q should abort deletion", answer)
			}
			if !strings.Contains(out.String(), "aborted") {
				t.Fatalf("output %q does not report that deletion was aborted", out.String())
			}
		})
	}
}
