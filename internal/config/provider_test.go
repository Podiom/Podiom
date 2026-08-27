package config

import "testing"

func TestProviderRegistryInvariants(t *testing.T) {
	if len(providerInfos) == 0 {
		t.Fatal("provider registry is empty")
	}
	if providerInfos[0].ID != ProviderClaude {
		t.Fatalf("registry order changed: first provider = %q, want %q (order is API-visible)", providerInfos[0].ID, ProviderClaude)
	}
	seen := map[Provider]bool{}
	for _, info := range providerInfos {
		if info.ID == "" {
			t.Fatal("provider with empty ID")
		}
		if seen[info.ID] {
			t.Fatalf("duplicate provider ID %q", info.ID)
		}
		seen[info.ID] = true
		if info.DisplayName == "" {
			t.Errorf("provider %q: DisplayName is required", info.ID)
		}
		if info.ProfileDirKey != "config_dir" && info.ProfileDirKey != "home_dir" {
			t.Errorf("provider %q: ProfileDirKey %q must be config_dir or home_dir", info.ID, info.ProfileDirKey)
		}
		if info.InstructionDelivery == "" {
			t.Errorf("provider %q: InstructionDelivery is required", info.ID)
		}
		if info.NativeAgentSep == "" {
			t.Errorf("provider %q: NativeAgentSep is required", info.ID)
		}
		defaults := 0
		for _, m := range info.FallbackModels {
			if m.Default {
				defaults++
			}
		}
		if defaults != 1 {
			t.Errorf("provider %q: %d default fallback models, want exactly 1", info.ID, defaults)
		}
	}
}

func TestProviderIDsLabel(t *testing.T) {
	if got := ProviderIDsLabel(); got != "claude|codex" {
		t.Fatalf("ProviderIDsLabel() = %q, want %q (error-message text is user-visible)", got, "claude|codex")
	}
}

func TestProviders(t *testing.T) {
	got := Providers()
	if len(got) != len(providerInfos) {
		t.Fatalf("Providers() length = %d, want %d", len(got), len(providerInfos))
	}
	for i, info := range got {
		if info.ID != providerInfos[i].ID || info.DisplayName != providerInfos[i].DisplayName {
			t.Fatalf("Providers()[%d] = %+v, want registry entry %+v", i, info, providerInfos[i])
		}
	}
	got[0] = ProviderInfo{}
	if Providers()[0].ID != providerInfos[0].ID {
		t.Fatal("mutating Providers result changed the registry")
	}
}

func TestProviderIDs(t *testing.T) {
	got := ProviderIDs()
	providers := Providers()
	if len(got) != len(providers) {
		t.Fatalf("ProviderIDs() length = %d, want %d", len(got), len(providers))
	}
	for i, id := range got {
		if id != providers[i].ID {
			t.Fatalf("ProviderIDs()[%d] = %q, want %q", i, id, providers[i].ID)
		}
		if !KnownProvider(id) {
			t.Fatalf("ProviderIDs()[%d] = %q is not a known provider", i, id)
		}
	}
}

func TestProfileDirRoundTrip(t *testing.T) {
	claude := Profile{Name: "work", Provider: ProviderClaude}
	claude.SetDir("/tmp/claude-work")
	if claude.ConfigDir != "/tmp/claude-work" || claude.HomeDir != "" {
		t.Fatalf("claude SetDir wrote wrong field: ConfigDir=%q HomeDir=%q", claude.ConfigDir, claude.HomeDir)
	}
	if claude.Dir() != "/tmp/claude-work" {
		t.Fatalf("claude Dir() = %q", claude.Dir())
	}

	codex := Profile{Name: "work2", Provider: ProviderCodex}
	codex.SetDir("/tmp/codex-work")
	if codex.HomeDir != "/tmp/codex-work" || codex.ConfigDir != "" {
		t.Fatalf("codex SetDir wrote wrong field: ConfigDir=%q HomeDir=%q", codex.ConfigDir, codex.HomeDir)
	}
	if codex.Dir() != "/tmp/codex-work" {
		t.Fatalf("codex Dir() = %q", codex.Dir())
	}
}
