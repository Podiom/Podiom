package usage

import (
	"context"
	"net/http"
	"path/filepath"

	"github.com/Podiom/Podiom/internal/claudeauth"
	"github.com/Podiom/Podiom/internal/config"
)

// usageProvider bundles the per-provider hooks the tracker dispatches on.
// Adding a provider with a usage endpoint means one entry here; providers
// without one simply have no entry and report StatusUnsupported.
type usageProvider struct {
	// credentialPath resolves the on-disk credential file for a profile dir.
	// It doubles as the dedupe key: profiles resolving to the same path share
	// one fetch.
	credentialPath func(dir string) string
	fetch          func(ctx context.Context, hc *http.Client, dir string) Snapshot
	// windows holds the (session, weekly) usage-window keys this provider's
	// endpoint reports.
	windows [2]string
}

var usageProviders = map[config.Provider]usageProvider{
	config.ProviderClaude: {
		credentialPath: claudeauth.CredentialPath,
		fetch:          FetchClaude,
		windows:        [2]string{WindowFiveHour, WindowSevenDay},
	},
	config.ProviderCodex: {
		credentialPath: func(dir string) string { return filepath.Join(codexHomeDir(dir), "auth.json") },
		fetch:          FetchCodex,
		windows:        [2]string{WindowPrimary, WindowSecondary},
	},
}

// WindowKeyPair returns the (session, weekly) window keys for a provider,
// mirroring the mapping used by the usage UI. Unknown providers use the
// five-hour/seven-day keys, matching historical behavior.
func WindowKeyPair(provider config.Provider) (string, string) {
	if p, ok := usageProviders[provider]; ok {
		return p.windows[0], p.windows[1]
	}
	return WindowFiveHour, WindowSevenDay
}
