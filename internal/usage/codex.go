package usage

import (
	"bufio"
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

	"github.com/Podiom/Podiom/internal/config"
)

// codexUsagePath is appended to the resolved base URL. Split out so tests can
// override codexBaseURLDefault while keeping the path.
const (
	codexUsagePath        = "/wham/usage"
	codexBaseURLDefault   = "https://chatgpt.com/backend-api"
	codexStaleRefreshDays = 8
)

// codexAuth is the subset of ~/.codex/auth.json we need. Token fields live only
// on this internal struct; they are never marshaled or logged.
type codexAuth struct {
	AccessToken string
	AccountID   string
	APIKeyOnly  bool
	LastRefresh time.Time
}

type codexAuthFile struct {
	OpenAIAPIKey string `json:"OPENAI_API_KEY"`
	Tokens       struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
	LastRefresh string `json:"last_refresh"`
}

// codexHomeDir resolves the CODEX_HOME for a profile. Empty falls back to
// $CODEX_HOME then ~/.codex.
func codexHomeDir(homeDir string) string {
	dir := homeDir
	if dir == "" {
		dir = os.Getenv("CODEX_HOME")
	}
	if dir == "" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".codex")
	}
	return expandHome(dir)
}

// readCodexAuth reads a profile's Codex credentials read-only. An auth.json that
// carries only OPENAI_API_KEY is flagged APIKeyOnly (no plan windows exist).
func readCodexAuth(homeDir string) (codexAuth, error) {
	path := filepath.Join(codexHomeDir(homeDir), "auth.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return codexAuth{}, err
	}
	var file codexAuthFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return codexAuth{}, fmt.Errorf("parse codex auth: %w", err)
	}
	auth := codexAuth{
		AccessToken: file.Tokens.AccessToken,
		AccountID:   file.Tokens.AccountID,
	}
	if file.LastRefresh != "" {
		if t, perr := time.Parse(time.RFC3339, file.LastRefresh); perr == nil {
			auth.LastRefresh = t
		}
	}
	if auth.AccessToken == "" && file.OpenAIAPIKey != "" {
		auth.APIKeyOnly = true
	}
	return auth, nil
}

// codexBaseURL scans <CODEX_HOME>/config.toml for a chatgpt_base_url override
// without pulling in a toml dependency; it defaults to the ChatGPT backend.
func codexBaseURL(homeDir string) string {
	path := filepath.Join(codexHomeDir(homeDir), "config.toml")
	f, err := os.Open(path)
	if err != nil {
		return codexBaseURLDefault
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "chatgpt_base_url" {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if value != "" {
			return strings.TrimRight(value, "/")
		}
	}
	return codexBaseURLDefault
}

// codexUsageResponse mirrors GET <base>/wham/usage.
type codexUsageResponse struct {
	PlanType  string `json:"plan_type"`
	RateLimit struct {
		PrimaryWindow   *codexWindow `json:"primary_window"`
		SecondaryWindow *codexWindow `json:"secondary_window"`
	} `json:"rate_limit"`
	AdditionalRateLimits []codexNamedWindow `json:"additional_rate_limits"`
	Credits              *struct {
		HasCredits bool      `json:"has_credits"`
		Unlimited  bool      `json:"unlimited"`
		Balance    flexFloat `json:"balance"`
	} `json:"credits"`
}

// flexFloat decodes a JSON number or a numeric string (the Codex usage API
// returns credits.balance as a quoted string like "4.25"). Unparseable values
// decode to 0 rather than failing the whole response.
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	*f = flexFloat(v)
	return nil
}

type codexWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	ResetAt            int64   `json:"reset_at"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
}

type codexNamedWindow struct {
	Name string `json:"name"`
	codexWindow
}

// FetchCodex fetches plan-limit utilization for one Codex profile. It is
// read-only: it never refreshes tokens. Long-stale credentials (last_refresh
// older than ~8 days) degrade to StatusStaleCredentials on 401.
func FetchCodex(ctx context.Context, hc *http.Client, homeDir string) Snapshot {
	snap := Snapshot{Provider: config.ProviderCodex, Source: SourceOAuth, FetchedAt: time.Now()}

	auth, err := readCodexAuth(homeDir)
	if err != nil {
		if os.IsNotExist(err) {
			snap.Status = StatusNoCredentials
			return snap
		}
		snap.Status = StatusError
		snap.Error = "failed to read credentials"
		return snap
	}
	if auth.APIKeyOnly {
		snap.Status = StatusUnsupported
		snap.Error = "API-key account has no plan windows"
		return snap
	}
	if auth.AccessToken == "" {
		snap.Status = StatusNoCredentials
		return snap
	}

	url := codexBaseURL(homeDir) + codexUsagePath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		snap.Status = StatusError
		snap.Error = "failed to build request"
		return snap
	}
	req.Header.Set("Authorization", "Bearer "+auth.AccessToken)
	req.Header.Set("ChatGPT-Account-Id", auth.AccountID)

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
		if !auth.LastRefresh.IsZero() && time.Since(auth.LastRefresh) > codexStaleRefreshDays*24*time.Hour {
			snap.Status = StatusStaleCredentials
			snap.Error = "token stale; a provider run will refresh it"
			return snap
		}
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

	var body codexUsageResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		snap.Status = StatusError
		snap.Error = "failed to decode response"
		return snap
	}

	snap.Plan = body.PlanType
	snap.Windows = codexWindows(body)
	if body.Credits != nil {
		snap.Credits = &Credits{
			Enabled:   body.Credits.HasCredits,
			Unlimited: body.Credits.Unlimited,
			Balance:   float64(body.Credits.Balance),
		}
	}
	snap.Status = StatusOK
	return snap
}

func codexWindows(body codexUsageResponse) []Window {
	var out []Window
	add := func(key, label string, w *codexWindow) {
		if w == nil {
			return
		}
		out = append(out, codexWindowTo(key, label, *w))
	}
	add(WindowPrimary, "5-hour", body.RateLimit.PrimaryWindow)
	add(WindowSecondary, "Weekly", body.RateLimit.SecondaryWindow)
	for _, extra := range body.AdditionalRateLimits {
		label := extra.Name
		if label == "" {
			label = "Limit"
		}
		out = append(out, codexWindowTo(extra.Name, label, extra.codexWindow))
	}
	return out
}

func codexWindowTo(key, label string, w codexWindow) Window {
	win := Window{
		Key:           key,
		Label:         label,
		UsedPercent:   w.UsedPercent,
		WindowSeconds: w.LimitWindowSeconds,
	}
	if w.ResetAt > 0 {
		win.ResetsAt = time.Unix(w.ResetAt, 0)
	}
	return win
}
