// Package usage measures provider plan-limit utilization (Claude & Codex) per
// auth profile. It reads provider credential files read-only and fetches the
// provider's OAuth usage API; Podiom never writes or logs tokens. Snapshots are
// provider-owned, cheap to re-fetch, and go stale server-side immediately, so
// they live in an in-memory cache (see tracker.go) rather than any store.
package usage

import (
	"time"

	"github.com/Podiom/Podiom/internal/config"
)

// Status is the coarse outcome of a usage fetch for one profile. It drives what
// the UI/CLI renders when there are no windows to show.
type Status string

const (
	// StatusOK means windows were fetched successfully.
	StatusOK Status = "ok"
	// StatusNoCredentials means the profile's credential file is absent.
	StatusNoCredentials Status = "no_credentials"
	// StatusStaleCredentials means the token expired; the next provider run
	// (which Podiom performs constantly) refreshes it. Podiom never refreshes.
	StatusStaleCredentials Status = "stale_credentials"
	// StatusUnauthorized means the provider returned 401 — re-auth needed.
	StatusUnauthorized Status = "unauthorized"
	// StatusRateLimited means the provider returned 429; calls are gated until
	// NextRetryAt.
	StatusRateLimited Status = "rate_limited"
	// StatusUnsupported means the account has no plan windows to report (e.g. a
	// Codex auth.json holding only OPENAI_API_KEY, or a Claude token missing the
	// user:profile scope).
	StatusUnsupported Status = "unsupported"
	// StatusError is any other failure (network, decode, unexpected status).
	StatusError Status = "error"
)

// Source records how a snapshot's windows were obtained.
const (
	SourceOAuth   = "oauth_api"
	SourcePassive = "passive"
)

// Window keys and their approximate window lengths in seconds.
const (
	WindowFiveHour       = "five_hour"
	WindowSevenDay       = "seven_day"
	WindowSevenDayOpus   = "seven_day_opus"
	WindowSevenDaySonnet = "seven_day_sonnet"
	WindowPrimary        = "primary"
	WindowSecondary      = "secondary"
	fiveHourSeconds      = 5 * 60 * 60
	sevenDaySeconds      = 7 * 24 * 60 * 60
)

// Window is one provider rate-limit window's utilization.
type Window struct {
	Key           string    `json:"key"`   // "five_hour","seven_day",..,"primary","secondary", or additional-limit name
	Label         string    `json:"label"` // "5-hour", "Weekly", "Weekly (Opus)", ...
	UsedPercent   float64   `json:"used_percent"`
	ResetsAt      time.Time `json:"resets_at,omitzero"`
	WindowSeconds int64     `json:"window_seconds,omitempty"`
}

// Credits describes extra/on-demand credit balance where a provider exposes it.
type Credits struct {
	Enabled            bool    `json:"enabled"`
	Unlimited          bool    `json:"unlimited,omitempty"`
	Balance            float64 `json:"balance,omitempty"`       // codex
	MonthlyLimit       float64 `json:"monthly_limit,omitempty"` // claude extra_usage
	UsedCredits        float64 `json:"used_credits,omitempty"`
	UtilizationPercent float64 `json:"utilization_percent,omitempty"`
	Currency           string  `json:"currency,omitempty"`
}

// Snapshot is the per-profile usage result surfaced to REST/WS/CLI/UI.
type Snapshot struct {
	Profile     string          `json:"profile"` // "claude"/"codex" for implicit defaults, else profile name
	Provider    config.Provider `json:"provider"`
	Default     bool            `json:"default"`
	Plan        string          `json:"plan,omitempty"` // subscriptionType / plan_type
	Status      Status          `json:"status"`
	Error       string          `json:"error,omitempty"` // sanitized, never contains tokens
	Windows     []Window        `json:"windows,omitempty"`
	Credits     *Credits        `json:"credits,omitempty"`
	FetchedAt   time.Time       `json:"fetched_at,omitzero"`
	NextRetryAt time.Time       `json:"next_retry_at,omitzero"`
	Source      string          `json:"source,omitempty"` // "oauth_api" | "passive"
}

// MaxUsedPercent returns the highest utilization across all windows, or 0.
func (s Snapshot) MaxUsedPercent() float64 {
	var max float64
	for _, w := range s.Windows {
		if w.UsedPercent > max {
			max = w.UsedPercent
		}
	}
	return max
}
