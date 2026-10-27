package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/config"
	"github.com/ivannguyendev/chatim/apps/websocket/internal/session"
)

type stopPlan struct {
	DrainDelay time.Duration
	Commands   time.Duration
	Feed       time.Duration
	Close      time.Duration
}

func stopPlanOf(cfg config.Config) stopPlan {
	return stopPlan{DrainDelay: cfg.DrainDelay, Commands: cfg.RequestDeadline, Feed: config.FeedStopTimeout, Close: config.CloseTimeout}
}

func (p stopPlan) Total() time.Duration { return p.DrainDelay + p.Commands + p.Feed + p.Close }

func (n *node) shutdown(ctx context.Context, sup *supervisor, t tasks, cause error) error {
	sup.stopping.Store(true)
	plan := stopPlanOf(n.cfg)
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), n.cfg.ShutdownBudget)
	defer cancel()
	n.log.InfoContext(stopCtx, "websocket shutting down", "component_failed", cause != nil, "stop_plan", plan.Total(), "budget", n.cfg.ShutdownBudget)

	n.admin.SetReady(false)
	if cause == nil {
		pause(stopCtx, plan.DrainDelay)
	}
	t.listener.cancel()
	var failed []string
	fail := func(step string, err error) {
		if err != nil {
			failed = append(failed, step)
			n.log.ErrorContext(stopCtx, "shutdown step failed", "step", step, "err", err)
		}
	}
	fail("commands", within(stopCtx, plan.Commands, n.ws.Drain))
	fail("typing", n.typing.Stop())
	fail("feed", within(stopCtx, plan.Feed, n.feed.Stop))
	n.ws.CloseAll(session.CodeGoingAway)
	fail("connections", within(stopCtx, plan.Close, func(c context.Context) error {
		return errors.Join(n.ws.Wait(c), t.listener.stop(c))
	}))
	n.online.Stop()
	fail("iam sync", t.sync.stop(stopCtx))
	fail("presence", closePresence(n.redis))
	n.rooms.Close()
	fail("core router", n.router.Close())
	fail("admin", t.admin.stop(stopCtx))
	n.log.InfoContext(stopCtx, "websocket stopped", "failed_steps", failed)
	if len(failed) > 0 {
		return errors.Join(cause, fmt.Errorf("shutdown incomplete: %v failed", failed))
	}
	return cause
}

func within(ctx context.Context, limit time.Duration, step func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	if err := step(ctx); err != nil {
		return fmt.Errorf("within %v: %w", limit, err)
	}
	return nil
}

func pause(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}
