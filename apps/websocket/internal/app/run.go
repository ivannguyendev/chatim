package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/command"
	"github.com/ivannguyendev/chatim/apps/websocket/internal/config"
	"github.com/ivannguyendev/chatim/apps/websocket/internal/session"
	"github.com/ivannguyendev/chatim/pkg/admin"
	"github.com/ivannguyendev/chatim/pkg/coreroute"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	"github.com/ivannguyendev/chatim/pkg/metrics"
)

type Deps struct {
	Dial         coreroute.Dialer
	ReadyTimeout time.Duration
	Listen       func(ctx context.Context, addr string) (net.Listener, error)
}

type node struct {
	cfg    config.Config
	log    *slog.Logger
	admin  *admin.Server
	ws     *session.Server
	routes *routerHolder
	router *coreroute.Router
	http   *http.Server
}

type tasks struct {
	admin, listener *task
}

func Run(ctx context.Context, cfg config.Config, log *slog.Logger, deps Deps) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid websocket config: %w", err)
	}
	log = redactedLogger(log, cfg)
	log.InfoContext(ctx, "starting websocket", "config", cfg.LogValue())
	n, err := wire(cfg, log)
	if err != nil {
		return err
	}
	if deps.Listen == nil {
		deps.Listen = listenTCP
	}
	return n.serve(ctx, deps)
}

func wire(cfg config.Config, log *slog.Logger) (*node, error) {
	verifier, err := jwtverify.LoadFile(cfg.JWTKeysFile, jwtConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("load jwt key set: %w", err)
	}
	routes := &routerHolder{}
	exec, err := command.New(routes, cfg.RequestDeadline)
	if err != nil {
		return nil, err
	}
	ws, err := session.New(sessionOptions(cfg), verifier, exec, log)
	if err != nil {
		return nil, err
	}
	handler, err := metrics.Handler(metricsSubsystem, metricSources(ws, exec))
	if err != nil {
		return nil, err
	}
	return &node{
		cfg:    cfg,
		log:    log,
		admin:  admin.New(admin.Config{Addr: cfg.AdminAddr, Metrics: handler}, log),
		ws:     ws,
		routes: routes,
		http:   newHTTPServer(ws, log),
	}, nil
}

func (n *node) serve(ctx context.Context, deps Deps) error {
	adminLis, err := deps.Listen(ctx, n.cfg.AdminAddr)
	if err != nil {
		return err
	}
	sup := newSupervisor(ctx)
	t := tasks{admin: sup.start("admin", func(c context.Context) error { return n.admin.ServeListener(c, adminLis) })}
	cause := n.openRouter(ctx, deps)
	var wsLis net.Listener
	if cause == nil && ctx.Err() == nil {
		wsLis, cause = deps.Listen(ctx, n.cfg.ListenAddr)
	}
	if ctx.Err() != nil {
		return n.abandon(ctx, sup, t)
	}
	if cause != nil {
		return errors.Join(cause, n.abandon(ctx, sup, t))
	}
	t.listener = sup.start("websocket listener", func(c context.Context) error { return n.serveListener(c, wsLis) })
	n.admin.SetReady(true)
	n.log.InfoContext(ctx, "websocket ready", "listen", wsLis.Addr().String(), "admin", adminLis.Addr().String())
	return n.shutdown(ctx, sup, t, sup.wait(ctx))
}

func (n *node) openRouter(ctx context.Context, deps Deps) error {
	life, cancel := context.WithCancel(context.WithoutCancel(ctx))
	abort := context.AfterFunc(ctx, cancel)
	r, err := coreroute.Open(life, routerConfig(n.cfg, deps, n.log))
	abort()
	if err != nil {
		cancel()
		return fmt.Errorf("open core router: %w", err)
	}
	n.router = r
	n.routes.set(r)
	return nil
}

func (n *node) abandon(ctx context.Context, sup *supervisor, t tasks) error {
	sup.stopping.Store(true)
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), n.cfg.ShutdownBudget)
	defer cancel()
	var err error
	if n.router != nil {
		err = n.router.Close()
	}
	return errors.Join(err, t.admin.stop(stopCtx))
}

func (n *node) serveListener(ctx context.Context, lis net.Listener) error {
	served := make(chan error, 1)
	go func() { served <- n.http.Serve(lis) }()
	select {
	case err := <-served:
		_ = n.http.Close()
		return fmt.Errorf("websocket serve %s: %w", lis.Addr(), err)
	case <-ctx.Done():
	}
	graceCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.CloseTimeout)
	defer cancel()
	if err := n.http.Shutdown(graceCtx); err != nil {
		_ = n.http.Close()
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("websocket serve: %w", err)
	}
	return nil
}

func listenTCP(ctx context.Context, addr string) (net.Listener, error) {
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	return lis, nil
}
