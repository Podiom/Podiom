// Package tokenmeter converts a session's or goal's cumulative billed-token
// total into an approximate share of the provider's 5-hour and weekly plan
// limits. Providers expose limit utilization only as a percentage (never an
// absolute token ceiling), so the ceiling is *estimated*: the Meter correlates
// Podiom's own token throughput against the provider's reported %-movement over
// time to learn a tokens-per-percent ratio per (profile, window), seeded by a
// coarse default until the first real movement is observed.
//
// It is deliberately separate from internal/usage (which reads credentials and
// never persists tokens): token counts here are transient in-memory accumulators
// used only to calibrate the ratio, never logged or stored.
package tokenmeter

import (
	"sync"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/usage"
)

const (
	// Seed ratios (tokens per 1% of a window) used until calibration observes a
	// real movement. Any guess here is overwritten the first time a window's
	// utilization climbs ≥ minPercentDelta while Podiom has sent tokens; they only
	// keep the pre-calibration estimate in a sane range.
	defaultFiveHourTokensPerPercent = 200_000.0
	defaultWeeklyTokensPerPercent   = 3_000_000.0

	emaAlpha        = 0.3    // weight of the newest observation in the running ratio
	minPercentDelta = 1.0    // ignore sub-1% jitter as calibration noise
	minTokenDelta   = 1000.0 // require meaningful Podiom throughput to attribute
	maxPercent      = 999.0  // clamp: a lifetime total can exceed one window
)

// Estimate is a token total expressed as a share of the 5-hour and weekly limits.
type Estimate struct {
	Tokens          int64   `json:"tokens"`
	FiveHourPercent float64 `json:"five_hour_percent"`
	WeeklyPercent   float64 `json:"weekly_percent"`
	// Calibrated is true once both windows have learned a ratio from real
	// provider movement; false means the figures still rest on the seed defaults.
	Calibrated bool `json:"calibrated"`
}

// windowState is the learned calibration for one (profile, window).
type windowState struct {
	tokensPerPercent float64
	calibrated       bool
	lastPercent      float64
	havePercent      bool
	pendingTokens    float64 // Podiom tokens sent since lastPercent was observed
}

// Meter learns per-window token→percent ratios and produces Estimates. Safe for
// concurrent use.
type Meter struct {
	snapshots func() []usage.Snapshot

	mu      sync.Mutex
	windows map[string]map[string]*windowState // profileKey -> windowKey -> state
}

// New constructs a Meter. snapshots returns the current provider usage snapshots
// (typically the usage.Tracker's Snapshots method).
func New(snapshots func() []usage.Snapshot) *Meter {
	return &Meter{
		snapshots: snapshots,
		windows:   map[string]map[string]*windowState{},
	}
}

// RecordTokens attributes a completed turn's billed tokens to a profile so the
// calibrator can correlate them with the provider's next %-movement.
func (m *Meter) RecordTokens(provider config.Provider, profile string, delta int64) {
	if m == nil || delta <= 0 {
		return
	}
	pk := profileKey(provider, profile)
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, key := range windowKeys(provider) {
		m.stateLocked(pk, key).pendingTokens += float64(delta)
	}
	m.observeLocked(provider, profile, pk)
}

// Estimate converts a cumulative token total into a share of the 5-hour and
// weekly limits for the given profile. A nil Meter or zero tokens yields zeroes.
func (m *Meter) Estimate(provider config.Provider, profile string, tokens int64) Estimate {
	est := Estimate{Tokens: tokens}
	if m == nil {
		return est
	}
	pk := profileKey(provider, profile)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.observeLocked(provider, profile, pk)
	fiveKey, weekKey := windowKeyPair(provider)
	fh := m.stateLocked(pk, fiveKey)
	wk := m.stateLocked(pk, weekKey)
	est.FiveHourPercent = percentFor(fh, tokens, defaultFiveHourTokensPerPercent)
	est.WeeklyPercent = percentFor(wk, tokens, defaultWeeklyTokensPerPercent)
	est.Calibrated = fh.calibrated && wk.calibrated
	return est
}

// observeLocked folds any newly reported provider %-movement for this profile
// into the learned ratios. Caller holds m.mu.
func (m *Meter) observeLocked(provider config.Provider, profile, pk string) {
	snap, ok := m.snapshotFor(provider, profile)
	if !ok || snap.Status != usage.StatusOK {
		return
	}
	for _, key := range windowKeys(provider) {
		w, ok := findWindow(snap, key)
		if !ok {
			continue
		}
		st := m.stateLocked(pk, key)
		if !st.havePercent {
			st.lastPercent = w.UsedPercent
			st.havePercent = true
			continue
		}
		dp := w.UsedPercent - st.lastPercent
		if dp < 0 {
			// Window reset: the accumulated tokens straddle the boundary and can
			// no longer be attributed to a percent delta, so discard them.
			st.lastPercent = w.UsedPercent
			st.pendingTokens = 0
			continue
		}
		if dp < minPercentDelta || st.pendingTokens < minTokenDelta {
			continue
		}
		ratio := st.pendingTokens / dp
		if st.calibrated {
			st.tokensPerPercent = emaAlpha*ratio + (1-emaAlpha)*st.tokensPerPercent
		} else {
			st.tokensPerPercent = ratio
			st.calibrated = true
		}
		st.lastPercent = w.UsedPercent
		st.pendingTokens = 0
	}
}

func percentFor(st *windowState, tokens int64, def float64) float64 {
	tpp := def
	if st.calibrated && st.tokensPerPercent > 0 {
		tpp = st.tokensPerPercent
	}
	if tpp <= 0 || tokens <= 0 {
		return 0
	}
	p := float64(tokens) / tpp
	if p > maxPercent {
		return maxPercent
	}
	return p
}

func (m *Meter) stateLocked(pk, windowKey string) *windowState {
	byWindow, ok := m.windows[pk]
	if !ok {
		byWindow = map[string]*windowState{}
		m.windows[pk] = byWindow
	}
	st, ok := byWindow[windowKey]
	if !ok {
		st = &windowState{}
		byWindow[windowKey] = st
	}
	return st
}

// snapshotFor finds the usage snapshot for a profile, falling back to the
// provider's implicit-default snapshot when the profile has none of its own.
func (m *Meter) snapshotFor(provider config.Provider, profile string) (usage.Snapshot, bool) {
	key := profileKey(provider, profile)
	var fallback usage.Snapshot
	var haveFallback bool
	for _, s := range m.snapshots() {
		if s.Profile == key {
			return s, true
		}
		if s.Default && s.Provider == provider {
			fallback, haveFallback = s, true
		}
	}
	return fallback, haveFallback
}

func findWindow(snap usage.Snapshot, key string) (usage.Window, bool) {
	for _, w := range snap.Windows {
		if w.Key == key {
			return w, true
		}
	}
	return usage.Window{}, false
}

// profileKey normalizes a session's profile to the tracker's snapshot key: named
// profiles key by name, implicit defaults key by the provider string.
func profileKey(provider config.Provider, profile string) string {
	if profile == "" {
		return string(provider)
	}
	return profile
}

// windowKeyPair returns the (5-hour, weekly) window keys for a provider, mirroring
// the mapping used by the usage UI (UsageChip).
func windowKeyPair(provider config.Provider) (string, string) {
	if provider == config.ProviderCodex {
		return usage.WindowPrimary, usage.WindowSecondary
	}
	return usage.WindowFiveHour, usage.WindowSevenDay
}

func windowKeys(provider config.Provider) []string {
	five, weekly := windowKeyPair(provider)
	return []string{five, weekly}
}
