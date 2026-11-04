package server

import (
	"net/http/httptest"
	"testing"
)

// #367: both query-string parsers have surprises worth pinning. logLines sees the
// raw query, so a "+" decodes to a space and fails the parse; atoiDefault is handed
// an already-decoded string, where "+9" parses and " 7" does not.
func TestLogLines(t *testing.T) {
	cases := []struct {
		name      string
		query     string
		fallback  int
		allowZero bool
		want      int
	}{
		{"valid number", "lines=5", 100, false, 5},
		{"plus sign decodes to a space and fails", "lines=+7", 100, false, 100},
		{"space-encoded value fails", "lines=%205", 100, false, 100},
		{"zero with allowZero", "lines=0", 100, true, 0},
		{"zero without allowZero", "lines=0", 100, false, 100},
		{"negative falls back", "lines=-1", 100, false, 100},
		{"negative falls back with allowZero", "lines=-1", 100, true, 100},
		{"over-large is capped", "lines=99999999", 100, false, maxLogLines},
		{"first repeated parameter wins", "lines=10&lines=20", 100, false, 10},
		{"leading zeros parse", "lines=007", 100, false, 7},
		{"empty is the fallback", "lines=", 100, false, 100},
		{"float is the fallback", "lines=5.0", 100, false, 100},
		{"hex is the fallback", "lines=0x10", 100, false, 100},
		{"past int64 is the fallback", "lines=99999999999999999999", 100, false, 100},
		{"absent is the fallback", "", 100, false, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://x/api/logs?"+tc.query, nil)
			if got := logLines(r, tc.fallback, tc.allowZero); got != tc.want {
				t.Errorf("logLines(%q, fallback=%d, allowZero=%v) = %d, want %d",
					tc.query, tc.fallback, tc.allowZero, got, tc.want)
			}
		})
	}
}

func TestAtoiDefault(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		fallback int
		want     int
	}{
		{"absent is the fallback", "", 50, 50},
		{"empty is the fallback", "", 50, 50},
		{"valid number passes", "5", 50, 5},
		{"negative passes through with no bound", "-5", 50, -5},
		{"space fails (already decoded)", " 7", 50, 50},
		{"plus sign parses", "+9", 50, 9},
		{"float is the fallback", "5.0", 50, 50},
		{"junk is the fallback", "abc", 50, 50},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := atoiDefault(tc.raw, tc.fallback); got != tc.want {
				t.Errorf("atoiDefault(%q, %d) = %d, want %d", tc.raw, tc.fallback, got, tc.want)
			}
		})
	}
}
