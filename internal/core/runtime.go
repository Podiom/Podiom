package core

import (
	"context"
	"fmt"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
)

func (c *Core) ensureSessionInstructions(ctx context.Context, sess store.Session) error {
	_, err := c.sessionInstructionPayload(ctx, sess)
	return err
}

func (c *Core) switchSessionTarget(ctx context.Context, sess store.Session, provider config.Provider, profile string) (store.Session, error) {
	if provider != config.ProviderClaude && provider != config.ProviderCodex {
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
	updated, err := c.store.UpdateSessionRuntime(ctx, sess.ID, provider, profile, sess.Model, sess.Effort, sess.PermissionMode, "")
	if err != nil {
		return store.Session{}, err
	}
	if err := c.ensureSessionInstructions(ctx, updated); err != nil {
		return store.Session{}, err
	}
	return updated, nil
}

func (c *Core) nextFallbackSession(ctx context.Context, sess store.Session, tried map[string]bool) (store.Session, error) {
	agent, err := c.store.GetAgent(ctx, sess.AgentName)
	if err != nil {
		return store.Session{}, err
	}
	chain := agent.Fallback
	if len(chain) == 0 {
		chain = c.GetGlobal().Fallback
	}
	if len(chain) == 0 {
		return store.Session{}, fmt.Errorf("rate limited on %s; no fallback chain configured", targetLabel(sess.Provider, sess.Profile))
	}

	currentKey := targetKey(sess.Provider, sess.Profile)
	start := 0
	for i, entry := range chain {
		provider, profile, err := c.resolveFallbackTarget(agent, entry)
		if err != nil {
			return store.Session{}, err
		}
		if targetKey(provider, profile) == currentKey {
			start = i + 1
			break
		}
	}
	for _, entry := range chain[start:] {
		provider, profile, err := c.resolveFallbackTarget(agent, entry)
		if err != nil {
			return store.Session{}, err
		}
		key := targetKey(provider, profile)
		if tried[key] || key == currentKey {
			continue
		}
		return c.switchSessionTarget(ctx, sess, provider, profile)
	}
	return store.Session{}, fmt.Errorf("rate limited on %s; fallback chain exhausted", targetLabel(sess.Provider, sess.Profile))
}

func (c *Core) resolveFallbackTarget(agent store.Agent, entry string) (config.Provider, string, error) {
	if entry == "default" {
		return agent.Provider, "", nil
	}
	// A bare provider token falls back to that provider with no profile env.
	if entry == string(config.ProviderClaude) || entry == string(config.ProviderCodex) {
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
