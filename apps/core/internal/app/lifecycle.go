package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

type tasks struct {
	admin, publisher, readBatch, stateClears, flusher, cidBatch, router, slots, timers, projections, mention, sweeper, grpc *task
}

func Run(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	log = redactedLogger(log, cfg)
	log.InfoContext(ctx, "starting core", "config", cfg.LogValue())
	cl, err := connect(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer cl.close(ctx, log)
	a, err := wire(cfg, cl, log)
	if err != nil {
		return err
	}
	return a.launch(ctx, func(ctx context.Context) ([]net.Listener, error) {
		if err := prepare(ctx, cfg, cl, a.indexes, a.setups...); err != nil {
			return nil, err
		}
		return listenAll(ctx, cfg.AdminAddr, cfg.GRPCAddr)
	})
}

func (a *node) launch(ctx context.Context, ready func(context.Context) ([]net.Listener, error)) error {
	lis, err := ready(ctx)
	if err != nil {
		a.abandon(ctx)
		return err
	}
	return a.serve(ctx, lis[0], lis[1])
}

func (a *node) abandon(ctx context.Context) {
	if a.watches != nil {
		a.watches.Stop()
	}
	if a.mention == nil {
		return
	}
	if err := a.mention.Close(context.WithoutCancel(ctx)); err != nil {
		a.log.WarnContext(ctx, "mention app close after a failed start", "err", err)
	}
}

func (a *node) serve(ctx context.Context, adminLis, grpcLis net.Listener) error {
	sup := newSupervisor(ctx, 13)
	t := tasks{
		admin:       sup.start("admin", func(c context.Context) error { return a.admin.ServeListener(c, adminLis) }),
		publisher:   sup.start("publisher", a.publisher.Run),
		readBatch:   sup.start("read batch", a.readBatch.Run),
		stateClears: sup.start("state clear queue", a.stateClears.Run),
		flusher:     sup.start("flusher", a.flusher.Run),
		cidBatch:    sup.start("cid batcher", a.cidBatch.Run),
		router:      sup.start("router", a.router.Run),
		slots:       sup.start("slot manager", a.slots.Run),
		timers:      sup.start("timer consumer", a.timers.Run),
		projections: sup.start("projections", a.projections.Run),
		mention:     startIf(sup, "mention", a.mention),
		sweeper:     sup.start("sweeper", a.sweeper.Run),
	}
	cause := a.awaitRouter(ctx, sup)
	if cause == nil && ctx.Err() == nil {
		cause = a.watches.Start(ctx)
	}
	if cause != nil || ctx.Err() != nil {
		_ = grpcLis.Close()
		return a.shutdown(ctx, sup, t, cause)
	}
	t.grpc = sup.start("grpc", func(c context.Context) error { return a.grpc.ServeListener(c, grpcLis) })
	a.admin.SetReady(true)
	a.log.InfoContext(ctx, "core ready", "grpc", grpcLis.Addr().String(), "admin", adminLis.Addr().String())
	return a.shutdown(ctx, sup, t, sup.wait(ctx))
}

func startIf(sup *supervisor, name string, d drainer) *task {
	if d == nil {
		return nil
	}
	return sup.start(name, d.Run)
}

func (a *node) awaitRouter(ctx context.Context, sup *supervisor) error {
	select {
	case <-a.router.Running():
		return nil
	case err := <-sup.failed:
		return err
	case <-ctx.Done():
		return nil
	}
}

func listenAll(ctx context.Context, addrs ...string) ([]net.Listener, error) {
	var lc net.ListenConfig
	lis := make([]net.Listener, 0, len(addrs))
	for _, addr := range addrs {
		l, err := lc.Listen(ctx, "tcp", addr)
		if err != nil {
			for _, open := range lis {
				_ = open.Close()
			}
			return nil, fmt.Errorf("listen %s: %w", addr, err)
		}
		lis = append(lis, l)
	}
	return lis, nil
}
