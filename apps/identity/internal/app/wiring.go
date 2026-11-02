package app

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net/http"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/api"
	"github.com/ivannguyendev/chatim/apps/identity/internal/config"
	"github.com/ivannguyendev/chatim/apps/identity/internal/keyring"
	"github.com/ivannguyendev/chatim/apps/identity/internal/signing"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store/pgstore"
	"github.com/ivannguyendev/chatim/pkg/admin"
	"github.com/ivannguyendev/chatim/pkg/metrics"
)

const (
	metricsSubsystem = "identity"
	tokenLeeway      = 30 * time.Second
	maxPoolConns     = 1 << 16
)

type Store interface {
	store.DirectoryReader
	keyring.Source
	api.RevocationSource
	store.TrustSource
	store.ProfileWriter
	DeleteExpiredRevocations(ctx context.Context) (int64, error)
	ActivateDueSigningKey(ctx context.Context, lead time.Duration, audit store.AuditEvent) (string, error)
	Ping(ctx context.Context) error
	SchemaVersion(ctx context.Context) (int, error)
	Close()
}

func openPostgres(ctx context.Context, uri string, opts pgstore.Options) (Store, error) {
	return pgstore.Open(ctx, uri, opts)
}

func wire(ctx context.Context, cfg config.Config, log *slog.Logger, deps Deps) (*node, error) {
	st, err := deps.OpenStore(ctx, cfg.DatabaseURI(), pgstore.Options{MaxConns: poolSize(cfg.PGMaxConns), RevocationKeep: cfg.RevocationKeep()})
	if err != nil {
		return nil, err
	}
	n, err := wireServers(ctx, cfg, log, st, deps)
	if err != nil {
		st.Close()
		return nil, err
	}
	return n, nil
}

func poolSize(n int) int32 {
	if n > maxPoolConns {
		return maxPoolConns
	}
	if n < 1 {
		return 1
	}
	return int32(n)
}

func newRing(cfg config.Config, st Store) (*keyring.Ring, error) {
	sealer, err := signing.NewSealer(cfg.KeyEncryptionKey, rand.Reader)
	if err != nil {
		return nil, err
	}
	return keyring.New(st, sealer, keyring.Config{
		Token:  signing.TokenConfig{Issuer: cfg.Issuer, Audience: cfg.Audience, TTL: cfg.TokenTTL},
		Leeway: tokenLeeway,
	}), nil
}

func wireServers(ctx context.Context, cfg config.Config, log *slog.Logger, st Store, deps Deps) (*node, error) {
	ring, err := newRing(cfg, st)
	if err != nil {
		return nil, err
	}
	dir, err := newDirectory(cfg, st, deps)
	if err != nil {
		return nil, err
	}
	tr, err := newTrust(cfg, st, dir, ring, log)
	if err != nil {
		return nil, err
	}
	n := &node{cfg: cfg, log: log, store: st, internalStats: &api.InternalStats{}, dir: dir, trust: tr}
	if cfg.ListenAddr != "" {
		handler, err := api.New(api.Options{
			Keys: ring, BodyMax: int64(cfg.BodyMax),
			Directory: dir.cache, Revocations: dir.list, Claims: ring, DirectoryGate: dir.gate, DirectoryUsers: dir.users, DirectoryStats: dir.apiStats,
			Exchange: tr.service, ExchangeIPs: tr.ips, ExchangeStats: tr.apiStats,
			CORSOrigins: cfg.CORSOrigins, ClientIPHeader: cfg.ClientIPHeader, Log: log,
		})
		if err != nil {
			return nil, err
		}
		n.ports = append(n.ports, &port{addr: cfg.ListenAddr, server: newHTTPServer(cfg, handler, log)})
	}
	if cfg.InternalAddr != "" {
		handler, err := api.NewInternal(api.InternalOptions{
			Revocations: st, Secret: cfg.NodeSecret, Stats: n.internalStats, Log: log,
			DirectoryFeed: st, DirectorySignal: dir.follower, FeedWaitMax: cfg.FeedWaitMax, FeedStats: dir.feedStats,
		})
		if err != nil {
			return nil, err
		}
		server := newHTTPServer(cfg, handler, log)
		server.RegisterOnShutdown(handler.Stop)
		n.ports = append(n.ports, &port{addr: cfg.InternalAddr, server: server})
	}
	memoryLimited := memoryLimitSet(deps.MemoryLimit())
	if !memoryLimited {
		log.WarnContext(ctx, "GOMEMLIMIT is not set; set it to about 90% of the container memory limit")
	}
	sources := append(metricSources(ring, n.internalStats, &n.cleanup, &n.keys), directoryMetricSources(dir, memoryLimited, time.Now)...)
	sources = append(sources, trustMetricSources(tr)...)
	metricsHandler, err := metrics.Handler(metricsSubsystem, sources)
	if err != nil {
		return nil, err
	}
	n.admin = admin.New(admin.Config{Addr: cfg.AdminAddr, Metrics: metricsHandler}, log)
	n.health = newHealth(n.admin, log, st, ring, dir.cache, tr.verifier, deps.SchemaVersion)
	return n, nil
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
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}
