package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
)

// FallbackRelay lets the server block an interactive turn on a user decision
// when a session limit is reached, instead of auto-falling-back silently.
type FallbackRelay interface {
	RequestFallback(ctx context.Context, req FallbackRequest, timeout time.Duration) (FallbackDecision, error)
}

// FallbackRequest describes a reached session limit presented to the user: the
// rate-limited target, the configured next fallback (if any), and every
// provider/profile they may switch to instead.
type FallbackRequest struct {
	ID          string           `json:"id"`
	TurnID      string           `json:"turn_id"`
	SessionID   string           `json:"session_id"`
	Provider    string           `json:"provider"`
	Profile     string           `json:"profile"`
	Label       string           `json:"label"`
	NextLabel   string           `json:"next_label,omitempty"`
	HasFallback bool             `json:"has_fallback"`
	Targets     []FallbackTarget `json:"targets"`
	ExpiresAt   time.Time        `json:"expires_at"`
}

// FallbackTarget is one selectable provider/profile in a fallback decision.
type FallbackTarget struct {
	Provider string `json:"provider"`
	Profile  string `json:"profile"`
	Label    string `json:"label"`
}

// FallbackDecision is the user's answer to a FallbackRequest. Action is either
// "use_configured" (advance the configured fallback chain) or "switch" (move to
// the given Provider/Profile).
type FallbackDecision struct {
	Action   string `json:"action"`
	Provider string `json:"provider,omitempty"`
	Profile  string `json:"profile,omitempty"`
}

// fallbackTimeout bounds how long an interactive turn waits for the user to
// resolve a session-limit prompt before the turn is abandoned.
const fallbackTimeout = 10 * time.Minute

// IsRateLimitErrorMessage conservatively identifies provider rate-limit
// failures in persisted session diagnostics. It intentionally avoids broad terms
// like "quota" so backfill does not turn unrelated provider failures into goal
// attention items.
func IsRateLimitErrorMessage(message string) bool {
	msg := strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "rate-limit") ||
		strings.Contains(msg, "rate_limited") ||
		strings.Contains(msg, "rate limited")
}

func (c *Core) ensureSessionInstructions(ctx context.Context, sess store.Session) error {
	projectCtx, err := c.sessionProjectExecutionContext(ctx, sess)
	if err != nil {
		return err
	}
	_, err = c.sessionInstructionPayload(ctx, sess, projectCtx)
	return err
}

func (c *Core) switchSessionTarget(ctx context.Context, sess store.Session, provider config.Provider, profile string) (store.Session, error) {
	if !config.KnownProvider(provider) {
		return store.Session{}, fmt.Errorf("unknown provider %q", provider)
	}
	if profile != "" {
		got, ok := c.profiles[profile]
		if !ok {
			return store.Session{}, fmt.Errorf("unknown profile %q", profile)
		}
		if got.Provider != provider {
			return store.Session{}, fmt.Errorf("profile %q belongs to provider %q, not %q", profile, got.Provider, provider)
		}
	}
	model, effort := sess.Model, sess.Effort
	if provider != sess.Provider {
		// Model IDs and effort vocabularies are provider-specific; carrying them
		// across providers makes the target reject the turn. Empty values select
		// the target provider's defaults.
		model, effort = "", ""
	}
	updated, err := c.store.UpdateSessionRuntime(ctx, sess.ID, provider, profile, model, effort, sess.PermissionMode, "")
	if err != nil {
		return store.Session{}, err
	}
	if err := c.ensureSessionInstructions(ctx, updated); err != nil {
		return store.Session{}, err
	}
	return updated, nil
}

func (c *Core) nextFallbackSession(ctx context.Context, sess store.Session, tried map[string]bool) (store.Session, error) {
	provider, profile, ok, err := c.resolveNextFallback(ctx, sess, tried)
	if err != nil {
		return store.Session{}, err
	}
	if !ok {
		return store.Session{}, fmt.Errorf("rate limited on %s; no fallback available", targetLabel(sess.Provider, sess.Profile))
	}
	return c.switchSessionTarget(ctx, sess, provider, profile)
}

// resolveNextFallback computes the next untried target in the fallback chain
// without switching to it. ok is false when there is no configured chain or no
// remaining untried entry — the interactive path treats that as "no configured
// fallback" rather than an error, since the user can still pick a target. err is
// reserved for genuine failures (missing agent, unresolvable profile).
func (c *Core) resolveNextFallback(ctx context.Context, sess store.Session, tried map[string]bool) (config.Provider, string, bool, error) {
	agent, err := c.store.GetAgent(ctx, sess.AgentName)
	if err != nil {
		return "", "", false, err
	}
	chain := agent.Fallback
	if len(chain) == 0 {
		chain = c.GetGlobal().Fallback
	}
	if len(chain) == 0 {
		return "", "", false, nil
	}

	currentKey := targetKey(sess.Provider, sess.Profile)
	start := 0
	for i, entry := range chain {
		provider, profile, err := c.resolveFallbackTarget(agent, entry)
		if err != nil {
			return "", "", false, err
		}
		if targetKey(provider, profile) == currentKey {
			start = i + 1
			break
		}
	}
	for _, entry := range chain[start:] {
		provider, profile, err := c.resolveFallbackTarget(agent, entry)
		if err != nil {
			return "", "", false, err
		}
		key := targetKey(provider, profile)
		if tried[key] || key == currentKey {
			continue
		}
		return provider, profile, true, nil
	}
	return "", "", false, nil
}

// availableFallbackTargets lists every provider/profile a user may switch a
// rate-limited session to: the agent's own provider, the two bare providers, and
// each configured profile — excluding the current target. Order is stable so the
// picker reads predictably.
func (c *Core) availableFallbackTargets(agent store.Agent, current store.Session) []FallbackTarget {
	currentKey := targetKey(current.Provider, current.Profile)
	seen := map[string]bool{currentKey: true}
	var targets []FallbackTarget
	add := func(provider config.Provider, profile string) {
		key := targetKey(provider, profile)
		if seen[key] {
			return
		}
		seen[key] = true
		targets = append(targets, FallbackTarget{
			Provider: string(provider),
			Profile:  profile,
			Label:    targetLabel(provider, profile),
		})
	}
	add(agent.Provider, "")
	for _, id := range config.ProviderIDs() {
		add(id, "")
	}
	for _, name := range c.ListProfiles() {
		add(name.Provider, name.Name)
	}
	return targets
}

// interactiveFallbackSession prompts the user (via relay) for how to resolve a
// reached session limit, then returns the switched-to session. It offers the
// configured next fallback (when one exists) plus every provider/profile the
// user could pick instead. A relay error (timeout, cancel, disconnect) aborts
// the turn.
func (c *Core) interactiveFallbackSession(ctx context.Context, sess store.Session, tried map[string]bool, relay FallbackRelay, turnID string) (store.Session, error) {
	agent, err := c.store.GetAgent(ctx, sess.AgentName)
	if err != nil {
		return store.Session{}, err
	}
	nextProvider, nextProfile, hasNext, err := c.resolveNextFallback(ctx, sess, tried)
	if err != nil {
		return store.Session{}, err
	}
	req := FallbackRequest{
		ID:          fmt.Sprintf("fallback-%s-%d", sess.ID, time.Now().UnixNano()),
		TurnID:      turnID,
		SessionID:   sess.ID,
		Provider:    string(sess.Provider),
		Profile:     sess.Profile,
		Label:       targetLabel(sess.Provider, sess.Profile),
		HasFallback: hasNext,
		Targets:     c.availableFallbackTargets(agent, sess),
	}
	if hasNext {
		req.NextLabel = targetLabel(nextProvider, nextProfile)
	}
	decision, err := relay.RequestFallback(ctx, req, fallbackTimeout)
	if err != nil {
		return store.Session{}, err
	}
	switch decision.Action {
	case "switch":
		provider := config.Provider(decision.Provider)
		if !c.isSelectableFallbackTarget(req.Targets, provider, decision.Profile) {
			return store.Session{}, fmt.Errorf("invalid fallback target %s", targetLabel(provider, decision.Profile))
		}
		tried[targetKey(provider, decision.Profile)] = true
		return c.switchSessionTarget(ctx, sess, provider, decision.Profile)
	case "use_configured":
		if !hasNext {
			return store.Session{}, fmt.Errorf("rate limited on %s; no fallback available", req.Label)
		}
		tried[targetKey(nextProvider, nextProfile)] = true
		return c.switchSessionTarget(ctx, sess, nextProvider, nextProfile)
	default:
		return store.Session{}, fmt.Errorf("unknown fallback action %q", decision.Action)
	}
}

func (c *Core) isSelectableFallbackTarget(targets []FallbackTarget, provider config.Provider, profile string) bool {
	key := targetKey(provider, profile)
	for _, t := range targets {
		if targetKey(config.Provider(t.Provider), t.Profile) == key {
			return true
		}
	}
	return false
}

func (c *Core) resolveFallbackTarget(agent store.Agent, entry string) (config.Provider, string, error) {
	if entry == "default" {
		return agent.Provider, "", nil
	}
	// A bare provider token falls back to that provider with no profile env.
	if config.KnownProvider(config.Provider(entry)) {
		return config.Provider(entry), "", nil
	}
	profile, ok := c.profiles[entry]
	if !ok {
		return "", "", fmt.Errorf("unknown fallback profile %q", entry)
	}
	return profile.Provider, profile.Name, nil
}

func targetKey(provider config.Provider, profile string) string {
	return string(provider) + ":" + profile
}

func targetLabel(provider config.Provider, profile string) string {
	if profile == "" {
		return fmt.Sprintf("%s/default", provider)
	}
	return fmt.Sprintf("%s/%s", provider, profile)
}
