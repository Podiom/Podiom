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

var errUserInputTimeout = errors.New("user input request timed out")

type userInputBroker struct {
	mu      sync.Mutex
	turns   map[string]chan adapter.UserInputRequest
	pending map[string]chan adapter.UserInputDecision
	meta    map[string]userInputMeta
	log     *slog.Logger
}

type userInputMeta struct {
	sessionID      string
	restoreRoadmap bool
}

func newUserInputBroker(loggers ...*slog.Logger) *userInputBroker {
	log := slog.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		log = loggers[0]
	}
	return &userInputBroker{
		turns:   map[string]chan adapter.UserInputRequest{},
		pending: map[string]chan adapter.UserInputDecision{},
		meta:    map[string]userInputMeta{},
		log:     log,
	}
}

func (b *userInputBroker) subscribe(turnID string) (<-chan adapter.UserInputRequest, func()) {
	ch := make(chan adapter.UserInputRequest, 8)
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

func (b *userInputBroker) RequestUserInput(ctx context.Context, req adapter.UserInputRequest, timeout time.Duration) (adapter.UserInputDecision, error) {
	if timeout <= 0 {
		timeout = defaultHTTPPermissionTimeout
	}
	decisionCh := make(chan adapter.UserInputDecision, 1)
	b.mu.Lock()
	b.pending[req.ID] = decisionCh
	turnCh := b.turns[req.TurnID]
	b.mu.Unlock()
	b.log.Info("user input requested",
		"event", "user_input",
		"turn", req.TurnID,
		"request", req.ID,
		"provider", string(req.Provider),
		"item", req.ItemID,
		"questions", len(req.Questions),
		"delivered", turnCh != nil,
	)
	defer func() {
		b.mu.Lock()
		delete(b.pending, req.ID)
		b.mu.Unlock()
	}()

	if turnCh != nil {
		select {
		case <-ctx.Done():
			b.log.Info("user input canceled",
				"event", "user_input",
				"turn", req.TurnID,
				"request", req.ID,
				"provider", string(req.Provider),
				podiomlog.ErrorAttr(ctx.Err()),
			)
			return adapter.UserInputDecision{}, ctx.Err()
		case turnCh <- req:
			b.log.Info("user input delivered",
				"event", "user_input",
				"turn", req.TurnID,
				"request", req.ID,
				"provider", string(req.Provider),
				"questions", len(req.Questions),
			)
		default:
			b.log.Warn("user input delivery skipped",
				"event", "user_input",
				"turn", req.TurnID,
				"request", req.ID,
				"provider", string(req.Provider),
				"reason", "subscriber_queue_full",
			)
		}
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		b.log.Info("user input canceled",
			"event", "user_input",
			"turn", req.TurnID,
			"request", req.ID,
			"provider", string(req.Provider),
			podiomlog.ErrorAttr(ctx.Err()),
		)
		return adapter.UserInputDecision{}, ctx.Err()
	case decision := <-decisionCh:
		if decision.Answers == nil {
			decision.Answers = map[string][]string{}
		}
		b.log.Info("user input answered",
			"event", "user_input",
			"turn", req.TurnID,
			"request", req.ID,
			"provider", string(req.Provider),
			"answer_keys", len(decision.Answers),
		)
		return decision, nil
	case <-timer.C:
		b.log.Info("user input timed out",
			"event", "user_input",
			"turn", req.TurnID,
			"request", req.ID,
			"provider", string(req.Provider),
			"timeout_ms", timeout.Milliseconds(),
		)
		return adapter.UserInputDecision{}, errUserInputTimeout
	}
}

func (b *userInputBroker) decide(id string, decision adapter.UserInputDecision) bool {
	b.mu.Lock()
	ch := b.pending[id]
	b.mu.Unlock()
	if ch == nil {
		b.log.Warn("user input decision missing",
			"event", "user_input",
			"request", id,
			"answer_keys", len(decision.Answers),
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

func (b *userInputBroker) attach(id, sessionID string, restoreRoadmap bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if existing := b.meta[id]; existing.restoreRoadmap && !restoreRoadmap {
		return
	}
	b.meta[id] = userInputMeta{sessionID: sessionID, restoreRoadmap: restoreRoadmap}
}

func (b *userInputBroker) popMeta(id string) userInputMeta {
	b.mu.Lock()
	defer b.mu.Unlock()
	meta := b.meta[id]
	delete(b.meta, id)
	return meta
}
