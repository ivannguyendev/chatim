package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"

	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/gateway/internal/api"
	"github.com/ivannguyendev/chatim/apps/gateway/internal/config"
	"github.com/ivannguyendev/chatim/pkg/auth"
	"github.com/ivannguyendev/chatim/pkg/chatrpc"
	"github.com/ivannguyendev/chatim/pkg/core/admin"
	"github.com/ivannguyendev/chatim/pkg/core/metrics"
	"github.com/ivannguyendev/chatim/pkg/core/userlimit"
)

const (
	metricsNamespace = "chatim"
	metricsSubsystem = "gateway"
)

func wire(ctx context.Context, cfg config.Config, log *slog.Logger, deps Deps) (*node, error) {
	verifier, err := auth.LoadVerifier(cfg.JWTKeysFile, auth.VerifierConfig{Issuer: cfg.JWTIssuer, Audience: cfg.JWTAudience, Leeway: cfg.JWTLeeway})
	if err != nil {
		return nil, fmt.Errorf("load jwt key set: %w", err)
	}
	router, err := openRouter(ctx, cfg, log, deps)
	if err != nil {
		return nil, err
	}
	shared := sharedRedis(cfg)
	n, err := wireServers(cfg, log, verifier, router, shared, deps.IdentityDoer)
	if err != nil {
		_ = router.Close()
		if shared != nil {
			_ = shared.Close()
		}
		return nil, err
	}
	return n, nil
}

func openRouter(ctx context.Context, cfg config.Config, log *slog.Logger, deps Deps) (*chatrpc.Router, error) {
	life, cancel := context.WithCancel(context.WithoutCancel(ctx))
	abort := context.AfterFunc(ctx, cancel)
	r, err := chatrpc.OpenRouter(life, chatrpc.RouterConfig{
		RedisAddr:     cfg.Redis.Addr,
		RedisDB:       cfg.Redis.DB,
		RedisPassword: cfg.Redis.Password,
		ClientName:    "chatim-gateway",
		ReadyTimeout:  deps.ReadyTimeout,
		Log:           log,
	})
	abort()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open chat router: %w", err)
	}
	return r, nil
}

func sharedRedis(cfg config.Config) *redis.Client {
	if cfg.SharedRedis.Addr == "" {
		return nil
	}
	r := cfg.SharedRedis
	return redis.NewClient(&redis.Options{
		Addr: r.Addr, DB: r.DB, Password: r.Password, ClientName: "chatim-gateway-identity-sync",
		DialTimeout: r.Timeout, ReadTimeout: r.Timeout, WriteTimeout: r.Timeout, PoolSize: 2,
		MaxRetries: 1, DisableIdentity: true, ContextTimeoutEnabled: true,
	})
}

func wireServers(cfg config.Config, log *slog.Logger, verifier *auth.Verifier, router *chatrpc.Router, shared *redis.Client, doer auth.Doer) (*node, error) {
	list, err := auth.NewNodeRevocationList(cfg.JWTLeeway)
	if err != nil {
		return nil, err
	}
	var copyStore auth.Shared
	if shared != nil {
		copyStore = auth.NewRedisShared(shared, cfg.JWTIssuer)
	}
	sync, err := auth.NewSyncer(cfg.Identity, list, verifier, copyStore, doer, log)
	if err != nil {
		return nil, err
	}
	fwd, err := chatrpc.NewForwarder(chatrpc.ForwarderOptions{
		Methods:  chatrpc.Request(),
		Deadline: cfg.RequestDeadline,
		Route: func(chatrpc.Command, proto.Message) (chatrpc.Clients, error) {
			c, err := router.Any()
			return c.Clients, err
		},
		OnUnavailable: router.Trigger,
	})
	if err != nil {
		return nil, err
	}
	gate, err := userlimit.NewGate(cfg.InflightMax)
	if err != nil {
		return nil, err
	}
	users, err := userlimit.NewUsers(userlimit.UserConfig{Rate: cfg.UserRate, Burst: cfg.UserBurst, Inflight: cfg.UserInflight, Capacity: cfg.UserLimiters})
	if err != nil {
		return nil, err
	}
	parts, err := partURLLimiter(cfg, shared, log)
	if err != nil {
		return nil, err
	}
	opts := api.Options{
		Forwarder: fwd, Verifier: verifier, Revocations: sync, Gate: gate, Users: users,
		BodyMax: cfg.BodyMax, CORSOrigins: cfg.CORSOrigins, Log: log,
		ClientIP: api.ClientIP{Header: cfg.ClientIPHeader, Trusted: cfg.TrustedProxies},
	}
	if parts != nil {
		opts.PartURLs = parts
	}
	handler, err := api.New(opts)
	if err != nil {
		return nil, err
	}
	metricsHandler, err := metrics.Handler(metricsNamespace, metricsSubsystem, slices.Concat(metricSources(handler, fwd), sync.Sources(), limiterSources(parts)))
	if err != nil {
		return nil, err
	}
	adm := admin.New(admin.Config{Addr: cfg.AdminAddr, Metrics: metricsHandler}, log)
	return &node{
		cfg:    cfg,
		log:    log,
		router: router,
		sync:   sync,
		redis:  shared,
		admin:  adm,
		http:   newHTTPServer(cfg, handler, log),
		ready:  &readiness{admin: adm, log: log},
	}, nil
}

func metricSources(handler *api.Handler, fwd *chatrpc.Forwarder) []metrics.Source {
	out := slices.Concat(handler.Sources(), fwd.CommandSources())
	for _, latency := range fwd.ChatCallLatency() {
		out = append(out, metrics.Source{
			Name:      "chat_call_seconds",
			Help:      "Chat call latency of client commands, in seconds.",
			Labels:    map[string]string{"command": latency.Command},
			Histogram: latency.Read,
		})
	}
	return out
}

func newHTTPServer(cfg config.Config, handler http.Handler, log *slog.Logger) *http.Server {
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
		Protocols:         &protocols,
		HTTP2:             &http.HTTP2Config{MaxConcurrentStreams: cfg.H2MaxStreams},
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}
