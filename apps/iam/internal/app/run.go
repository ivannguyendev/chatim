package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/api"
	"github.com/ivannguyendev/chatim/apps/iam/internal/config"
	"github.com/ivannguyendev/chatim/apps/iam/internal/password"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
	"github.com/ivannguyendev/chatim/pkg/admin"
)

type Deps struct {
	Listen        func(ctx context.Context, addr string) (net.Listener, error)
	OpenStore     func(ctx context.Context, uri string, opts pgstore.Options) (Store, error)
	OpenListener  func(ctx context.Context, uri string) (store.Listener, error)
	MemoryLimit   func() int64
	HashParams    password.Params
	SchemaVersion int
}

type port struct {
	addr   string
	server *http.Server
	lis    net.Listener
	task   *task
}

type node struct {
	cfg           config.Config
	log           *slog.Logger
	store         Store
	admin         *admin.Server
	ports         []*port
	health        *health
	internalStats *api.InternalStats
	dir           *directoryParts
	cleanup       cleanupStats
	keys          keyStats
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
	admin, health, directory             *task
	stopAdmin, stopHealth, stopDirectory context.CancelFunc
	exited                               chan *port
}

func (d Deps) withDefaults() Deps {
	if d.Listen == nil {
		d.Listen = listenTCP
	}
	if d.OpenStore == nil {
		d.OpenStore = openPostgres
	}
	if d.OpenListener == nil {
		d.OpenListener = openListener
	}
	if d.MemoryLimit == nil {
		d.MemoryLimit = currentMemoryLimit
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

func (n *node) listen(ctx context.Context, deps Deps) (net.Listener, error) {
	adminLis, err := deps.Listen(ctx, n.cfg.AdminAddr)
	if err != nil {
		return nil, err
	}
	for _, p := range n.ports {
		if p.lis, err = deps.Listen(ctx, p.addr); err != nil {
			errs := []error{err, adminLis.Close()}
			for _, open := range n.ports {
				if open.lis != nil {
					errs = append(errs, open.lis.Close())
				}
			}
			return nil, errors.Join(errs...)
		}
	}
	return adminLis, nil
}

func (n *node) serve(ctx context.Context, deps Deps) error {
	adminLis, err := n.listen(ctx, deps)
	if err != nil {
		n.store.Close()
		return err
	}
	base := context.WithoutCancel(ctx)
	adminCtx, stopAdmin := context.WithCancel(base)
	healthCtx, stopHealth := context.WithCancel(base)
	directoryCtx, stopDirectory := context.WithCancel(base)
	r := running{
		admin:         start(func() error { return n.admin.ServeListener(adminCtx, adminLis) }),
		health:        start(func() error { n.watchHealth(healthCtx); return nil }),
		directory:     start(func() error { return n.dir.run(directoryCtx, n.health.trigger) }),
		stopAdmin:     stopAdmin,
		stopHealth:    stopHealth,
		stopDirectory: stopDirectory,
		exited:        make(chan *port, len(n.ports)),
	}
	addrs := []any{"admin", adminLis.Addr().String()}
	for _, p := range n.ports {
		p.task = start(func() error { return p.server.Serve(p.lis) })
		go func() {
			<-p.task.done
			r.exited <- p
		}()
		addrs = append(addrs, p.addr, p.lis.Addr().String())
	}
	n.log.InfoContext(ctx, "iam serving", addrs...)
	var cause error
	select {
	case <-ctx.Done():
	case p := <-r.exited:
		cause = fmt.Errorf("iam port %s stopped unexpectedly: %w", p.addr, p.task.err)
	case <-r.admin.done:
		cause = fmt.Errorf("admin stopped unexpectedly: %w", r.admin.err)
	}
	return n.shutdown(ctx, r, cause)
}

func (n *node) watchHealth(ctx context.Context) {
	t := time.NewTicker(n.cfg.KeyReload)
	defer t.Stop()
	n.health.check(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-n.health.wake:
			n.health.check(ctx)
		case <-t.C:
			n.activateDueKey(ctx)
			n.health.check(ctx)
			n.cleanExpired(ctx)
		}
	}
}

func (n *node) activateDueKey(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, storeCallTimeout)
	defer cancel()
	audit := store.AuditEvent{At: time.Now(), Actor: "iam", Action: "key_activate"}
	kid, err := n.store.ActivateDueSigningKey(ctx, n.cfg.KeyActivateLead, audit)
	if err != nil {
		n.log.WarnContext(ctx, "signing key activation failed", "err", err)
		return
	}
	if kid != "" {
		n.keys.activated.Add(1)
		n.log.InfoContext(ctx, "signing key activated", "kid", kid)
	}
}

func (n *node) cleanExpired(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, storeCallTimeout)
	defer cancel()
	removed, err := n.store.DeleteExpiredRevocations(ctx)
	if err != nil {
		if n.cleanup.errors.Add(1) == 1 {
			n.log.WarnContext(ctx, "revocation cleanup failed", "err", err)
		}
		return
	}
	n.cleanup.removed.Add(removed)
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
	for _, p := range n.ports {
		if err := p.server.Shutdown(stopCtx); err != nil {
			errs = append(errs, fmt.Errorf("http shutdown %s: %w", p.addr, err))
			_ = p.server.Close()
		}
	}
	for _, p := range n.ports {
		<-p.task.done
		if p.task.err != nil && !errors.Is(p.task.err, http.ErrServerClosed) {
			errs = append(errs, fmt.Errorf("http serve %s: %w", p.addr, p.task.err))
		}
	}
	r.stopDirectory()
	<-r.directory.done
	if r.directory.err != nil {
		errs = append(errs, fmt.Errorf("directory: %w", r.directory.err))
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
