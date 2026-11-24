package schedule

import (
	"testing"
	"time"
)

func TestMissedSlot(t *testing.T) {
	lastRun := time.Date(2026, time.November, 14, 10, 0, 0, 0, time.UTC)
	interval := 24 * time.Hour

	tests := []struct {
		name    string
		lastRun time.Time
		now     time.Time
		want    bool
	}{
		{
			name:    "never run",
			lastRun: time.Time{},
			now:     lastRun.Add(interval),
			want:    false,
		},
		{
			name:    "before due",
			lastRun: lastRun,
			now:     lastRun.Add(interval - time.Nanosecond),
			want:    false,
		},
		{
			name:    "exactly due",
			lastRun: lastRun,
			now:     lastRun.Add(interval),
			want:    true,
		},
		{
			name:    "after due",
			lastRun: lastRun,
			now:     lastRun.Add(interval + time.Nanosecond),
			want:    true,
		},
		{
			name:    "week late needs one catch-up decision",
			lastRun: lastRun,
			now:     lastRun.Add(7 * interval),
			want:    true,
		},
		{
			name:    "last run is in the future",
			lastRun: lastRun,
			now:     lastRun.Add(-time.Hour),
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := missedSlot(tt.lastRun, interval, tt.now)
			if got != tt.want {
				t.Errorf("missedSlot(%v, %v, %v) = %v; want %v",
					tt.lastRun, interval, tt.now, got, tt.want)
			}
		})
	}
}
