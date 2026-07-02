package main

import (
	"strings"
	"testing"
	"time"

	"github.com/mar-schmidt/Podium/internal/config"
	"github.com/mar-schmidt/Podium/internal/usage"
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
