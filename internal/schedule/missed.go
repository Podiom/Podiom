package schedule

import "time"

// missedSlot reports whether at least one interval has elapsed since lastRun.
// A zero lastRun means the schedule has never run and is not a missed slot.
func missedSlot(lastRun time.Time, interval time.Duration, now time.Time) bool {
	if lastRun.IsZero() {
		return false
	}

	due := lastRun.Add(interval)
	return !now.Before(due)
}
