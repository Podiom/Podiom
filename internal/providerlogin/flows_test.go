package providerlogin

import "testing"

func TestParseClaudeLineScrapesVisitURL(t *testing.T) {
	s := &Session{}
	parseClaudeLine("If the browser didn't open, visit: https://claude.com/cai/oauth/authorize", s)
	if s.URL != "https://claude.com/cai/oauth/authorize" {
		t.Fatalf("URL = %q, want the manual authorize URL", s.URL)
	}
	if s.Phase != PhaseAwaitingCode {
		t.Fatalf("Phase = %q, want %q", s.Phase, PhaseAwaitingCode)
	}
}

func TestParseClaudeLineKeepsFirstURL(t *testing.T) {
	s := &Session{URL: "https://first.example"}
	parseClaudeLine("visit: https://second.example", s)
	if s.URL != "https://first.example" {
		t.Fatalf("URL = %q, want the first URL to be kept", s.URL)
	}
}

func TestParseClaudeLineInvalidCodeKeepsSessionOpen(t *testing.T) {
	s := &Session{URL: "https://claude.com/authorize"}
	parseClaudeLine("Invalid code. Please make sure the full code was copied.", s)
	if s.Message != "Invalid code. Please make sure the full code was copied." {
		t.Fatalf("Message = %q, want the CLI rejection text", s.Message)
	}
	if s.Phase != PhaseAwaitingCode {
		t.Fatalf("Phase = %q, want %q so the user can retry", s.Phase, PhaseAwaitingCode)
	}
}

func TestParseClaudeLineInvalidCodeWithoutURLLeavesPhase(t *testing.T) {
	s := &Session{}
	before := s.Phase
	parseClaudeLine("Invalid code.", s)
	if s.Message != "Invalid code." {
		t.Fatalf("Message = %q, want the rejection text", s.Message)
	}
	if s.Phase != before {
		t.Fatalf("Phase = %q, want it unchanged when no URL has been scraped yet", s.Phase)
	}
}

func TestParseClaudeLineLoginFailedSetsMessage(t *testing.T) {
	s := &Session{}
	parseClaudeLine("Login failed: browser unreachable", s)
	if s.Message != "browser unreachable" {
		t.Fatalf("Message = %q, want the trimmed failure text", s.Message)
	}
}

func TestParseCodexLineScrapesURL(t *testing.T) {
	s := &Session{}
	parseCodexLine("   https://auth.openai.com/codex/device   ", s)
	if s.URL != "https://auth.openai.com/codex/device" {
		t.Fatalf("URL = %q, want the device URL", s.URL)
	}
	if s.Phase != PhaseAwaitingAuthorization {
		t.Fatalf("Phase = %q, want %q", s.Phase, PhaseAwaitingAuthorization)
	}
}

func TestParseCodexLineScrapesUserCode(t *testing.T) {
	s := &Session{}
	parseCodexLine("BDJL-IOS16", s)
	if s.UserCode != "BDJL-IOS16" {
		t.Fatalf("UserCode = %q, want BDJL-IOS16", s.UserCode)
	}
}

func TestParseCodexLineReportsTimeout(t *testing.T) {
	s := &Session{}
	parseCodexLine("device auth timed out", s)
	if s.Message != "device auth timed out" {
		t.Fatalf("Message = %q, want the timeout text", s.Message)
	}
}

func TestCutPrefix(t *testing.T) {
	tests := []struct {
		name   string
		line   string
		prefix string
		want   string
		wantOK bool
	}{
		{"prefix at start", "Login failed: browser unreachable", "Login failed:", "browser unreachable", true},
		{"prefix embedded", "war: Login failed: nope", "Login failed:", "nope", true},
		{"missing prefix", "all good here", "Login failed:", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := cutPrefix(tt.line, tt.prefix)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("cutPrefix(%q, %q) = (%q, %v), want (%q, %v)", tt.line, tt.prefix, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
