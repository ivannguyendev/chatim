package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/flush"
	"github.com/ivannguyendev/chatim/apps/core/internal/grpcsrv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/recovery"
	"github.com/ivannguyendev/chatim/apps/core/internal/slot"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/pkg/admin"
	"github.com/ivannguyendev/chatim/pkg/grpcserver"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/resilience"
)

type app struct {
	cfg       config.Config
	log       *slog.Logger
	admin     *admin.Server
	grpc      *grpcserver.Server
	publisher *publish.Publisher
	flusher   *flush.Flusher
	router    *actor.Router
	sweeper   *recovery.Sweeper
	slots     *slot.Manager
}

func prepare(ctx context.Context, cfg config.Config, cl *clients) error {
	ctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := mongostore.Bootstrap(ctx, cl.mongo.Database(cfg.MongoDB)); err != nil {
		return fmt.Errorf("bootstrap mongo database %s: %w", cfg.MongoDB, err)
	}
	return publish.EnsureStream(ctx, cl.js, cfg.Stream)
}

func wire(cfg config.Config, cl *clients, log *slog.Logger) (*app, error) {
	st := mongostore.New(cl.mongo.Database(cfg.MongoDB), mongostore.Options{})
	a := &app{cfg: cfg, log: log}
	cids, err := dedupe.New(cl.redis, cfg.Dedupe, log)
	if err != nil {
		return nil, fmt.Errorf("wire cid dedupe: %w", err)
	}
	marks, err := publish.NewActivityMarks(cl.redis, cfg.Marks, log)
	if err != nil {
		return nil, fmt.Errorf("wire activity marks: %w", err)
	}
	if a.publisher, err = publish.New(cl.js, cl.redis, cfg.Publish, log); err != nil {
		return nil, fmt.Errorf("wire publisher: %w", err)
	}
	if a.flusher, err = flush.New(st, cfg.Flush); err != nil {
		return nil, fmt.Errorf("wire flusher: %w", err)
	}
	if a.router, err = actor.NewRouter(st, st, a.flusher, cids, a.publisher, marks, cfg.Actor, log); err != nil {
		return nil, fmt.Errorf("wire router: %w", err)
	}
	slotCfg := cfg.Slot
	slotCfg.BeforeRelease = a.router.EvictSlots
	slotCfg.AfterLose = a.router.EvictSlots
	slotCfg.AfterClaim = a.afterClaim
	if a.slots, err = slot.New(cl.redis, slotCfg, log); err != nil {
		return nil, fmt.Errorf("wire slot manager: %w", err)
	}
	deps := recovery.Deps{Slots: a.slots, Rooms: a.router, Msgs: st, Redis: cl.redis}
	if a.sweeper, err = recovery.New(deps, cfg.Recovery, log); err != nil {
		return nil, fmt.Errorf("wire recovery sweeper: %w", err)
	}
	svc, err := grpcsrv.New(grpcsrv.Deps{Sender: a.router, Rooms: st, Pages: st}, log)
	if err != nil {
		return nil, fmt.Errorf("wire core service: %w", err)
	}
	a.grpc = grpcserver.New(grpcserver.Config{
		Addr:            cfg.GRPCAddr,
		ShutdownTimeout: cfg.GRPCShutdown,
		RequestDeadline: cfg.RequestDeadline,
		Limiter:         resilience.NewLimiter(cfg.MaxInflight, cfg.QueueWait),
	}, log)
	chatimv1.RegisterCoreServiceServer(a.grpc, svc)
	a.admin = admin.New(admin.Config{Addr: cfg.AdminAddr, ShutdownTimeout: config.CloseTimeout}, log)
	return a, nil
}

func (a *app) afterClaim(ctx context.Context, slots []uint16) {
	a.router.EvictSlots(ctx, slots)
	a.sweeper.Trigger(slots)
}
