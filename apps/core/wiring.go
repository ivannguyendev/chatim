package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventmark"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/reconcile"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/metrics"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/slot"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/flush"
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
	cfg         config.Config
	log         *slog.Logger
	admin       *admin.Server
	grpc        *grpcserver.Server
	publisher   drainer
	flusher     drainer
	cidBatch    drainer
	router      gate
	slots       runner
	workers     drainer
	reconciler  drainer
	memberWatch memberWatcher
}

func prepare(ctx context.Context, cfg config.Config, cl *clients) error {
	ctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := mongostore.Bootstrap(ctx, cl.mongo.Database(cfg.MongoDB)); err != nil {
		return fmt.Errorf("bootstrap mongo database %s: %w", cfg.MongoDB, config.RedactError(err, cfg.MongoURI))
	}
	if err := publish.EnsureStream(ctx, cl.js, cfg.Stream); err != nil {
		return config.RedactError(err, cfg.NATSURL)
	}
	return config.RedactError(work.EnsureStream(ctx, cl.js, cfg.Work), cfg.NATSURL)
}

func wire(cfg config.Config, cl *clients, log *slog.Logger) (*app, error) {
	st := mongostore.New(cl.mongo.Database(cfg.MongoDB), mongostore.Options{})
	a := &app{cfg: cfg, log: log}
	cids, err := dedupe.New(cl.dedupe, cfg.Dedupe, log)
	if err != nil {
		return nil, fmt.Errorf("wire cid dedupe: %w", err)
	}
	batch, err := dedupe.NewBatcher(cids, cfg.CIDBatch, log)
	if err != nil {
		return nil, fmt.Errorf("wire cid batcher: %w", err)
	}
	marks, err := eventmark.New(cl.dedupe, cfg.AckMarks, log)
	if err != nil {
		return nil, fmt.Errorf("wire event ack marks: %w", err)
	}
	pub, err := publish.New(cl.js, cfg.Publish, log, publish.WithAckMarks(marks), publish.WithCounters(cl.pubCounters))
	if err != nil {
		return nil, fmt.Errorf("wire publisher: %w", err)
	}
	fl, err := flush.New(st, cfg.Flush)
	if err != nil {
		return nil, fmt.Errorf("wire flusher: %w", err)
	}
	router, err := actor.NewRouter(st, st, fl, batch, pub, cfg.Actor, log)
	if err != nil {
		return nil, fmt.Errorf("wire router: %w", err)
	}
	watch, err := wireMemberWatch(cfg, cl.nats, router, log)
	if err != nil {
		return nil, err
	}
	a.memberWatch = watch
	slotCfg := cfg.Slot
	slotCfg.BeforeRelease = router.EvictSlots
	slotCfg.AfterLose = router.EvictSlots
	slotCfg.AfterClaim = router.EvictSlots
	slots, err := slot.New(cl.slots, slotCfg, log)
	if err != nil {
		return nil, fmt.Errorf("wire slot manager: %w", err)
	}
	w := cfg.Work
	timers, err := work.NewTimers(cl.js, w.Name, w.SubjectRoot, w.Partitions, cfg.MemberCountCheckDelay, work.WithTimerLogger(log))
	if err != nil {
		return nil, fmt.Errorf("wire member count timers: %w", err)
	}
	fx, err := wireEffects(cfg, cl, st, marks, slots, timers, log)
	if err != nil {
		return nil, err
	}
	a.workers = fx.workers
	var rec *reconcile.Reconciler
	if cfg.ReconcileEnabled {
		rec, err = reconcile.New(reconcile.Deps{
			Feed: mongostore.NewFeed(cl.mongo.Database(cfg.MongoDB)), Owner: slots, JS: cl.reconcileJS,
		}, cfg.Reconcile, log)
		if err != nil {
			return nil, fmt.Errorf("wire reconciler: %w", err)
		}
		a.reconciler = rec
	}
	a.publisher, a.flusher, a.cidBatch, a.router, a.slots = pub, fl, batch, router, slots
	svc, err := wireService(serviceDeps{store: st, router: router, pub: pub, cidBatch: batch, timers: timers, cfg: cfg, log: log})
	if err != nil {
		return nil, err
	}
	limiter := resilience.NewLimiter(cfg.MaxInflight, cfg.QueueWait)
	a.grpc = grpcserver.New(grpcserver.Config{
		Addr:            cfg.GRPCAddr,
		ShutdownTimeout: cfg.GRPCShutdown,
		RequestDeadline: cfg.RequestDeadline,
		SlowRPC:         cfg.SlowRPC,
		Limiter:         limiter,
	}, log)
	chatimv1.RegisterCoreServiceServer(a.grpc, svc)
	p := probes{
		drops:           cl.pubCounters.Drops,
		router:          router.Stats,
		cidDegraded:     cids.Degraded,
		markDegraded:    marks.Degraded,
		cidDropped:      batch.Dropped,
		loadShed:        limiter.Rejected,
		oplogWindow:     oplogWindowSeconds(cl.mongo),
		workers:         fx.workers.Stats,
		effectCounts:    fx.counters(),
		counterRepairs:  fx.counterRepairs(),
		memberForgets:   watch.Forgets,
		memberMalformed: watch.Malformed,
	}
	if rec != nil {
		p.reconcile = rec.Stats
	}
	handler, err := metrics.Handler(metricSources(p))
	if err != nil {
		return nil, fmt.Errorf("wire metrics: %w", err)
	}
	a.admin = admin.New(admin.Config{Addr: cfg.AdminAddr, ShutdownTimeout: config.CloseTimeout, Metrics: handler}, log)
	return a, nil
}
