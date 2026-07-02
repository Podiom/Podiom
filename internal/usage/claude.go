package usage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Podiom/Podiom/internal/config"
)

// claudeUsageURL is a package var so tests can point it at an httptest server.
var claudeUsageURL = "https://api.anthropic.com/api/oauth/usage"

// claudeUserAgent mimics the Claude Code CLI. The provider gates the OAuth usage
// endpoint on a claude-code User-Agent; we send a stable one.
const (
	claudeUserAgent = "claude-code/1.0.0 (podiom)"
	claudeOAuthBeta = "oauth-2025-04-20"
	claudeScope     = "user:profile"
)

// claudeCredentials is the subset of ~/.claude/.credentials.json we need. Token
// fields live only on this internal struct; they are never marshaled or logged.
type claudeCredentials struct {
	AccessToken      string
	Scopes           []string
	ExpiresAt        int64 // ms epoch
	SubscriptionType string
}

type claudeCredentialsFile struct {
	OAuth struct {
		AccessToken      string   `json:"accessToken"`
		RefreshToken     string   `json:"refreshToken"`
		ExpiresAt        int64    `json:"expiresAt"`
		Scopes           []string `json:"scopes"`
		SubscriptionType string   `json:"subscriptionType"`
	} `json:"claudeAiOauth"`
}

// claudeConfigDir resolves the Claude config directory for a profile. An empty
// configDir falls back to $CLAUDE_CONFIG_DIR then ~/.claude. A leading ~ is
// expanded so profile dirs like "~/.claude-work" resolve correctly.
func claudeConfigDir(configDir string) string {
	dir := configDir
	if dir == "" {
		dir = os.Getenv("CLAUDE_CONFIG_DIR")
	}
	if dir == "" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".claude")
	}
	return expandHome(dir)
}

// claudeCredentialPath resolves the credentials file for a profile.
func claudeCredentialPath(configDir string) string {
	return filepath.Join(claudeConfigDir(configDir), ".credentials.json")
}

// isDefaultClaudeDir reports whether configDir resolves to the CLI's default
// ~/.claude (where macOS stores credentials in the Keychain, not a file).
func isDefaultClaudeDir(configDir string) bool {
	if configDir == "" && os.Getenv("CLAUDE_CONFIG_DIR") == "" {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	return filepath.Clean(claudeConfigDir(configDir)) == filepath.Join(home, ".claude")
}

// readClaudeCredentials reads a profile's Claude OAuth credentials read-only.
// On macOS Claude Code stores tokens in the login Keychain rather than a file —
// for the default account and for every custom CLAUDE_CONFIG_DIR alike — so an
// absent credentials file falls back to the profile's Keychain entry.
// os.IsNotExist errors are surfaced so callers can map them to no_credentials.
func readClaudeCredentials(configDir string) (claudeCredentials, error) {
	path := claudeCredentialPath(configDir)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && runtime.GOOS == "darwin" {
			if creds, kerr := readClaudeKeychainCredentials(configDir); kerr == nil {
				return creds, nil
			}
		}
		return claudeCredentials{}, err
	}
	return parseClaudeCredentials(raw)
}

func parseClaudeCredentials(raw []byte) (claudeCredentials, error) {
	var file claudeCredentialsFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return claudeCredentials{}, fmt.Errorf("parse claude credentials: %w", err)
	}
	return claudeCredentials{
		AccessToken:      file.OAuth.AccessToken,
		Scopes:           file.OAuth.Scopes,
		ExpiresAt:        file.OAuth.ExpiresAt,
		SubscriptionType: file.OAuth.SubscriptionType,
	}, nil
}

// claudeKeychainBase is the macOS Keychain generic-password service under which
// Claude Code stores the default account's OAuth credentials.
const claudeKeychainBase = "Claude Code-credentials"

// claudeKeychainService returns the Keychain service name for a config dir. The
// default account uses the bare base name; a custom CLAUDE_CONFIG_DIR uses
// "<base>-<first 8 hex of sha256(absolute dir)>", matching how the Claude Code
// CLI names its per-profile Keychain entries.
func claudeKeychainService(configDir string) string {
	if isDefaultClaudeDir(configDir) {
		return claudeKeychainBase
	}
	sum := sha256.Sum256([]byte(claudeConfigDir(configDir)))
	return claudeKeychainBase + "-" + hex.EncodeToString(sum[:])[:8]
}

// readClaudeKeychainCredentials reads a Claude account's token from the macOS
// login Keychain via the `security` CLI. The returned blob has the same shape
// as .credentials.json. The token is only ever passed to the parser.
func readClaudeKeychainCredentials(configDir string) (claudeCredentials, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", claudeKeychainService(configDir), "-w").Output()
	if err != nil {
		return claudeCredentials{}, err
	}
	return parseClaudeCredentials(out)
}

// expandHome expands a leading ~ to the user's home directory.
func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
		}
	}
	return path
}

func (c claudeCredentials) hasScope(scope string) bool {
	for _, s := range c.Scopes {
		if strings.EqualFold(strings.TrimSpace(s), scope) {
			return true
		}
	}
	return false
}

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

	creds, err := readClaudeCredentials(configDir)
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
	if creds.ExpiresAt > 0 && time.UnixMilli(creds.ExpiresAt).Before(time.Now()) {
		snap.Status = StatusStaleCredentials
		snap.Error = "token expired; a provider run will refresh it"
		return snap
	}
	if !creds.hasScope(claudeScope) {
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
	req.Header.Set("anthropic-beta", claudeOAuthBeta)
	req.Header.Set("User-Agent", claudeUserAgent)

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
