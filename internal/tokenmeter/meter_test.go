package tokenmeter

import (
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/usage"
)

// snapFn returns a snapshots function whose value can be swapped between calls to
// simulate the provider's utilization climbing over time.
func snapFn(s *[]usage.Snapshot) func() []usage.Snapshot {
	return func() []usage.Snapshot { return *s }
}

func claudeSnap(profile string, fiveHour, weekly float64) usage.Snapshot {
	return usage.Snapshot{
		Profile:  profile,
		Provider: config.ProviderClaude,
		Default:  profile == string(config.ProviderClaude),
		Status:   usage.StatusOK,
		Windows: []usage.Window{
			{Key: usage.WindowFiveHour, UsedPercent: fiveHour},
			{Key: usage.WindowSevenDay, UsedPercent: weekly},
		},
	}
}

func TestEstimateNilMeterIsZero(t *testing.T) {
	var m *Meter
	est := m.Estimate(config.ProviderClaude, "", 1000)
	if est.Tokens != 1000 || est.FiveHourPercent != 0 || est.WeeklyPercent != 0 || est.Calibrated {
		t.Fatalf("nil meter should yield zero percentages, got %+v", est)
	}
}

func TestEstimateUsesSeedDefaultBeforeCalibration(t *testing.T) {
	snaps := []usage.Snapshot{claudeSnap("claude", 0, 0)}
	m := New(snapFn(&snaps))

	est := m.Estimate(config.ProviderClaude, "", 200_000)
	if est.Calibrated {
		t.Fatalf("should not be calibrated before any movement")
	}
	// 200k tokens against the 200k-per-percent seed = ~1%.
	if got := est.FiveHourPercent; got < 0.99 || got > 1.01 {
		t.Fatalf("seed 5-hour percent = %v, want ~1", got)
	}
}

func TestCalibrationLearnsRatioFromMovement(t *testing.T) {
	snaps := []usage.Snapshot{claudeSnap("claude", 10, 2)}
	m := New(snapFn(&snaps))

	// Observe the baseline percent.
	m.RecordTokens(config.ProviderClaude, "", 1)
	// Send 1,000,000 tokens while the 5-hour window climbs 10% -> 20% (10% delta),
	// implying a ceiling of ~100k tokens per percent.
	m.RecordTokens(config.ProviderClaude, "", 1_000_000)
	snaps[0] = claudeSnap("claude", 20, 4)
	m.RecordTokens(config.ProviderClaude, "", 1) // triggers observation of the new %

	// A 250k lifetime total should now read ~2.5% of the 5-hour limit.
	est := m.Estimate(config.ProviderClaude, "", 250_000)
	if !est.Calibrated {
		t.Fatalf("expected calibration after a >=1%% movement, got %+v", est)
	}
	if est.FiveHourPercent < 2.0 || est.FiveHourPercent > 3.0 {
		t.Fatalf("calibrated 5-hour percent = %v, want ~2.5", est.FiveHourPercent)
	}
}

func TestPercentIsMonotonicInTokens(t *testing.T) {
	snaps := []usage.Snapshot{claudeSnap("claude", 0, 0)}
	m := New(snapFn(&snaps))
	low := m.Estimate(config.ProviderClaude, "", 100_000).FiveHourPercent
	high := m.Estimate(config.ProviderClaude, "", 500_000).FiveHourPercent
	if !(high > low) {
		t.Fatalf("percent should grow with tokens: low=%v high=%v", low, high)
	}
}

func TestPercentClampedToMax(t *testing.T) {
	snaps := []usage.Snapshot{claudeSnap("claude", 0, 0)}
	m := New(snapFn(&snaps))
	est := m.Estimate(config.ProviderClaude, "", 1_000_000_000_000)
	if est.FiveHourPercent != maxPercent {
		t.Fatalf("expected clamp to %v, got %v", maxPercent, est.FiveHourPercent)
	}
}

func TestWindowResetDiscardsStraddlingTokens(t *testing.T) {
	snaps := []usage.Snapshot{claudeSnap("claude", 90, 50)}
	m := New(snapFn(&snaps))
	m.RecordTokens(config.ProviderClaude, "", 1) // baseline at 90%
	m.RecordTokens(config.ProviderClaude, "", 500_000)
	// Window resets: 90% -> 5%. The accumulated tokens straddle the reset and must
	// not calibrate a (negative) ratio.
	snaps[0] = claudeSnap("claude", 5, 50)
	m.RecordTokens(config.ProviderClaude, "", 1)
	est := m.Estimate(config.ProviderClaude, "", 200_000)
	if est.Calibrated {
		t.Fatalf("a window reset must not produce a calibration, got %+v", est)
	}
}
