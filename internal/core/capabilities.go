package core

import (
	"context"
	"fmt"
	"time"

	"github.com/mar-schmidt/Podium/internal/capabilities"
	"github.com/mar-schmidt/Podium/internal/config"
)

const providerCapabilitiesTTL = 5 * time.Minute

// ProviderCapabilities returns model/effort choices for the given provider and
// profile. Live lookup failures are represented in the returned snapshot rather
// than as hard errors so UI pickers remain usable.
func (c *Core) ProviderCapabilities(ctx context.Context, provider config.Provider, profile string, refresh bool) (capabilities.ProviderCapabilities, error) {
	if provider == "" {
		provider = c.GetGlobal().Provider
	}
	if provider != config.ProviderClaude && provider != config.ProviderCodex {
		return capabilities.ProviderCapabilities{}, fmt.Errorf("unknown provider %q (want claude|codex)", provider)
	}
	if profile == "default" {
		profile = ""
	}
	profileDir, err := c.capabilityProfileDir(provider, profile)
	if err != nil {
		return capabilities.ProviderCapabilities{}, err
	}

	key := string(provider) + "|" + profile
	now := time.Now()
	if !refresh {
		c.capMu.Lock()
		entry, ok := c.capCache[key]
		c.capMu.Unlock()
		if ok && now.Before(entry.expiresAt) {
			return capabilities.Clone(entry.caps), nil
		}
	}

	caps, err := c.adapter.Capabilities(ctx, capabilities.Request{
		Provider:   provider,
		Profile:    profile,
		ProfileDir: profileDir,
	})
	if err != nil {
		return capabilities.ProviderCapabilities{}, err
	}
	if caps.FetchedAt.IsZero() {
		caps.FetchedAt = time.Now().UTC()
	}
	c.capMu.Lock()
	c.capCache[key] = capabilityCacheEntry{caps: capabilities.Clone(caps), expiresAt: now.Add(providerCapabilitiesTTL)}
	c.capMu.Unlock()
	return capabilities.Clone(caps), nil
}

func (c *Core) capabilityProfileDir(provider config.Provider, profile string) (string, error) {
	if profile == "" {
		return "", nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	p, ok := c.profiles[profile]
	if !ok {
		return "", fmt.Errorf("unknown profile %q", profile)
	}
	if p.Provider != provider {
		return "", fmt.Errorf("profile %q belongs to provider %q, not %q", profile, p.Provider, provider)
	}
	if provider == config.ProviderCodex {
		return p.HomeDir, nil
	}
	return p.ConfigDir, nil
}
