package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/usage"
)

func TestFormatUsageTableOK(t *testing.T) {
	var b strings.Builder
	snaps := []usage.Snapshot{{
		Profile:  "claude",
		Provider: config.ProviderClaude,
		Default:  true,
		Plan:     "max",
		Status:   usage.StatusOK,
		Windows: []usage.Window{
			{Key: usage.WindowFiveHour, Label: "5-hour", UsedPercent: 42, ResetsAt: time.Now().Add(2 * time.Hour)},
			{Key: usage.WindowSevenDay, Label: "Weekly", UsedPercent: 63, ResetsAt: time.Now().Add(72 * time.Hour)},
		},
		Credits: &usage.Credits{Enabled: true, MonthlyLimit: 100, UsedCredits: 25, Currency: "USD"},
	}}
	formatUsageTable(&b, snaps)
	out := b.String()
	for _, want := range []string{"claude (default)", "[claude]", "plan=max", "5-hour", "42%", "Weekly", "63%", "resets in", "credits:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestFormatUsageTableError(t *testing.T) {
	var b strings.Builder
	snaps := []usage.Snapshot{{
		Profile:  "codex",
		Provider: config.ProviderCodex,
		Default:  true,
		Status:   usage.StatusNoCredentials,
		Error:    "no credentials found",
	}}
	formatUsageTable(&b, snaps)
	out := b.String()
	if !strings.Contains(out, "status=no_credentials") || !strings.Contains(out, "no credentials found") {
		t.Errorf("unexpected output:\n%s", out)
	}
	if strings.Contains(out, "5-hour") {
		t.Errorf("error snapshot should not render windows:\n%s", out)
	}
}

func TestFormatUsageTableEmpty(t *testing.T) {
	var b strings.Builder
	formatUsageTable(&b, nil)
	if !strings.Contains(b.String(), "no usage data") {
		t.Errorf("unexpected: %q", b.String())
	}
}

func TestFormatUsageTableMixed(t *testing.T) {
	var b strings.Builder
	snaps := []usage.Snapshot{
		{Profile: "claude", Provider: config.ProviderClaude, Default: true, Status: usage.StatusOK,
			Windows: []usage.Window{{Label: "5-hour", UsedPercent: 10}}},
		{Profile: "work", Provider: config.ProviderCodex, Status: usage.StatusStaleCredentials, Error: "token stale"},
	}
	formatUsageTable(&b, snaps)
	out := b.String()
	if !strings.Contains(out, "claude (default)") || !strings.Contains(out, "work  [codex]") {
		t.Errorf("both profiles should render:\n%s", out)
	}
	if !strings.Contains(out, "token stale") {
		t.Errorf("stale error missing:\n%s", out)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{name: "zero", d: 0, want: "0m"},
		{name: "under an hour", d: 45 * time.Minute, want: "45m"},
		{name: "exactly an hour", d: time.Hour, want: "1h 0m"},
		{name: "multi-hour day branch", d: 26 * time.Hour, want: "1d 2h"},
		{name: "rounds across hour", d: 59*time.Minute + 31*time.Second, want: "1h 0m"},
		{name: "just under a day", d: 23*time.Hour + 59*time.Minute, want: "23h 59m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatDuration(tt.d); got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestFormatResets(t *testing.T) {
	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{name: "zero", want: ""},
		{name: "past", at: time.Now().Add(-time.Minute), want: "resets now"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatResets(tt.at); got != tt.want {
				t.Errorf("formatResets(%v) = %q, want %q", tt.at, got, tt.want)
			}
		})
	}

	got := formatResets(time.Now().Add(2 * time.Hour))
	if !strings.HasPrefix(got, "resets in ") || len(got) == len("resets in ") {
		t.Errorf("formatResets(future) = %q, want a non-empty resets-in duration", got)
	}
}

func TestFormatCredits(t *testing.T) {
	tests := []struct {
		name string
		c    usage.Credits
		want string
	}{
		{
			name: "unlimited overrides other values",
			c: usage.Credits{
				Unlimited:    true,
				Balance:      9.876,
				MonthlyLimit: 100,
				UsedCredits:  25,
				Currency:     "USD",
			},
			want: "credits: unlimited",
		},
		{
			name: "monthly limit formats used credits and currency",
			c: usage.Credits{
				MonthlyLimit: 100,
				UsedCredits:  12.005,
				Currency:     "USD",
			},
			want: "credits: 12.01/100.00 USD used",
		},
		{
			name: "zero monthly limit falls back to balance",
			c:    usage.Credits{MonthlyLimit: 0, Balance: 4.2},
			want: "credits: 4.20 balance",
		},
		{
			name: "negative monthly limit falls back to balance",
			c:    usage.Credits{MonthlyLimit: -1, Balance: 4.2},
			want: "credits: 4.20 balance",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatCredits(&tt.c); got != tt.want {
				t.Errorf("formatCredits(%+v) = %q, want %q", tt.c, got, tt.want)
			}
		})
	}
}
