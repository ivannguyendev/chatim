package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/command"
	"github.com/ivannguyendev/chatim/apps/websocket/internal/config"
	"github.com/ivannguyendev/chatim/apps/websocket/internal/feed"
	"github.com/ivannguyendev/chatim/apps/websocket/internal/interest"
	"github.com/ivannguyendev/chatim/apps/websocket/internal/session"
	"github.com/ivannguyendev/chatim/apps/websocket/internal/typing"
	"github.com/ivannguyendev/chatim/pkg/admin"
	"github.com/ivannguyendev/chatim/pkg/coreroute"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	"github.com/ivannguyendev/chatim/pkg/metrics"
)

type Deps struct {
	Dial         coreroute.Dialer
	ReadyTimeout time.Duration
	Listen       func(ctx context.Context, addr string) (net.Listener, error)
	NATS         func(ctx context.Context) (feed.Broker, error)
}

type node struct {
	cfg    config.Config
	log    *slog.Logger
	admin  *admin.Server
	ws     *session.Server
	routes *routerHolder
	router *coreroute.Router
	feed   *feed.Feed
	rooms  *interest.Table
	typing *typing.Service
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
	rooms, err := newInterest(cfg, routes, log)
	if err != nil {
		return nil, err
	}
	n, err := wireServers(cfg, log, verifier, exec, rooms)
	if err != nil {
		rooms.Close()
		return nil, err
	}
	n.routes = routes
	return n, nil
}

func wireServers(cfg config.Config, log *slog.Logger, verifier *jwtverify.Verifier, exec *command.Executor, rooms *interest.Table) (*node, error) {
	typist, err := newTyping(cfg, rooms)
	if err != nil {
		return nil, err
	}
	ws, err := session.New(sessionOptions(cfg), verifier, exec, rooms, typist, log)
	if err != nil {
		return nil, err
	}
	publisher := interest.NewPublisher(rooms)
	fd, err := feed.New(feedConfig(cfg), eventRouter{rooms, publisher}, log)
	if err != nil {
		return nil, err
	}
	handler, err := metrics.Handler(metricsSubsystem, slices.Concat(metricSources(ws, exec), fd.Sources(), rooms.Sources(), publisher.Sources(), typist.Sources()))
	if err != nil {
		return nil, err
	}
	return &node{
		cfg:    cfg,
		log:    log,
		admin:  admin.New(admin.Config{Addr: cfg.AdminAddr, Metrics: handler}, log),
		ws:     ws,
		feed:   fd,
		rooms:  rooms,
		typing: typist,
		http:   newHTTPServer(ws, log),
	}, nil
}

func (n *node) serve(ctx context.Context, deps Deps) error {
	adminLis, err := deps.Listen(ctx, n.cfg.AdminAddr)
	if err != nil {
		closeListener(adminLis)
		return err
	}
	sup := newSupervisor(ctx)
	t := tasks{admin: sup.start("admin", func(c context.Context) error { return n.admin.ServeListener(c, adminLis) })}
	wsLis, cause := n.openRoutes(ctx, deps)
	if cause != nil || ctx.Err() != nil {
		closeListener(wsLis)
		if ctx.Err() != nil {
			cause = nil
		}
		return errors.Join(cause, n.abandon(ctx, sup, t))
	}
	t.listener = sup.start("websocket listener", func(c context.Context) error { return n.serveListener(c, wsLis) })
	select {
	case failed := <-sup.failed:
		return n.shutdown(ctx, sup, t, failed)
	default:
	}
	n.admin.SetReady(true)
	n.log.InfoContext(ctx, "websocket ready", "listen", wsLis.Addr().String(), "admin", adminLis.Addr().String())
	return n.shutdown(ctx, sup, t, sup.wait(ctx))
}

func (n *node) openRoutes(ctx context.Context, deps Deps) (net.Listener, error) {
	if err := n.openFeed(ctx, deps); err != nil || ctx.Err() != nil {
		return nil, err
	}
	if err := n.openRouter(ctx, deps); err != nil || ctx.Err() != nil {
		return nil, err
	}
	return deps.Listen(ctx, n.cfg.ListenAddr)
}

func closeListener(lis net.Listener) {
	if lis != nil {
		_ = lis.Close()
	}
}

func (n *node) openFeed(ctx context.Context, deps Deps) error {
	dial := deps.NATS
	if dial == nil {
		dial = func(c context.Context) (feed.Broker, error) { return feed.Dial(c, natsConfig(n.cfg, deps), n.log) }
	}
	b, err := dial(ctx)
	if err != nil {
		return fmt.Errorf("open nats: %w", err)
	}
	if err := n.feed.Start(ctx, b); err != nil {
		return fmt.Errorf("start feed: %w", err)
	}
	if err := n.typing.Start(b); err != nil {
		return fmt.Errorf("start typing relay: %w", err)
	}
	return nil
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
	err := errors.Join(n.typing.Stop(), n.feed.Stop(stopCtx))
	n.rooms.Close()
	if n.router != nil {
		err = errors.Join(err, n.router.Close())
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
