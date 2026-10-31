package app

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/command"
	"github.com/ivannguyendev/chatim/apps/websocket/internal/config"
	"github.com/ivannguyendev/chatim/apps/websocket/internal/feed"
	"github.com/ivannguyendev/chatim/apps/websocket/internal/interest"
	"github.com/ivannguyendev/chatim/apps/websocket/internal/presence"
	"github.com/ivannguyendev/chatim/apps/websocket/internal/session"
	"github.com/ivannguyendev/chatim/apps/websocket/internal/typing"
	"github.com/ivannguyendev/chatim/pkg/coreroute"
	"github.com/ivannguyendev/chatim/pkg/iamsync"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	"github.com/ivannguyendev/chatim/pkg/metrics"
	"github.com/ivannguyendev/chatim/pkg/redact"
)

const (
	metricsSubsystem  = "websocket"
	wsPath            = "GET /ws"
	readHeaderTimeout = 5 * time.Second
	maxHeaderBytes    = 16 << 10
)

func redactedLogger(log *slog.Logger, cfg config.Config) *slog.Logger {
	return slog.New(redact.NewHandler(log.Handler(), []string{cfg.NATSURL}, []string{cfg.RedisPassword, cfg.PresenceRedisPassword}))
}

func natsConfig(cfg config.Config, deps Deps) feed.NATSConfig {
	return feed.NATSConfig{URL: cfg.NATSURL, Name: "chatim-websocket-" + cfg.NodeID, ReadyTimeout: deps.ReadyTimeout, PendingMsgs: cfg.TypingPendingMsgs}
}

func feedConfig(cfg config.Config) feed.Config {
	return feed.Config{
		EventStream:   cfg.EventStream,
		EventRoot:     cfg.EventRoot,
		PublishStream: cfg.PublishStream,
		PublishRoot:   cfg.PublishRoot,
		Replicas:      cfg.StreamReplicas,
		Workers:       cfg.FanoutWorkers,
		Queue:         cfg.FanoutQueue,
		ReplayRate:    cfg.ReplayRate,
		ConsumeBatch:  cfg.ConsumeBatch,
		ConsumeBytes:  cfg.ConsumeBytes,
		ReplayMaxAge:  cfg.ReplayMaxAge,
		JumpCooldown:  cfg.ReplayMaxAge,
	}
}

type eventRouter struct {
	rooms     *interest.Table
	publisher *interest.Publisher
}

func (r eventRouter) Event(env feed.Envelope) { r.rooms.Route(env) }

func (r eventRouter) Publish(env feed.Envelope) { r.publisher.Deliver(env) }

func (r eventRouter) Jumped(j feed.Jump) { r.rooms.Jumped(j) }

func newInterest(cfg config.Config, routes *routerHolder, log *slog.Logger) (*interest.Table, error) {
	loader, err := interest.NewCoreLoader(routes, cfg.RequestDeadline)
	if err != nil {
		return nil, err
	}
	return interest.New(interest.Config{
		Inflight:           cfg.RoomIDsInflight,
		OpenPending:        cfg.OpenPending,
		ReloadRate:         cfg.ReloadRate,
		UserConnectionsMax: cfg.UserConnectionsMax,
	}, loader, log)
}

func jwtConfig(cfg config.Config) jwtverify.Config {
	return jwtverify.Config{Issuer: cfg.JWTIssuer, Audience: cfg.JWTAudience, Leeway: cfg.JWTLeeway}
}

func sessionOptions(cfg config.Config) session.Options {
	return session.Options{
		AuthGrace:        cfg.AuthGrace,
		AuthRate:         cfg.AuthRate,
		AuthBurst:        cfg.AuthBurst,
		AuthRetryMin:     cfg.AuthRetryMin,
		AuthRetryMax:     cfg.AuthRetryMax,
		HeartbeatTimeout: cfg.HeartbeatTimeout,
		JWTLeeway:        cfg.JWTLeeway,
		WriteTimeout:     cfg.WriteTimeout,
		UnauthMax:        cfg.UnauthMax,
		FrameMax:         cfg.FrameMax,
		CommandRate:      cfg.CommandRate,
		CommandBurst:     cfg.CommandBurst,
		CommandInflight:  cfg.CommandInflight,
		SendQueue:        cfg.SendQueue,
		SendQueueBytes:   cfg.SendQueueBytes,
		TypingRate:       cfg.TypingRate,
		TypingBurst:      cfg.TypingBurst,
		PresenceWatchMax: cfg.PresenceWatchMax,
	}
}

func newTyping(cfg config.Config, rooms *interest.Table) (*typing.Service, error) {
	origin, err := typing.NewOrigin(cfg.NodeID)
	if err != nil {
		return nil, err
	}
	return typing.New(typing.Config{Origin: origin, Queue: cfg.TypingQueue}, rooms)
}

type presenceParts struct {
	raw      presence.Conn
	client   *presence.Client
	stamper  *presence.Stamper
	answerer *presence.Answerer
}

type presenceService struct {
	*presence.Stamper
	*presence.Answerer
}

func (p presenceParts) service() presenceService { return presenceService{p.stamper, p.answerer} }

func (p presenceParts) sources() []metrics.Source {
	return slices.Concat(p.stamper.Sources(), p.answerer.Sources(), p.client.Sources())
}

func presenceRedisConfig(cfg config.Config) presence.RedisConfig {
	return presence.RedisConfig{
		Addr:     cfg.PresenceRedisAddr,
		DB:       cfg.PresenceRedisDB,
		Password: cfg.PresenceRedisPassword,
		Pool:     cfg.PresenceRedisPool,
		Timeout:  cfg.PresenceRedisTimeout,
		Node:     cfg.NodeID,
	}
}

func presenceConfig(cfg config.Config, prune func(time.Time)) presence.Config {
	return presence.Config{TTL: cfg.PresenceTTL, StampEvery: cfg.PresenceStampEvery, Flush: cfg.PresenceFlush, Batch: cfg.PresenceBatch, Prune: prune}
}

func newPresence(cfg config.Config, deps Deps) (presenceParts, error) {
	open := deps.PresenceRedis
	if open == nil {
		open = func(opts *redis.Options) presence.Conn { return redis.NewClient(opts) }
	}
	conn := open(presence.RedisOptions(presenceRedisConfig(cfg)))
	client, err := presence.NewClient(conn, cfg.PresenceRedisTimeout)
	if err != nil {
		return presenceParts{}, errors.Join(err, closeConn(conn))
	}
	answerer, err := presence.NewAnswerer(cfg.PresenceCache, client)
	if err != nil {
		return presenceParts{}, errors.Join(err, client.Close())
	}
	stamper, err := presence.NewStamper(presenceConfig(cfg, answerer.Prune), client)
	if err != nil {
		return presenceParts{}, errors.Join(err, client.Close())
	}
	return presenceParts{raw: conn, client: client, stamper: stamper, answerer: answerer}, nil
}

func closeConn(conn presence.Conn) error {
	if conn == nil {
		return nil
	}
	return conn.Close()
}

func closePresence(client *presence.Client) error {
	if err := client.Close(); err != nil {
		return fmt.Errorf("close presence redis: %w", err)
	}
	return nil
}

func newSync(cfg config.Config, verifier *jwtverify.Verifier, conn presence.Conn, doer iamsync.Doer, log *slog.Logger) (*iamsync.Syncer, error) {
	list, err := iamsync.NewList(cfg.JWTLeeway)
	if err != nil {
		return nil, err
	}
	var shared iamsync.Shared
	if rc, ok := conn.(iamsync.RedisClient); ok {
		shared = iamsync.NewRedisShared(rc, cfg.JWTIssuer)
	}
	return iamsync.New(cfg.IAM, list, verifier, shared, doer, log)
}

func routerConfig(cfg config.Config, deps Deps, log *slog.Logger) coreroute.Config {
	return coreroute.Config{
		RedisAddr:     cfg.RedisAddr,
		RedisDB:       cfg.RedisDB,
		RedisPassword: cfg.RedisPassword,
		ClientName:    "chatim-websocket-" + cfg.NodeID,
		ReadyTimeout:  deps.ReadyTimeout,
		Log:           log,
		Dial:          deps.Dial,
	}
}

func newHTTPServer(ws http.Handler, log *slog.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.Handle(wsPath, ws)
	return &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}

func metricSources(ws *session.Server, exec *command.Executor) []metrics.Source {
	out := append(ws.Sources(), exec.CommandSources()...)
	for _, latency := range exec.CoreCallLatency() {
		out = append(out, metrics.Source{
			Name:      "core_call_seconds",
			Help:      "Core call latency of client commands, in seconds.",
			Labels:    map[string]string{"command": latency.Command},
			Histogram: latency.Read,
		})
	}
	return out
}

type routerHolder struct {
	current atomic.Pointer[coreroute.Router]
}

func (h *routerHolder) set(r *coreroute.Router) { h.current.Store(r) }

func (h *routerHolder) Room(roomID string) (coreroute.Conn, error) {
	r := h.current.Load()
	if r == nil {
		return coreroute.Conn{}, coreroute.ErrNoRoute
	}
	return r.Room(roomID)
}

func (h *routerHolder) Any() (coreroute.Conn, error) {
	r := h.current.Load()
	if r == nil {
		return coreroute.Conn{}, coreroute.ErrNoRoute
	}
	return r.Any()
}

func (h *routerHolder) Trigger() {
	if r := h.current.Load(); r != nil {
		r.Trigger()
	}
}
