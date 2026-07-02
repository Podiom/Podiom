package server

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	podiomlog "github.com/Podiom/Podiom/internal/logging"
)

var errPermissionTimeout = errors.New("permission request timed out")

type permissionBroker struct {
	mu      sync.Mutex
	turns   map[string]chan adapter.PermissionRequest
	pending map[string]chan adapter.PermissionDecision
	meta    map[string]permissionMeta
	log     *slog.Logger
}

type permissionMeta struct {
	sessionID      string
	restoreRoadmap bool
}

func newPermissionBroker(loggers ...*slog.Logger) *permissionBroker {
	log := slog.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		log = loggers[0]
	}
	return &permissionBroker{
		turns:   map[string]chan adapter.PermissionRequest{},
		pending: map[string]chan adapter.PermissionDecision{},
		meta:    map[string]permissionMeta{},
		log:     log,
	}
}

func (b *permissionBroker) subscribe(turnID string) (<-chan adapter.PermissionRequest, func()) {
	ch := make(chan adapter.PermissionRequest, 8)
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

func (b *permissionBroker) RequestPermission(ctx context.Context, req adapter.PermissionRequest, timeout time.Duration) (adapter.PermissionDecision, error) {
	decisionCh := make(chan adapter.PermissionDecision, 1)
	b.mu.Lock()
	b.pending[req.ID] = decisionCh
	turnCh := b.turns[req.TurnID]
	b.mu.Unlock()
	delivered := turnCh != nil
	b.log.Info("permission requested",
		"event", "permission",
		"turn", req.TurnID,
		"request", req.ID,
		"tool_name", req.ToolName,
		"tool_use", req.ToolUseID,
		"delivered", delivered,
	)
	defer func() {
		b.mu.Lock()
		delete(b.pending, req.ID)
		b.mu.Unlock()
	}()

	if turnCh != nil {
		req.ExpiresAt = time.Now().Add(timeout).UTC()
		select {
		case <-ctx.Done():
			b.log.Info("permission auto-denied",
				"event", "permission",
				"turn", req.TurnID,
				"request", req.ID,
				"tool_name", req.ToolName,
				"reason", "context_canceled",
				podiomlog.ErrorAttr(ctx.Err()),
			)
			return adapter.PermissionDecision{Behavior: "deny"}, ctx.Err()
		case turnCh <- req:
			b.log.Info("permission delivered",
				"event", "permission",
				"turn", req.TurnID,
				"request", req.ID,
				"tool_name", req.ToolName,
			)
		default:
			b.log.Warn("permission delivery skipped",
				"event", "permission",
				"turn", req.TurnID,
				"request", req.ID,
				"tool_name", req.ToolName,
				"reason", "subscriber_queue_full",
			)
		}
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		b.log.Info("permission auto-denied",
			"event", "permission",
			"turn", req.TurnID,
			"request", req.ID,
			"tool_name", req.ToolName,
			"reason", "context_canceled",
			podiomlog.ErrorAttr(ctx.Err()),
		)
		return adapter.PermissionDecision{Behavior: "deny"}, ctx.Err()
	case decision := <-decisionCh:
		if decision.Behavior == "" {
			decision.Behavior = "deny"
		}
		b.log.Info("permission decided",
			"event", "permission",
			"turn", req.TurnID,
			"request", req.ID,
			"tool_name", req.ToolName,
			"decision", decision.Behavior,
		)
		return decision, nil
	case <-timer.C:
		b.log.Info("permission timed out",
			"event", "permission",
			"turn", req.TurnID,
			"request", req.ID,
			"tool_name", req.ToolName,
			"decision", "deny",
			"timeout_ms", timeout.Milliseconds(),
		)
		return adapter.PermissionDecision{Behavior: "deny"}, errPermissionTimeout
	}
}

func (b *permissionBroker) decide(id string, decision adapter.PermissionDecision) bool {
	b.mu.Lock()
	ch := b.pending[id]
	b.mu.Unlock()
	if ch == nil {
		b.log.Warn("permission decision missing",
			"event", "permission",
			"request", id,
			"decision", decision.Behavior,
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

func (b *permissionBroker) attach(id, sessionID string, restoreRoadmap bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if existing := b.meta[id]; existing.restoreRoadmap && !restoreRoadmap {
		return
	}
	b.meta[id] = permissionMeta{sessionID: sessionID, restoreRoadmap: restoreRoadmap}
}

func (b *permissionBroker) popMeta(id string) permissionMeta {
	b.mu.Lock()
	defer b.mu.Unlock()
	meta := b.meta[id]
	delete(b.meta, id)
	return meta
}
