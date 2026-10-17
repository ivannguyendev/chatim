package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
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

type node struct {
	cfg         config.Config
	log         *slog.Logger
	admin       *admin.Server
	grpc        *grpcserver.Server
	publisher   drainer
	stateClears drainer
	flusher     drainer
	cidBatch    drainer
	router      gate
	slots       runner
	timers      drainer
	projections drainer
	sweeper     drainer
	mention     drainer
	setups      []preparer
	watches     liveWatcher
}

func prepare(ctx context.Context, cfg config.Config, cl *clients, setups ...preparer) error {
	ctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := mongostore.Bootstrap(ctx, cl.mongo.Database(cfg.MongoDB)); err != nil {
		return fmt.Errorf("bootstrap mongo database %s: %w", cfg.MongoDB, config.RedactError(err, cfg.MongoURI))
	}
	if err := publish.EnsureStream(ctx, cl.js, cfg.Stream); err != nil {
		return config.RedactError(err, cfg.NATSURL)
	}
	if err := projection.EnsureConsumer(ctx, cl.js, cfg.Projection); err != nil {
		return config.RedactError(err, cfg.NATSURL)
	}
	if err := timer.EnsureStream(ctx, cl.js, cfg.Timer); err != nil {
		return config.RedactError(err, cfg.NATSURL)
	}
	for _, s := range setups {
		if err := s.Prepare(ctx); err != nil {
			return config.RedactError(config.RedactError(err, cfg.NATSURL), cfg.MongoURI)
		}
	}
	return nil
}

func wire(cfg config.Config, cl *clients, log *slog.Logger) (*node, error) {
	st := mongostore.New(cl.mongo.Database(cfg.MongoDB), mongostore.Options{})
	a := &node{cfg: cfg, log: log}
	cids, err := dedupe.New(cl.dedupe, cfg.Dedupe, log)
	if err != nil {
		return nil, fmt.Errorf("wire cid dedupe: %w", err)
	}
	batch, err := dedupe.NewBatcher(cids, cfg.CIDBatch, log)
	if err != nil {
		return nil, fmt.Errorf("wire cid batcher: %w", err)
	}
	clears, err := wireStateClears(cfg, st, log)
	if err != nil {
		return nil, err
	}
	pub, err := publish.New(cl.js, cfg.Publish, log, publish.WithStateClears(clears), publish.WithCounters(cl.pubCounters))
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
	caches, err := wireRoomCaches(cfg.RoomList)
	if err != nil {
		return nil, err
	}
	roomWatch, err := wireRoomWatch(cfg, cl.nats, caches.info, log)
	if err != nil {
		return nil, err
	}
	slotCfg := cfg.Slot
	slotCfg.BeforeRelease = router.EvictSlots
	slotCfg.AfterLose = router.EvictSlots
	slotCfg.AfterClaim = router.EvictSlots
	slots, err := slot.New(cl.slots, slotCfg, log)
	if err != nil {
		return nil, fmt.Errorf("wire slot manager: %w", err)
	}
	ts, err := wireTimers(cfg, cl, st, pub, log)
	if err != nil {
		return nil, err
	}
	a.timers = ts.consumer
	projections, err := wireProjections(cfg, cl, st, slots, pub, ts.messages, log)
	if err != nil {
		return nil, err
	}
	sweeper, err := wireSweeper(cfg, cl, st, slots, log)
	if err != nil {
		return nil, err
	}
	a.publisher, a.stateClears, a.flusher, a.cidBatch, a.router, a.slots = pub, clears, fl, batch, router, slots
	a.projections, a.sweeper = projections, sweeper
	svc, err := wireService(serviceDeps{store: st, router: router, pub: pub, cidBatch: batch, timers: ts.members, msgTimers: ts.messages, rooms: caches, cfg: cfg, log: log})
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
	mention, err := wireMention(cfg, cl, st, caches, svc, log)
	if err != nil {
		return nil, err
	}
	p := probes{
		drops:           cl.pubCounters.Drops,
		router:          router.Stats,
		cidDegraded:     cids.Degraded,
		cidDropped:      batch.Dropped,
		loadShed:        limiter.Rejected,
		oplogWindow:     oplogWindowSeconds(cl.mongo),
		counterRepairs:  ts.counterRepairs(),
		timerFailures:   ts.consumer.Failures,
		timerDropped:    ts.consumer.Dropped,
		memberForgets:   watch.Forgets,
		memberMalformed: watch.Malformed,
		roomCache:       caches.probes(roomWatch),
		events:          eventProbes{clears: clears, projection: projections, sweeper: sweeper},
		mention:         a.attachMention(mention, liveWatchers{watch, roomWatch}),
	}
	handler, err := metrics.Handler(metricSources(p))
	if err != nil {
		return nil, fmt.Errorf("wire metrics: %w", err)
	}
	a.admin = admin.New(admin.Config{Addr: cfg.AdminAddr, ShutdownTimeout: config.CloseTimeout, Metrics: handler}, log)
	return a, nil
}
