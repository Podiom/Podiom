package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Podiom/Podiom/internal/claudeauth"
	"github.com/Podiom/Podiom/internal/config"
)

// claudeUsageURL is a package var so tests can point it at an httptest server.
var claudeUsageURL = "https://api.anthropic.com/api/oauth/usage"

// claudeScope is the OAuth scope the usage endpoint requires.
const claudeScope = "user:profile"

// claudeUsageResponse mirrors GET /api/oauth/usage.
type claudeUsageResponse struct {
	FiveHour       *claudeWindow `json:"five_hour"`
	SevenDay       *claudeWindow `json:"seven_day"`
	SevenDayOpus   *claudeWindow `json:"seven_day_opus"`
	SevenDaySonnet *claudeWindow `json:"seven_day_sonnet"`
	ExtraUsage     *struct {
		IsEnabled    bool    `json:"is_enabled"`
		MonthlyLimit float64 `json:"monthly_limit"`
		UsedCredits  float64 `json:"used_credits"`
		Utilization  float64 `json:"utilization"`
		Currency     string  `json:"currency"`
	} `json:"extra_usage"`
}

type claudeWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

// FetchClaude fetches plan-limit utilization for one Claude profile. It never
// writes or refreshes tokens; expired credentials degrade to StatusStaleCredentials.
func FetchClaude(ctx context.Context, hc *http.Client, configDir string) Snapshot {
	snap := Snapshot{Provider: config.ProviderClaude, Source: SourceOAuth, FetchedAt: time.Now()}

	creds, err := claudeauth.ReadCredentials(configDir)
	if err != nil {
		if os.IsNotExist(err) {
			snap.Status = StatusNoCredentials
			return snap
		}
		snap.Status = StatusError
		snap.Error = "failed to read credentials"
		return snap
	}
	if creds.AccessToken == "" {
		snap.Status = StatusNoCredentials
		return snap
	}
	snap.Plan = creds.SubscriptionType
	if creds.Expired() {
		snap.Status = StatusStaleCredentials
		snap.Error = "token expired; a provider run will refresh it"
		return snap
	}
	if !creds.HasScope(claudeScope) {
		snap.Status = StatusUnsupported
		snap.Error = "token missing user:profile scope"
		return snap
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, claudeUsageURL, nil)
	if err != nil {
		snap.Status = StatusError
		snap.Error = "failed to build request"
		return snap
	}
	req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
	req.Header.Set("anthropic-beta", claudeauth.OAuthBeta)
	req.Header.Set("User-Agent", claudeauth.UserAgent)

	resp, err := hc.Do(req)
	if err != nil {
		snap.Status = StatusError
		snap.Error = "request failed"
		return snap
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// handled below
	case http.StatusUnauthorized:
		snap.Status = StatusUnauthorized
		snap.Error = "unauthorized (401); re-auth required"
		return snap
	case http.StatusTooManyRequests:
		snap.Status = StatusRateLimited
		snap.NextRetryAt = retryAfter(resp.Header.Get("Retry-After"), 5*time.Minute)
		snap.Error = "rate limited (429)"
		return snap
	default:
		snap.Status = StatusError
		snap.Error = fmt.Sprintf("unexpected status %d", resp.StatusCode)
		return snap
	}

	var body claudeUsageResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		snap.Status = StatusError
		snap.Error = "failed to decode response"
		return snap
	}

	snap.Windows = claudeWindows(body)
	if body.ExtraUsage != nil && body.ExtraUsage.IsEnabled {
		snap.Credits = &Credits{
			Enabled:            true,
			MonthlyLimit:       body.ExtraUsage.MonthlyLimit,
			UsedCredits:        body.ExtraUsage.UsedCredits,
			UtilizationPercent: body.ExtraUsage.Utilization,
			Currency:           body.ExtraUsage.Currency,
		}
	}
	snap.Status = StatusOK
	return snap
}

func claudeWindows(body claudeUsageResponse) []Window {
	var out []Window
	add := func(key, label string, seconds int64, w *claudeWindow) {
		if w == nil {
			return
		}
		out = append(out, Window{
			Key:           key,
			Label:         label,
			UsedPercent:   w.Utilization,
			ResetsAt:      parseISO(w.ResetsAt),
			WindowSeconds: seconds,
		})
	}
	add(WindowFiveHour, "5-hour", fiveHourSeconds, body.FiveHour)
	add(WindowSevenDay, "Weekly", sevenDaySeconds, body.SevenDay)
	add(WindowSevenDayOpus, "Weekly (Opus)", sevenDaySeconds, body.SevenDayOpus)
	add(WindowSevenDaySonnet, "Weekly (Sonnet)", sevenDaySeconds, body.SevenDaySonnet)
	return out
}

// parseISO parses an ISO8601 timestamp, returning the zero time on failure.
func parseISO(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// retryAfter interprets a Retry-After header (delta-seconds or HTTP-date),
// falling back to now+fallback.
func retryAfter(header string, fallback time.Duration) time.Time {
	header = strings.TrimSpace(header)
	if header != "" {
		if secs, err := strconv.Atoi(header); err == nil {
			return time.Now().Add(time.Duration(secs) * time.Second)
		}
		if t, err := http.ParseTime(header); err == nil {
			return t
		}
	}
	return time.Now().Add(fallback)
}

// expandHome expands a leading ~ to the user's home directory. Shared by codex.go.
func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
		}
	}
	return path
}
