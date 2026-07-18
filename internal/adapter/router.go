package adapter

import (
	"context"
	"fmt"

	"github.com/Podiom/Podiom/internal/capabilities"
	"github.com/Podiom/Podiom/internal/config"
)

// Router dispatches adapter calls to the implementation for the owning
// provider. It lets core stay provider-neutral while daemon startup wires each
// CLI independently.
type Router struct {
	adapters map[config.Provider]Adapter
}

// NewRouter returns a provider-dispatching adapter.
func NewRouter(adapters map[config.Provider]Adapter) *Router {
	copied := map[config.Provider]Adapter{}
	for provider, adapter := range adapters {
		if adapter != nil {
			copied[provider] = adapter
		}
	}
	return &Router{adapters: copied}
}

func (r *Router) Start(ctx context.Context, req StartRequest) (Handle, error) {
	ad, err := r.adapter(req.Provider)
	if err != nil {
		return Handle{}, err
	}
	return ad.Start(ctx, req)
}

func (r *Router) Resume(ctx context.Context, req ResumeRequest) (Handle, error) {
	ad, err := r.adapter(req.Handle.Provider)
	if err != nil {
		return Handle{}, err
	}
	return ad.Resume(ctx, req)
}

func (r *Router) SendTurn(ctx context.Context, req TurnRequest) (<-chan Event, error) {
	ad, err := r.adapter(req.Handle.Provider)
	if err != nil {
		return nil, err
	}
	return ad.SendTurn(ctx, req)
}

func (r *Router) Teardown(ctx context.Context, handle Handle) error {
	ad, err := r.adapter(handle.Provider)
	if err != nil {
		return err
	}
	return ad.Teardown(ctx, handle)
}

func (r *Router) Capabilities(ctx context.Context, req capabilities.Request) (capabilities.ProviderCapabilities, error) {
	ad, err := r.adapter(req.Provider)
	if err != nil {
		return capabilities.WithError(capabilities.Fallback(req.Provider, req.Profile), err), nil
	}
	return ad.Capabilities(ctx, req)
}

// RefreshCredentials fans out to every adapter that caches credentials in a
// long-lived process. Adapters that do not implement CredentialRefresher (the
// per-turn Claude adapter, Fake, Unavailable) are skipped.
func (r *Router) RefreshCredentials() {
	if r == nil {
		return
	}
	for _, ad := range r.adapters {
		if cr, ok := ad.(CredentialRefresher); ok {
			cr.RefreshCredentials()
		}
	}
}

func (r *Router) adapter(provider config.Provider) (Adapter, error) {
	if provider == "" {
		return nil, fmt.Errorf("provider is required")
	}
	if r == nil {
		return nil, fmt.Errorf("%s adapter unavailable", provider)
	}
	ad := r.adapters[provider]
	if ad == nil {
		return nil, fmt.Errorf("%s adapter unavailable", provider)
	}
	return ad, nil
}

// Unavailable is installed for configured providers whose CLI could not be
// discovered at daemon startup. It fails only when that provider is used.
type Unavailable struct {
	Provider config.Provider
	Err      error
}

func (u Unavailable) Start(context.Context, StartRequest) (Handle, error) {
	return Handle{}, u.err()
}

func (u Unavailable) Resume(context.Context, ResumeRequest) (Handle, error) {
	return Handle{}, u.err()
}

func (u Unavailable) SendTurn(context.Context, TurnRequest) (<-chan Event, error) {
	return nil, u.err()
}

func (u Unavailable) Teardown(context.Context, Handle) error {
	return u.err()
}

func (u Unavailable) Capabilities(_ context.Context, req capabilities.Request) (capabilities.ProviderCapabilities, error) {
	return capabilities.WithError(capabilities.Fallback(req.Provider, req.Profile), u.err()), nil
}

func (u Unavailable) err() error {
	if u.Err != nil {
		return fmt.Errorf("%s adapter unavailable: %w", u.Provider, u.Err)
	}
	return fmt.Errorf("%s adapter unavailable", u.Provider)
}
