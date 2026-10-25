package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

type stopper struct {
	ctx    context.Context
	log    *slog.Logger
	mu     sync.Mutex
	failed []string
}

type phase struct {
	name  string
	limit time.Duration
	drain func(context.Context) error
	task  *task
}

func (a *node) shutdown(ctx context.Context, sup *supervisor, t tasks, cause error) error {
	sup.stopping.Store(true)
	plan := a.cfg.StopPlan()
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.cfg.ShutdownBudget-config.CloseTimeout)
	defer cancel()
	a.log.InfoContext(stopCtx, "core shutting down", "component_failed", cause != nil, "stop_plan", plan.Total(), "budget", a.cfg.ShutdownBudget)

	a.admin.SetReady(false)
	a.grpc.Health().Shutdown()
	if cause == nil {
		pause(stopCtx, plan.DrainDelay)
	}
	s := &stopper{ctx: stopCtx, log: a.log}
	s.step("grpc", 0, nil, t.grpc)
	s.step("sweeper", plan.Sweeper, a.sweeper.Close, t.sweeper)
	s.together(phase{"projections", plan.Projections, a.projections.Close, t.projections}, a.mentionPhase(plan.Mention, t.mention))
	s.step("timer consumer", plan.Timer, a.timers.Close, t.timers)
	a.watches.Stop()
	s.step("router", plan.Router, a.router.Close, t.router)
	s.step("cid batcher", plan.CIDBatch, a.cidBatch.Close, t.cidBatch)
	s.step("flusher", plan.Flusher, a.flusher.Close, t.flusher)
	publishBy := time.Now().Add(plan.Publisher)
	s.step("read batch", plan.Publisher, a.readBatch.Close, t.readBatch)
	s.step("publisher", max(time.Until(publishBy), 0), a.publisher.Close, t.publisher)
	s.step("state clear queue", plan.StateClear, a.stateClears.Close, t.stateClears)
	s.step("slot manager", 0, nil, t.slots)
	s.step("admin", 0, nil, t.admin)
	a.log.InfoContext(stopCtx, "core stopped", "failed_steps", s.failed)
	return errors.Join(cause, s.err())
}

func (s *stopper) step(name string, limit time.Duration, drain func(context.Context) error, t *task) {
	var err error
	if drain != nil {
		ctx, cancel := context.WithTimeout(s.ctx, limit)
		if derr := drain(ctx); derr != nil {
			err = fmt.Errorf("drain within %v: %w", limit, derr)
		}
		cancel()
	}
	if t != nil {
		err = errors.Join(err, t.stop(s.ctx))
	}
	if err != nil {
		s.mu.Lock()
		s.failed = append(s.failed, name)
		s.mu.Unlock()
		s.log.ErrorContext(s.ctx, "shutdown step failed", "step", name, "err", err)
	}
}

func (s *stopper) together(phases ...phase) {
	var wg sync.WaitGroup
	for _, p := range phases {
		if p.drain == nil && p.task == nil {
			continue
		}
		wg.Go(func() { s.step(p.name, p.limit, p.drain, p.task) })
	}
	wg.Wait()
}

func (a *node) mentionPhase(limit time.Duration, t *task) phase {
	if a.mention == nil {
		return phase{name: "mention"}
	}
	return phase{"mention", limit, a.mention.Close, t}
}

func (s *stopper) err() error {
	if len(s.failed) == 0 {
		return nil
	}
	return fmt.Errorf("shutdown incomplete: %v failed", s.failed)
}

func pause(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}
