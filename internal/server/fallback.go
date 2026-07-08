package server

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/Podiom/Podiom/internal/core"
	podiomlog "github.com/Podiom/Podiom/internal/logging"
)

var errFallbackTimeout = errors.New("fallback decision timed out")

// fallbackBroker blocks an interactive turn on the user's answer to a reached
// session limit. It mirrors permissionBroker: the core turn calls
// RequestFallback and parks until the WebSocket layer routes a decision back via
// decide, or the context/timeout fires.
type fallbackBroker struct {
	mu      sync.Mutex
	turns   map[string]chan core.FallbackRequest
	pending map[string]chan core.FallbackDecision
	log     *slog.Logger
}

func newFallbackBroker(loggers ...*slog.Logger) *fallbackBroker {
	log := slog.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		log = loggers[0]
	}
	return &fallbackBroker{
		turns:   map[string]chan core.FallbackRequest{},
		pending: map[string]chan core.FallbackDecision{},
		log:     log,
	}
}

func (b *fallbackBroker) subscribe(turnID string) (<-chan core.FallbackRequest, func()) {
	ch := make(chan core.FallbackRequest, 8)
	b.mu.Lock()
	b.turns[turnID] = ch
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.turns, turnID)
		close(ch)
		b.mu.Unlock()
	}
}

// RequestFallback implements core.FallbackRelay. It delivers the request to the
// turn's subscriber (for display) and waits for a decision.
func (b *fallbackBroker) RequestFallback(ctx context.Context, req core.FallbackRequest, timeout time.Duration) (core.FallbackDecision, error) {
	decisionCh := make(chan core.FallbackDecision, 1)
	b.mu.Lock()
	b.pending[req.ID] = decisionCh
	turnCh := b.turns[req.TurnID]
	b.mu.Unlock()
	b.log.Info("fallback requested",
		"event", "fallback",
		"turn", req.TurnID,
		"request", req.ID,
		"session", req.SessionID,
		"from", req.Label,
		"delivered", turnCh != nil,
	)
	defer func() {
		b.mu.Lock()
		delete(b.pending, req.ID)
		b.mu.Unlock()
	}()

	req.ExpiresAt = time.Now().Add(timeout).UTC()
	if turnCh != nil {
		select {
		case <-ctx.Done():
			return core.FallbackDecision{}, ctx.Err()
		case turnCh <- req:
		default:
			b.log.Warn("fallback delivery skipped",
				"event", "fallback",
				"turn", req.TurnID,
				"request", req.ID,
				"reason", "subscriber_queue_full",
			)
		}
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return core.FallbackDecision{}, ctx.Err()
	case decision := <-decisionCh:
		b.log.Info("fallback decided",
			"event", "fallback",
			"turn", req.TurnID,
			"request", req.ID,
			"action", decision.Action,
		)
		return decision, nil
	case <-timer.C:
		b.log.Info("fallback timed out",
			"event", "fallback",
			"turn", req.TurnID,
			"request", req.ID,
			"timeout_ms", timeout.Milliseconds(),
			podiomlog.ErrorAttr(errFallbackTimeout),
		)
		return core.FallbackDecision{}, errFallbackTimeout
	}
}

func (b *fallbackBroker) decide(id string, decision core.FallbackDecision) bool {
	b.mu.Lock()
	ch := b.pending[id]
	b.mu.Unlock()
	if ch == nil {
		b.log.Warn("fallback decision missing",
			"event", "fallback",
			"request", id,
			"action", decision.Action,
		)
		return false
	}
	select {
	case ch <- decision:
		return true
	default:
		return false
	}
}
