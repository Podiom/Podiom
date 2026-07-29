package config

import "strings"

// FallbackModel is one bundled-catalogue model entry, converted to a
// capabilities.ModelOption by internal/capabilities.Fallback.
type FallbackModel struct {
	ID              string
	DisplayName     string
	Description     string
	Default         bool
	InputModalities []string
}

// ProviderInfo is the static, dependency-free description of one supported
// provider. It carries identity and data only — behavior (adapters, usage
// fetchers, auth probes) lives in per-layer tables keyed by Provider.
//
// Adding a provider:
//  1. Add an adapter in internal/adapter and a wiring block in
//     cmd/podiomd/main.go.
//  2. Add one ProviderInfo entry to providerInfos below.
//  3. Add one-line entries to usage.usageProviders and
//     providercheck.authProbes; optionally mcp.nativeImports and
//     skills.nativeRoots if the provider has native MCP/skills dirs.
//  4. Add one PROVIDERS entry + logo component in web/src/lib/providers.ts.
//
// Nothing else: config validation, core, server, schedule, onboard,
// tokenmeter, capabilities, exec, and the store schema all derive from this
// registry.
type ProviderInfo struct {
	ID          Provider
	DisplayName string // "Claude" — onboarding and UI-facing labels

	// ProfileDirKey is the Profile YAML field holding this provider's auth
	// dir: "config_dir" (ConfigDir) or "home_dir" (HomeDir). A future
	// provider reuses one of the two existing keys.
	ProfileDirKey string

	// Onboarding / provider checks (consumed by internal/providercheck).
	InstallPackage string
	LoginArgs      []string
	InstallHint    string
	LoginHint      string // base hint; OS-specific overrides stay in providercheck

	// InstructionDelivery names the core composition strategy and must equal
	// a core.DeliveryMode value ("claude_import" / "codex_bundle").
	InstructionDelivery string
	// InstructionsNeedProjectDir drops per-turn instructions when the session
	// has no project dir (Codex bundles instructions into the project tree).
	InstructionsNeedProjectDir bool

	// Native-agent projection (core/native_agents). Names are
	// "podiom<sep><stem><sep><hash>"; ConfigDir, when non-empty, is the
	// subdirectory under .podiom/native-agents/ holding generated files.
	NativeAgentSep       string
	NativeAgentConfigDir string

	// PlanReadOnlyTools are exact tool names the plan gate allows read-only.
	// Mutating tools are expressed by omission — the gate denies by default.
	// Only consulted for providers without NativePlanMode, where Podiom's own
	// gate is what enforces read-only.
	PlanReadOnlyTools []string

	// NativePlanMode marks a provider that has its own plan mode, which Podiom
	// drives instead of running its own gate: the provider explores read-only
	// and produces the plan, and the adapter emits adapter.EventPlanProposed.
	// False falls back to Podiom's gate (planModePrompt + podiom_submit_plan +
	// PlanGateRelay), which stays the contract for any provider that cannot
	// plan natively.
	NativePlanMode bool

	// QuestionEndsTurn: true when the provider's user-input questions end the
	// turn (the answer must be delivered as a follow-up turn); false when
	// questions block mid-turn and the decision resumes them. Mirrored by
	// questionEndsTurn in web/src/lib/providers.ts.
	QuestionEndsTurn bool

	// FallbackModels is the bundled model catalogue used when live capability
	// discovery is unavailable.
	FallbackModels []FallbackModel
}

// providerInfos is the ordered provider registry. Order is API-visible
// (fallback-target pickers, usage listings) — keep Claude first.
var providerInfos = []ProviderInfo{
	{
		ID:                  ProviderClaude,
		DisplayName:         "Claude",
		ProfileDirKey:       "config_dir",
		InstallPackage:      "@anthropic-ai/claude-code",
		LoginArgs:           []string{"/login"},
		InstallHint:         "Install Claude Code with Anthropic's current instructions, commonly: npm install -g @anthropic-ai/claude-code",
		LoginHint:           "Run claude /login and follow the native Claude Code login prompts.",
		InstructionDelivery: "claude_import",
		NativeAgentSep:      "-",
		PlanReadOnlyTools:   []string{"read", "ls", "glob", "grep", "webfetch", "websearch"},
		NativePlanMode:      true,
		QuestionEndsTurn:    true,
		FallbackModels: []FallbackModel{
			{ID: "claude-opus-4-8", DisplayName: "Claude Opus 4.8", Description: "Claude Opus 4.8.", Default: true, InputModalities: []string{"text", "image"}},
			{ID: "claude-sonnet-5", DisplayName: "Claude Sonnet 5", Description: "Claude Sonnet 5.", InputModalities: []string{"text", "image"}},
			{ID: "claude-haiku-4-5", DisplayName: "Claude Haiku 4.5", Description: "Claude Haiku 4.5.", InputModalities: []string{"text", "image"}},
			{ID: "claude-fable-5", DisplayName: "Claude Fable 5", Description: "Claude Fable 5.", InputModalities: []string{"text", "image"}},
		},
	},
	{
		ID:                         ProviderCodex,
		DisplayName:                "Codex",
		ProfileDirKey:              "home_dir",
		InstallPackage:             "@openai/codex",
		LoginArgs:                  []string{"login", "--device-auth"},
		InstallHint:                "Install Codex CLI with OpenAI's current instructions, commonly: npm install -g @openai/codex",
		LoginHint:                  "Run codex login --device-auth and follow the OpenAI account prompts.",
		InstructionDelivery:        "codex_bundle",
		InstructionsNeedProjectDir: true,
		NativeAgentSep:             "_",
		NativeAgentConfigDir:       "codex",
		NativePlanMode:             true,
		FallbackModels: []FallbackModel{
			{ID: "gpt-5.1", DisplayName: "GPT-5.1", Description: "Recommended full-size Codex model.", Default: true, InputModalities: []string{"text", "image"}},
			{ID: "gpt-5.1-mini", DisplayName: "GPT-5.1 mini", Description: "Faster, lower-cost Codex model.", InputModalities: []string{"text", "image"}},
			{ID: "o4", DisplayName: "o4", Description: "Reasoning-oriented OpenAI model.", InputModalities: []string{"text", "image"}},
		},
	},
}

// Providers returns the ordered provider registry.
func Providers() []ProviderInfo {
	out := make([]ProviderInfo, len(providerInfos))
	copy(out, providerInfos)
	return out
}

// ProviderInfoFor returns the registry entry for p.
func ProviderInfoFor(p Provider) (ProviderInfo, bool) {
	for _, info := range providerInfos {
		if info.ID == p {
			return info, true
		}
	}
	return ProviderInfo{}, false
}

// KnownProvider reports whether p is a registered provider.
func KnownProvider(p Provider) bool {
	_, ok := ProviderInfoFor(p)
	return ok
}

// ProviderIDs returns the registered provider identifiers in registry order.
func ProviderIDs() []Provider {
	out := make([]Provider, len(providerInfos))
	for i, info := range providerInfos {
		out[i] = info.ID
	}
	return out
}

// ProviderIDsLabel renders the registry as "claude|codex" for error messages.
func ProviderIDsLabel() string {
	ids := make([]string, len(providerInfos))
	for i, info := range providerInfos {
		ids[i] = string(info.ID)
	}
	return strings.Join(ids, "|")
}

// Dir returns the profile's provider-owned auth directory (ConfigDir for
// config_dir providers, HomeDir for home_dir providers).
func (p Profile) Dir() string {
	info, _ := ProviderInfoFor(p.Provider)
	if info.ProfileDirKey == "home_dir" {
		return p.HomeDir
	}
	return p.ConfigDir
}

// SetDir stores dir into the field matching the profile's provider.
func (p *Profile) SetDir(dir string) {
	info, _ := ProviderInfoFor(p.Provider)
	if info.ProfileDirKey == "home_dir" {
		p.HomeDir = dir
		return
	}
	p.ConfigDir = dir
}
