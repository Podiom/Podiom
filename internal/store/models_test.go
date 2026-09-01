package store

import "testing"

func TestSessionUsageAdd(t *testing.T) {
	t.Run("zero delta leaves receiver unchanged", func(t *testing.T) {
		usage := SessionUsage{
			InputTokens:      10,
			OutputTokens:     20,
			CacheReadTokens:  30,
			CacheWriteTokens: 40,
		}

		if got := usage.Add(SessionUsage{}); got != usage {
			t.Fatalf("Add zero delta = %+v, want %+v", got, usage)
		}
	})

	t.Run("zero receiver yields delta", func(t *testing.T) {
		delta := SessionUsage{
			InputTokens:      1,
			OutputTokens:     2,
			CacheReadTokens:  4,
			CacheWriteTokens: 8,
		}

		if got := (SessionUsage{}).Add(delta); got != delta {
			t.Fatalf("zero receiver Add = %+v, want %+v", got, delta)
		}
	})

	t.Run("accumulates each field independently", func(t *testing.T) {
		usage := SessionUsage{
			InputTokens:      10,
			OutputTokens:     20,
			CacheReadTokens:  40,
			CacheWriteTokens: 80,
		}
		delta := SessionUsage{
			InputTokens:      1,
			OutputTokens:     2,
			CacheReadTokens:  4,
			CacheWriteTokens: 8,
		}
		want := SessionUsage{
			InputTokens:      11,
			OutputTokens:     22,
			CacheReadTokens:  44,
			CacheWriteTokens: 88,
		}

		if got := usage.Add(delta); got != want {
			t.Fatalf("Add = %+v, want %+v", got, want)
		}
	})

	t.Run("does not mutate receiver or delta", func(t *testing.T) {
		usage := SessionUsage{
			InputTokens:      10,
			OutputTokens:     20,
			CacheReadTokens:  40,
			CacheWriteTokens: 80,
		}
		delta := SessionUsage{
			InputTokens:      1,
			OutputTokens:     2,
			CacheReadTokens:  4,
			CacheWriteTokens: 8,
		}
		originalUsage := usage
		originalDelta := delta

		_ = usage.Add(delta)

		if usage != originalUsage {
			t.Fatalf("Add mutated receiver: got %+v, want %+v", usage, originalUsage)
		}
		if delta != originalDelta {
			t.Fatalf("Add mutated delta: got %+v, want %+v", delta, originalDelta)
		}
	})

	t.Run("result total equals the sum of input totals", func(t *testing.T) {
		usage := SessionUsage{
			InputTokens:      10,
			OutputTokens:     20,
			CacheReadTokens:  40,
			CacheWriteTokens: 80,
		}
		delta := SessionUsage{
			InputTokens:      1,
			OutputTokens:     2,
			CacheReadTokens:  4,
			CacheWriteTokens: 8,
		}

		if got, want := usage.Add(delta).Total(), usage.Total()+delta.Total(); got != want {
			t.Fatalf("Add total = %d, want %d", got, want)
		}
	})
}
