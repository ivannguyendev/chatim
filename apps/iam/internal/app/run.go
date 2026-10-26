package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/config"
	"github.com/ivannguyendev/chatim/apps/iam/internal/password"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
	"github.com/ivannguyendev/chatim/pkg/admin"
)

type Deps struct {
	Listen        func(ctx context.Context, addr string) (net.Listener, error)
	OpenStore     func(ctx context.Context, uri string, maxConns int32) (Store, error)
	HashParams    password.Params
	SchemaVersion int
}

type node struct {
	cfg    config.Config
	log    *slog.Logger
	store  Store
	admin  *admin.Server
	http   *http.Server
	health *health
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
	api, admin, health    *task
	stopAdmin, stopHealth context.CancelFunc
}

func (d Deps) withDefaults() Deps {
	if d.Listen == nil {
		d.Listen = listenTCP
	}
	if d.OpenStore == nil {
		d.OpenStore = openPostgres
	}
	if d.HashParams == (password.Params{}) {
		d.HashParams = password.DefaultParams
	}
	if d.SchemaVersion == 0 {
		d.SchemaVersion = pgstore.LatestVersion()
	}
	return d
}

func Run(ctx context.Context, cfg config.Config, log *slog.Logger, deps Deps) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid iam config: %w", err)
	}
	log.InfoContext(ctx, "starting iam", "config", cfg.LogValue())
	deps = deps.withDefaults()
	n, err := wire(ctx, cfg, log, deps)
	if err != nil {
		return err
	}
	return n.serve(ctx, deps)
}

func (n *node) serve(ctx context.Context, deps Deps) error {
	adminLis, err := deps.Listen(ctx, n.cfg.AdminAddr)
	if err != nil {
		n.store.Close()
		return err
	}
	apiLis, err := deps.Listen(ctx, n.cfg.ListenAddr)
	if err != nil {
		n.store.Close()
		return errors.Join(err, adminLis.Close())
	}
	base := context.WithoutCancel(ctx)
	adminCtx, stopAdmin := context.WithCancel(base)
	healthCtx, stopHealth := context.WithCancel(base)
	n.health.check(healthCtx)
	r := running{
		admin:      start(func() error { return n.admin.ServeListener(adminCtx, adminLis) }),
		api:        start(func() error { return n.http.Serve(apiLis) }),
		health:     start(func() error { n.watchHealth(healthCtx); return nil }),
		stopAdmin:  stopAdmin,
		stopHealth: stopHealth,
	}
	n.log.InfoContext(ctx, "iam serving", "listen", apiLis.Addr().String(), "admin", adminLis.Addr().String())
	var cause error
	select {
	case <-ctx.Done():
	case <-r.api.done:
		cause = fmt.Errorf("iam serve stopped unexpectedly: %w", r.api.err)
	case <-r.admin.done:
		cause = fmt.Errorf("admin stopped unexpectedly: %w", r.admin.err)
	}
	return n.shutdown(ctx, r, cause)
}

func (n *node) watchHealth(ctx context.Context) {
	t := time.NewTicker(n.cfg.KeyReload)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n.health.check(ctx)
		}
	}
}

func (n *node) shutdown(ctx context.Context, r running, cause error) error {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), n.cfg.ShutdownBudget)
	defer cancel()
	n.log.InfoContext(stopCtx, "iam shutting down", "component_failed", cause != nil, "budget", n.cfg.ShutdownBudget)
	n.health.stop()
	if cause == nil {
		pause(stopCtx, n.cfg.DrainDelay)
	}
	errs := []error{cause}
	if err := n.http.Shutdown(stopCtx); err != nil {
		errs = append(errs, fmt.Errorf("http shutdown: %w", err))
		_ = n.http.Close()
	}
	<-r.api.done
	if r.api.err != nil && !errors.Is(r.api.err, http.ErrServerClosed) {
		errs = append(errs, fmt.Errorf("http serve: %w", r.api.err))
	}
	r.stopHealth()
	<-r.health.done
	n.store.Close()
	r.stopAdmin()
	select {
	case <-r.admin.done:
		if r.admin.err != nil {
			errs = append(errs, r.admin.err)
		}
	case <-stopCtx.Done():
		errs = append(errs, fmt.Errorf("admin did not stop in time: %w", stopCtx.Err()))
	}
	n.log.InfoContext(stopCtx, "iam stopped")
	return errors.Join(errs...)
}

func pause(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func listenTCP(ctx context.Context, addr string) (net.Listener, error) {
	var lc net.ListenConfig
	return lc.Listen(ctx, "tcp", addr)
}
