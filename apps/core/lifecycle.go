package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

const startPoll = time.Millisecond

type tasks struct {
	admin, publisher, flusher, router, sweeper, slots, grpc *task
}

func run(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	log.InfoContext(ctx, "starting core", "config", cfg)
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

func (a *app) serve(ctx context.Context, adminLis, grpcLis net.Listener) error {
	sup := newSupervisor(ctx, 7)
	t := tasks{
		admin:     sup.start("admin", func(c context.Context) error { return a.admin.ServeListener(c, adminLis) }),
		publisher: sup.start("publisher", a.publisher.Run),
		flusher:   sup.start("flusher", a.flusher.Run),
		router:    sup.start("router", a.router.Run),
		sweeper:   sup.start("sweeper", a.sweeper.Run),
		slots:     sup.start("slot manager", a.slots.Run),
	}
	cause := a.awaitRouter(ctx, sup)
	t.grpc = sup.start("grpc", func(c context.Context) error { return a.grpc.ServeListener(c, grpcLis) })
	if cause == nil && ctx.Err() == nil {
		a.admin.SetReady(true)
		a.log.InfoContext(ctx, "core ready", "grpc", grpcLis.Addr().String(), "admin", adminLis.Addr().String())
		cause = sup.wait(ctx)
	}
	return a.shutdown(ctx, sup, t, cause)
}

func (a *app) awaitRouter(ctx context.Context, sup *supervisor) error {
	tick := time.NewTicker(startPoll)
	defer tick.Stop()
	for !a.router.Started() {
		select {
		case err := <-sup.failed:
			return err
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
	return nil
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
