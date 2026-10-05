package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

type stopper struct {
	ctx    context.Context
	log    *slog.Logger
	failed []string
}

func (a *app) shutdown(ctx context.Context, sup *supervisor, t tasks, cause error) error {
	sup.stopping.Store(true)
	plan := a.cfg.StopPlan()
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.cfg.ShutdownBudget-config.CloseTimeout)
	defer cancel()
	a.log.InfoContext(stopCtx, "core shutting down", "component_failed", cause != nil)

	a.admin.SetReady(false)
	a.grpc.Health().Shutdown()
	if cause == nil {
		pause(stopCtx, plan.DrainDelay)
	}
	s := &stopper{ctx: stopCtx, log: a.log}
	s.step("grpc", 0, nil, t.grpc)
	var closeReconciler func(context.Context) error
	if a.reconciler != nil {
		closeReconciler = a.reconciler.Close
	}
	s.step("reconciler", plan.Reconciler, closeReconciler, t.reconciler)
	s.step("workers", plan.Workers, a.workers.Close, t.workers)
	s.step("router", plan.Router, a.router.Close, t.router)
	s.step("cid batcher", plan.CIDBatch, a.cidBatch.Close, t.cidBatch)
	s.step("flusher", plan.Flusher, a.flusher.Close, t.flusher)
	s.step("publisher", plan.Publisher, a.publisher.Close, t.publisher)
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
		s.failed = append(s.failed, name)
		s.log.ErrorContext(s.ctx, "shutdown step failed", "step", name, "err", err)
	}
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
