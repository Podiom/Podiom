package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Podiom/Podiom/internal/capabilities"
	"github.com/Podiom/Podiom/internal/config"
)

// Fake is a deterministic in-memory adapter for tests and local core
// development. It never shells out or spends tokens.
type Fake struct {
	mu               sync.Mutex
	started          map[string]Handle
	Responses        []string
	Reasoning        []string
	Requests         []TurnRequest
	StartRequests    []StartRequest
	RateLimitedTurns int
	ResponseDelay    time.Duration
	SendTurnError    error
	// PermissionTool, when set, makes each turn request approval for this tool
	// name through the turn's PermissionRelay before responding. It models the
	// unattended preapproved policy (§7.7) so tests can assert allow/deny.
	PermissionTool string
	// Decisions records each permission decision the relay returned, in order.
	Decisions []PermissionDecision
	// ToolUses, when set, are emitted as EventToolUse before the assistant
	// message so tests can exercise the goal tool-use audit path.
	ToolUses []ToolUse
}

// NewFake returns a fake adapter with the default echo-style response script.
func NewFake() *Fake {
	return &Fake{started: map[string]Handle{}}
}

// Start records a new backing handle for a session.
func (f *Fake) Start(ctx context.Context, req StartRequest) (Handle, error) {
	if err := ctx.Err(); err != nil {
		return Handle{}, err
	}
	handle := Handle{Provider: req.Provider, ID: fmt.Sprintf("fake-%s", req.SessionID)}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.started == nil {
		f.started = map[string]Handle{}
	}
	f.started[req.SessionID] = handle
	f.StartRequests = append(f.StartRequests, req)
	return handle, nil
}

// Resume returns the supplied handle unchanged.
func (f *Fake) Resume(ctx context.Context, req ResumeRequest) (Handle, error) {
	if err := ctx.Err(); err != nil {
		return Handle{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.started == nil {
		f.started = map[string]Handle{}
	}
	f.started[req.SessionID] = req.Handle
	return req.Handle, nil
}

// SendTurn streams a final assistant message and a turn-done marker.
func (f *Fake) SendTurn(ctx context.Context, req TurnRequest) (<-chan Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.SendTurnError != nil {
		f.mu.Lock()
		f.Requests = append(f.Requests, req)
		err := f.SendTurnError
		f.mu.Unlock()
		return nil, err
	}
	rateLimited, response, reasoning := f.nextResult(req)
	ch := make(chan Event, 4)
	go func() {
		defer close(ch)
		if rateLimited {
			select {
			case <-ctx.Done():
				return
			case ch <- Event{Kind: EventRateLimited, Content: "fake rate limit"}:
			}
			select {
			case <-ctx.Done():
				return
			case ch <- Event{Kind: EventTurnDone}:
			}
			return
		}
		if f.PermissionTool != "" {
			decision := f.requestPermission(ctx, req)
			if decision.Behavior != "allow" {
				response = fmt.Sprintf("denied %s: %s", f.PermissionTool, decision.Message)
			}
		}
		if f.ResponseDelay > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(f.ResponseDelay):
			}
		}
		if reasoning != "" {
			select {
			case <-ctx.Done():
				return
			case ch <- Event{Kind: EventReasoningMessage, Content: reasoning}:
			}
		}
		for i := range f.ToolUses {
			tu := f.ToolUses[i]
			select {
			case <-ctx.Done():
				return
			case ch <- Event{Kind: EventToolUse, ToolUse: &tu}:
			}
		}
		select {
		case <-ctx.Done():
			return
		case ch <- Event{Kind: EventAssistantMessage, Content: response}:
		}
		select {
		case <-ctx.Done():
			return
		case ch <- Event{Kind: EventTurnDone}:
		}
	}()
	return ch, nil
}

// Teardown forgets any stored fake handle.
func (f *Fake) Teardown(ctx context.Context, handle Handle) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for sessionID, h := range f.started {
		if h == handle {
			delete(f.started, sessionID)
		}
	}
	return nil
}

func (f *Fake) Capabilities(ctx context.Context, req capabilities.Request) (capabilities.ProviderCapabilities, error) {
	if err := ctx.Err(); err != nil {
		return capabilities.ProviderCapabilities{}, err
	}
	provider := req.Provider
	if provider == "" {
		provider = config.ProviderClaude
	}
	caps := capabilities.Fallback(provider, req.Profile)
	caps.Source = "fake"
	caps.Stale = false
	return caps, nil
}

// requestPermission asks the turn's relay to approve PermissionTool. A missing
// relay denies (matching real adapters: no relay means no approver).
func (f *Fake) requestPermission(ctx context.Context, req TurnRequest) PermissionDecision {
	decision := PermissionDecision{Behavior: "deny", Message: "no relay"}
	if req.Relay != nil {
		timeout := req.Settings.PermissionTimeout
		if timeout <= 0 {
			timeout = defaultPermissionTimeout
		}
		got, err := req.Relay.RequestPermission(ctx, PermissionRequest{
			ID:       "fake-perm-" + req.Settings.PermissionTurnID,
			TurnID:   req.Settings.PermissionTurnID,
			ToolName: f.PermissionTool,
			Input:    json.RawMessage(`{}`),
		}, timeout)
		if err == nil && got.Behavior != "" {
			decision = got
		}
	}
	f.mu.Lock()
	f.Decisions = append(f.Decisions, decision)
	f.mu.Unlock()
	return decision
}

// RecordedDecisions returns a copy of the permission decisions made so far,
// safe to read while background turns may still be appending.
func (f *Fake) RecordedDecisions() []PermissionDecision {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]PermissionDecision, len(f.Decisions))
	copy(out, f.Decisions)
	return out
}

func (f *Fake) nextResult(req TurnRequest) (bool, string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Requests = append(f.Requests, req)
	reasoning := ""
	if len(f.Reasoning) > 0 {
		reasoning = f.Reasoning[0]
		f.Reasoning = f.Reasoning[1:]
	}
	if f.RateLimitedTurns > 0 {
		f.RateLimitedTurns--
		return true, "", reasoning
	}
	if len(f.Responses) > 0 {
		response := f.Responses[0]
		f.Responses = f.Responses[1:]
		return false, response, reasoning
	}
	return false, fmt.Sprintf("fake response: %s", req.Message), reasoning
}
