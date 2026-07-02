// Package dream runs Podium's built-in nightly memory-consolidation loop. It is a
// thin lifecycle wrapper: the "who is due and what to do" logic lives in core
// (Core.RunDueDreams), so the runner just ticks and delegates.
package dream

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/mar-schmidt/Podium/internal/core"
)

// tickInterval controls how often the runner asks core whether any agent is due
// to dream. It is well below the daily cadence so a missed nominal dream time is
// caught within minutes of the daemon coming back up (MEM8).
const tickInterval = 15 * time.Minute

// Options configures a Runner.
type Options struct {
	Core   *core.Core
	Logger *slog.Logger
}

// Runner fires memory dreams on a periodic tick.
type Runner struct {
	core *core.Core
	log  *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	running bool
}

// New constructs a Runner. Call Start to begin.
func New(opts Options) *Runner {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{core: opts.Core, log: log, ctx: ctx, cancel: cancel}
}

// Start runs an immediate catch-up pass (for a daemon that was down over the
// nominal dream time) and then ticks on the interval.
func (r *Runner) Start() {
	r.log.Info("dream runner started", "event", "dream", "interval", tickInterval.String())
	go r.tick()
	go r.loop()
}

// Stop cancels any in-flight scan and stops the loop.
func (r *Runner) Stop() {
	r.log.Info("dream runner stopped", "event", "dream")
	r.cancel()
}

func (r *Runner) loop() {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.tick()
		}
	}
}

// tick runs one due-dream scan, guarding against overlap so a long-running scan
// is never re-entered by the next tick.
func (r *Runner) tick() {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return
	}
	r.running = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
	}()

	r.core.RunDueDreams(r.ctx)
}
