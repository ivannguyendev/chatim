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

type runner interface {
	Run(ctx context.Context) error
}

type drainer interface {
	runner
	Close(ctx context.Context) error
}

type gate interface {
	drainer
	Running() <-chan struct{}
}

type app struct {
	cfg       config.Config
	log       *slog.Logger
	admin     *admin.Server
	grpc      *grpcserver.Server
	publisher drainer
	flusher   drainer
	router    gate
	sweeper   runner
	slots     runner
}

func prepare(ctx context.Context, cfg config.Config, cl *clients) error {
	ctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := mongostore.Bootstrap(ctx, cl.mongo.Database(cfg.MongoDB)); err != nil {
		return fmt.Errorf("bootstrap mongo database %s: %w", cfg.MongoDB, config.RedactError(err, cfg.MongoURI))
	}
	return config.RedactError(publish.EnsureStream(ctx, cl.js, cfg.Stream), cfg.NATSURL)
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
	pub, err := publish.New(cl.js, cl.redis, cfg.Publish, log)
	if err != nil {
		return nil, fmt.Errorf("wire publisher: %w", err)
	}
	fl, err := flush.New(st, cfg.Flush)
	if err != nil {
		return nil, fmt.Errorf("wire flusher: %w", err)
	}
	router, err := actor.NewRouter(st, st, fl, cids, pub, marks, cfg.Actor, log)
	if err != nil {
		return nil, fmt.Errorf("wire router: %w", err)
	}
	var sweeper *recovery.Sweeper
	slotCfg := cfg.Slot
	slotCfg.BeforeRelease = router.EvictSlots
	slotCfg.AfterLose = router.EvictSlots
	slotCfg.AfterClaim = func(ctx context.Context, slots []uint16) {
		router.EvictSlots(ctx, slots)
		sweeper.Trigger(slots)
	}
	slots, err := slot.New(cl.slots, slotCfg, log)
	if err != nil {
		return nil, fmt.Errorf("wire slot manager: %w", err)
	}
	deps := recovery.Deps{Slots: slots, Rooms: router, Msgs: st, Redis: cl.redis}
	if sweeper, err = recovery.New(deps, cfg.Recovery, log); err != nil {
		return nil, fmt.Errorf("wire recovery sweeper: %w", err)
	}
	a.publisher, a.flusher, a.router, a.sweeper, a.slots = pub, fl, router, sweeper, slots
	svc, err := grpcsrv.New(grpcsrv.Deps{Sender: router, Rooms: st, Pages: st}, log)
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
