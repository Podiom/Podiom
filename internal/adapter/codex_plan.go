package adapter

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// Codex's native plan mode is driven through the app-server's collaboration
// modes. Two things make it work, both verified against codex-cli 0.142.4:
//
//   - It is gated on capabilities.experimentalApi, which Podiom already
//     requests at initialize. Without it, collaborationMode/list and
//     turn/start.collaborationMode are both rejected with -32600.
//   - `codex app-server generate-json-schema` hides collaborationMode unless
//     run with --experimental. Reading the reduced schema is how an earlier
//     investigation wrongly concluded plan mode was unreachable; regenerate
//     with --experimental when checking this against a new Codex release.
//
// The mode is behavioral, not a sandbox boundary — a plan turn under a
// workspace-write sandbox declined to write, but Podiom still pins read-only
// while planning rather than relying on instruction alone.

// codexCollabPreset is one entry from collaborationMode/list. Model is often
// null, meaning "keep using the thread's active model".
type codexCollabPreset struct {
	Name            string `json:"name"`
	Mode            string `json:"mode"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
}

// codexMeta caches the per-app-server discovery that collaboration modes need.
// Presets and the default model do not change while the process lives.
type codexMeta struct {
	mu           sync.Mutex
	presets      []codexCollabPreset
	presetsDone  bool
	defaultModel string
	modelDone    bool
}

// codexDiscoveryTimeout bounds the optional discovery calls. A server that does
// not implement them (an older Codex, or one where the experimental capability
// was lost) must degrade to "no collaboration mode" rather than hang the turn
// waiting for a reply that will never come.
const codexDiscoveryTimeout = 10 * time.Second

// collaborationPresets returns the server's advertised modes, discovered once.
// Names and efforts are deliberately not hardcoded — they are server data.
func (c *codexClient) collaborationPresets(ctx context.Context) []codexCollabPreset {
	c.meta.mu.Lock()
	defer c.meta.mu.Unlock()
	if c.meta.presetsDone {
		return c.meta.presets
	}
	c.meta.presetsDone = true
	ctx, cancel := context.WithTimeout(ctx, codexDiscoveryTimeout)
	defer cancel()
	raw, err := c.call(ctx, "collaborationMode/list", map[string]any{})
	if err != nil {
		// -32600 here means the experimental capability was lost at initialize.
		c.log.Warn("collaboration modes unavailable; plan mode will fall back",
			"stage", "collaboration_mode", "method", "collaborationMode/list", "error", err)
		return nil
	}
	var resp struct {
		Data []codexCollabPreset `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil
	}
	c.meta.presets = resp.Data
	return c.meta.presets
}

// codexDefaultModel returns the account's default model id. collaborationMode
// settings require a model, and the preset's own value is usually null.
//
// It must be discovered, never hardcoded: an account default can be a model the
// installed CLI does not support, which fails the turn with an unrelated HTTP
// 400. model/list is authoritative and returns its catalogue under result.data.
func (c *codexClient) codexDefaultModel(ctx context.Context) string {
	c.meta.mu.Lock()
	defer c.meta.mu.Unlock()
	if c.meta.modelDone {
		return c.meta.defaultModel
	}
	c.meta.modelDone = true
	ctx, cancel := context.WithTimeout(ctx, codexDiscoveryTimeout)
	defer cancel()
	raw, err := c.call(ctx, "model/list", map[string]any{"limit": 100})
	if err != nil {
		return ""
	}
	var resp struct {
		Data []struct {
			ID        string `json:"id"`
			Model     string `json:"model"`
			IsDefault bool   `json:"isDefault"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return ""
	}
	for _, model := range resp.Data {
		if model.IsDefault {
			c.meta.defaultModel = firstNonEmptyString(model.Model, model.ID)
			return c.meta.defaultModel
		}
	}
	return ""
}

// collaborationMode builds the turn/start.collaborationMode payload.
//
// The intended mode rides on every turn rather than only on plan turns: the
// setting is sticky on the thread, so an approved plan's implementation turn
// must say "default" explicitly or it would keep planning.
//
// Returns nil when the mode cannot be resolved — no presets (older or
// non-experimental server) or no model. Omitting the field leaves the thread on
// its existing behaviour, which is the safe degradation.
func (c *codexClient) collaborationMode(ctx context.Context, settings TurnSettings) map[string]any {
	presets := c.collaborationPresets(ctx)
	if len(presets) == 0 {
		return nil
	}
	want := "default"
	if settings.PlanMode {
		want = "plan"
	}
	var preset codexCollabPreset
	found := false
	for _, candidate := range presets {
		if candidate.Mode == want {
			preset, found = candidate, true
			break
		}
	}
	if !found {
		return nil
	}
	model := firstNonEmptyString(settings.Model, preset.Model, c.codexDefaultModel(ctx))
	if model == "" {
		return nil
	}
	modeSettings := map[string]any{
		"model": model,
		// null selects Codex's built-in instructions for the mode. It does not
		// clear the thread's developerInstructions — verified: an identity
		// marker planted there was still honoured inside plan mode — so
		// Podiom's composed agent instructions survive planning.
		"developer_instructions": nil,
	}
	if effort := firstNonEmptyString(settings.Effort, preset.ReasoningEffort); effort != "" {
		modeSettings["reasoning_effort"] = effort
	} else {
		modeSettings["reasoning_effort"] = nil
	}
	return map[string]any{"mode": want, "settings": modeSettings}
}

// codexPlanProposal extracts the plan from a completed plan item.
//
// The completed item is authoritative: the schema warns that concatenated
// item/plan/delta text may differ from it, so Podiom ignores the deltas and
// takes the final text. Do not confuse this with turn/plan/updated, which is
// the separate update_plan progress checklist and never carries a plan.
func codexPlanProposal(method string, params json.RawMessage) *PlanProposal {
	if method != "item/completed" {
		return nil
	}
	var p struct {
		Item struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"item"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil
	}
	if p.Item.Type != "plan" {
		return nil
	}
	markdown := strings.TrimSpace(p.Item.Text)
	if markdown == "" {
		return nil
	}
	// Codex produces no file; Podiom writes its own canonical copy.
	return &PlanProposal{Markdown: markdown}
}
