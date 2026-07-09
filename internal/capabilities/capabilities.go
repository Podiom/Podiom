// Package capabilities defines Podiom's provider model/effort catalogue.
package capabilities

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Podiom/Podiom/internal/config"
)

const (
	SourceFallback = "fallback"
)

// ProviderCapabilities is the daemon-owned contract used by UI/CLI surfaces to
// render provider model and effort choices.
type ProviderCapabilities struct {
	Provider  config.Provider `json:"provider"`
	Profile   string          `json:"profile,omitempty"`
	Source    string          `json:"source"`
	FetchedAt time.Time       `json:"fetched_at"`
	Stale     bool            `json:"stale"`
	Error     string          `json:"error,omitempty"`
	Models    []ModelOption   `json:"models"`
	Efforts   []EffortOption  `json:"efforts"`
}

// ModelOption is one selectable model. Model is the exact string sent to the
// provider; DisplayName and Description are presentation hints only.
type ModelOption struct {
	ID                     string         `json:"id,omitempty"`
	Model                  string         `json:"model"`
	DisplayName            string         `json:"display_name,omitempty"`
	Description            string         `json:"description,omitempty"`
	Hidden                 bool           `json:"hidden,omitempty"`
	IsDefault              bool           `json:"is_default,omitempty"`
	DefaultReasoningEffort string         `json:"default_reasoning_effort,omitempty"`
	SupportedEfforts       []EffortOption `json:"supported_efforts,omitempty"`
}

// EffortOption is one selectable reasoning effort.
type EffortOption struct {
	Effort      string `json:"effort"`
	Description string `json:"description,omitempty"`
}

// Request identifies the provider/profile target for live capability discovery.
type Request struct {
	Provider   config.Provider
	Profile    string
	ProfileDir string
}

var DefaultEfforts = []EffortOption{
	{Effort: "low"},
	{Effort: "medium"},
	{Effort: "high"},
	{Effort: "xhigh"},
	{Effort: "max"},
}

// Fallback returns the bundled registry for a provider.
func Fallback(provider config.Provider, profile string) ProviderCapabilities {
	caps := ProviderCapabilities{
		Provider:  provider,
		Profile:   profile,
		Source:    SourceFallback,
		FetchedAt: time.Now().UTC(),
		Stale:     true,
		Efforts:   CloneEfforts(DefaultEfforts),
	}
	switch provider {
	case config.ProviderCodex:
		caps.Models = []ModelOption{
			model("gpt-5.1", "GPT-5.1", "Recommended full-size Codex model.", true),
			model("gpt-5.1-mini", "GPT-5.1 mini", "Faster, lower-cost Codex model.", false),
			model("o4", "o4", "Reasoning-oriented OpenAI model.", false),
		}
	case config.ProviderClaude:
		caps.Models = []ModelOption{
			model("claude-opus-4-8", "Claude Opus 4.8", "Claude Opus 4.8.", true),
			model("claude-sonnet-5", "Claude Sonnet 5", "Claude Sonnet 5.", false),
			model("claude-haiku-4-5", "Claude Haiku 4.5", "Claude Haiku 4.5.", false),
			model("claude-fable-5", "Claude Fable 5", "Claude Fable 5.", false),
		}
	}
	return caps
}

func model(value, display, description string, def bool) ModelOption {
	return ModelOption{
		ID:                     value,
		Model:                  value,
		DisplayName:            display,
		Description:            description,
		IsDefault:              def,
		DefaultReasoningEffort: "medium",
		SupportedEfforts:       CloneEfforts(DefaultEfforts),
	}
}

// WithError marks a fallback snapshot with a non-fatal discovery error.
func WithError(caps ProviderCapabilities, err error) ProviderCapabilities {
	if err != nil {
		caps.Error = err.Error()
		caps.Stale = true
	}
	return caps
}

// Clone returns a deep copy.
func Clone(caps ProviderCapabilities) ProviderCapabilities {
	caps.Models = CloneModels(caps.Models)
	caps.Efforts = CloneEfforts(caps.Efforts)
	return caps
}

func CloneModels(in []ModelOption) []ModelOption {
	out := make([]ModelOption, len(in))
	copy(out, in)
	for i := range out {
		out[i].SupportedEfforts = CloneEfforts(out[i].SupportedEfforts)
	}
	return out
}

func CloneEfforts(in []EffortOption) []EffortOption {
	out := make([]EffortOption, len(in))
	copy(out, in)
	return out
}

// MergeEfforts replaces provider-level and model-level efforts with parsed
// values when the provider CLI advertises them.
func MergeEfforts(caps ProviderCapabilities, efforts []EffortOption) ProviderCapabilities {
	if len(efforts) == 0 {
		return caps
	}
	caps.Efforts = CloneEfforts(efforts)
	for i := range caps.Models {
		caps.Models[i].SupportedEfforts = CloneEfforts(efforts)
		if caps.Models[i].DefaultReasoningEffort == "" || !HasEffort(efforts, caps.Models[i].DefaultReasoningEffort) {
			caps.Models[i].DefaultReasoningEffort = defaultEffort(efforts)
		}
	}
	return caps
}

func defaultEffort(efforts []EffortOption) string {
	if HasEffort(efforts, "medium") {
		return "medium"
	}
	if len(efforts) == 0 {
		return ""
	}
	return efforts[0].Effort
}

func HasEffort(efforts []EffortOption, effort string) bool {
	for _, option := range efforts {
		if option.Effort == effort {
			return true
		}
	}
	return false
}

var parenRE = regexp.MustCompile(`\(([^)]*)\)`)

// ParseEffortsFromHelp extracts advertised effort values from CLI help text.
func ParseEffortsFromHelp(help string) []EffortOption {
	var found []string
	parseWindow := 0
	for _, line := range strings.Split(help, "\n") {
		if strings.Contains(strings.ToLower(line), "effort") {
			parseWindow = 2
		}
		if parseWindow <= 0 {
			continue
		}
		for _, match := range parenRE.FindAllStringSubmatch(line, -1) {
			for _, part := range strings.FieldsFunc(match[1], func(r rune) bool {
				return r == ',' || r == '|' || r == ' ' || r == '\t'
			}) {
				part = strings.ToLower(strings.TrimSpace(part))
				switch part {
				case "low", "medium", "high", "xhigh", "max":
					found = append(found, part)
				}
			}
		}
		parseWindow--
	}
	if len(found) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []EffortOption
	order := map[string]int{"low": 0, "medium": 1, "high": 2, "xhigh": 3, "max": 4}
	for _, value := range found {
		if !seen[value] {
			seen[value] = true
			out = append(out, EffortOption{Effort: value})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return order[out[i].Effort] < order[out[j].Effort] })
	return out
}
