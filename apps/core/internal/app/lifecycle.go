package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

type tasks struct {
	admin, publisher, flusher, cidBatch, router, slots, workers, reconciler, grpc *task
}

func run(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	log = redactedLogger(log, cfg)
	log.InfoContext(ctx, "starting core", "config", cfg.LogValue())
	cl, err := connect(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer cl.close(ctx, log)
	if err := prepare(ctx, cfg, cl); err != nil {
		return err
	}
	a, err := wire(cfg, cl, log)
	if err != nil {
		return err
	}
	lis, err := listenAll(ctx, cfg.AdminAddr, cfg.GRPCAddr)
	if err != nil {
		return err
	}
	return a.serve(ctx, lis[0], lis[1])
}

func (a *node) serve(ctx context.Context, adminLis, grpcLis net.Listener) error {
	sup := newSupervisor(ctx, 9)
	t := tasks{
		admin:     sup.start("admin", func(c context.Context) error { return a.admin.ServeListener(c, adminLis) }),
		publisher: sup.start("publisher", a.publisher.Run),
		flusher:   sup.start("flusher", a.flusher.Run),
		cidBatch:  sup.start("cid batcher", a.cidBatch.Run),
		router:    sup.start("router", a.router.Run),
		slots:     sup.start("slot manager", a.slots.Run),
		workers:   sup.start("workers", a.workers.Run),
	}
	if a.reconciler != nil {
		t.reconciler = sup.start("reconciler", a.reconciler.Run)
	}
	cause := a.awaitRouter(ctx, sup)
	if cause == nil && ctx.Err() == nil {
		cause = a.memberWatch.Start(ctx)
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
