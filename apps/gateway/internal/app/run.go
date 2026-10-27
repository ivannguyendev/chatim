package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"

	"github.com/ivannguyendev/chatim/apps/gateway/internal/config"
	"github.com/ivannguyendev/chatim/pkg/admin"
	"github.com/ivannguyendev/chatim/pkg/iamsync"
)

type Deps struct {
	Listen      func(ctx context.Context, addr string) (net.Listener, error)
	DialOptions []grpc.DialOption
	IAMDoer     iamsync.Doer
}

type node struct {
	cfg   config.Config
	log   *slog.Logger
	conn  *grpc.ClientConn
	sync  *iamsync.Syncer
	redis *redis.Client
	admin *admin.Server
	http  *http.Server
	ready *readiness
}

type task struct {
	done chan struct{}
	err  error
}

func start(run func() error) *task {
	t := &task{done: make(chan struct{})}
	go func() {
		defer close(t.done)
		t.err = run()
	}()
	return t
}

type running struct {
	api, admin, watch, sync        *task
	stopAdmin, stopWatch, stopSync context.CancelFunc
}

func Run(ctx context.Context, cfg config.Config, log *slog.Logger, deps Deps) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid gateway config: %w", err)
	}
	log.InfoContext(ctx, "starting gateway", "config", cfg.LogValue())
	n, err := wire(cfg, log, deps)
	if err != nil {
		return err
	}
	if deps.Listen == nil {
		deps.Listen = listenTCP
	}
	return n.serve(ctx, deps)
}

func (n *node) serve(ctx context.Context, deps Deps) error {
	adminLis, err := deps.Listen(ctx, n.cfg.AdminAddr)
	if err != nil {
		return errors.Join(err, n.conn.Close())
	}
	apiLis, err := deps.Listen(ctx, n.cfg.ListenAddr)
	if err != nil {
		return errors.Join(err, adminLis.Close(), n.conn.Close())
	}
	base := context.WithoutCancel(ctx)
	adminCtx, stopAdmin := context.WithCancel(base)
	watchCtx, stopWatch := context.WithCancel(base)
	syncCtx, stopSync := context.WithCancel(base)
	r := running{
		admin:     start(func() error { return n.admin.ServeListener(adminCtx, adminLis) }),
		sync:      start(func() error { n.runSync(syncCtx); return nil }),
		api:       start(func() error { return n.http.Serve(apiLis) }),
		watch:     start(func() error { n.ready.watch(watchCtx, n.conn); return nil }),
		stopAdmin: stopAdmin,
		stopWatch: stopWatch,
		stopSync:  stopSync,
	}
	n.log.InfoContext(ctx, "gateway serving", "listen", apiLis.Addr().String(), "admin", adminLis.Addr().String())
	var cause error
	select {
	case <-ctx.Done():
	case <-r.api.done:
		cause = fmt.Errorf("gateway serve stopped unexpectedly: %w", r.api.err)
	case <-r.admin.done:
		cause = fmt.Errorf("admin stopped unexpectedly: %w", r.admin.err)
	}
	return n.shutdown(ctx, r, cause)
}

func (n *node) shutdown(ctx context.Context, r running, cause error) error {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), n.cfg.ShutdownBudget)
	defer cancel()
	n.log.InfoContext(stopCtx, "gateway shutting down", "component_failed", cause != nil, "budget", n.cfg.ShutdownBudget)
	n.ready.stop()
	if cause == nil {
		pause(stopCtx, n.cfg.DrainDelay)
	}
	var errs []error
	if err := n.http.Shutdown(stopCtx); err != nil {
		errs = append(errs, fmt.Errorf("http shutdown: %w", err))
		_ = n.http.Close()
	}
	<-r.api.done
	if r.api.err != nil && !errors.Is(r.api.err, http.ErrServerClosed) {
		errs = append(errs, fmt.Errorf("http serve: %w", r.api.err))
	}
	r.stopWatch()
	<-r.watch.done
	r.stopSync()
	<-r.sync.done
	if n.redis != nil {
		if err := n.redis.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close shared redis: %w", err))
		}
	}
	if err := n.conn.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close core connection: %w", err))
	}
	r.stopAdmin()
	select {
	case <-r.admin.done:
		if r.admin.err != nil {
			errs = append(errs, r.admin.err)
		}
	case <-stopCtx.Done():
		errs = append(errs, fmt.Errorf("admin did not stop in time: %w", stopCtx.Err()))
	}
	n.log.InfoContext(stopCtx, "gateway stopped", "failed_steps", len(errs))
	return errors.Join(cause, errors.Join(errs...))
}

func (n *node) runSync(ctx context.Context) {
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		select {
		case <-n.sync.Booted():
			n.ready.iamBooted(ctx)
		case <-ctx.Done():
		}
	}()
	n.sync.Run(ctx)
	<-watched
}

func pause(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

func listenTCP(ctx context.Context, addr string) (net.Listener, error) {
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	return lis, nil
}
