package app

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net/http"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/api"
	"github.com/ivannguyendev/chatim/apps/identity/internal/config"
	"github.com/ivannguyendev/chatim/apps/identity/internal/directory"
	"github.com/ivannguyendev/chatim/apps/identity/internal/keyring"
	"github.com/ivannguyendev/chatim/apps/identity/internal/signing"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/pkg/core/admin"
	"github.com/ivannguyendev/chatim/pkg/core/metrics"
)

const (
	metricsSubsystem = "identity"
	tokenLeeway      = 30 * time.Second
	serveAppName     = "chatim-identity"
	adminAppName     = "chatim-identity-admin"
)

type Store interface {
	directory.Source
	keyring.Source
	api.RevocationSource
	store.TrustSource
	store.ProfileWriter
	DeleteExpiredRevocations(ctx context.Context) (int64, error)
	ActivateDueSigningKey(ctx context.Context, lead time.Duration) (string, error)
	Ping(ctx context.Context) error
	Schema(ctx context.Context) (store.SchemaState, error)
	JITBatchDocs() metrics.Histogram
	Close()
}

func openMongo(_ context.Context, opts mongostore.Options) (Store, error) {
	st, err := mongostore.Open(opts)
	if err != nil {
		return nil, err
	}
	return st, nil
}

func mongoOptions(cfg config.Config, appName string) mongostore.Options {
	return mongostore.Options{
		URI: cfg.MongoURI, Database: cfg.MongoDB, User: cfg.MongoUser, Password: cfg.MongoPassword, AuthSource: cfg.MongoAuthSource,
		AppName: appName, RevocationKeep: cfg.RevocationKeep(),
		JITWriters: cfg.JITWriters, JITWindow: cfg.JITWindow, JITMaxBatch: cfg.JITMaxBatch, JITQueue: cfg.JITQueue, JITInsertTimeout: cfg.JITInsertTimeout,
	}
}

func wire(ctx context.Context, cfg config.Config, log *slog.Logger, deps Deps) (*node, error) {
	st, err := deps.OpenStore(ctx, mongoOptions(cfg, serveAppName))
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
	dir, err := newDirectory(cfg, st)
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
		})
		if err != nil {
			return nil, err
		}
		n.ports = append(n.ports, &port{addr: cfg.InternalAddr, server: newHTTPServer(cfg, handler, log)})
	}
	memoryLimited := memoryLimitSet(deps.MemoryLimit())
	if !memoryLimited {
		log.WarnContext(ctx, "GOMEMLIMIT is not set; set it to about 90% of the container memory limit")
	}
	schema := newSchemaStats()
	sources := append(metricSources(ring, n.internalStats, &n.cleanup, &n.keys), directoryMetricSources(dir, memoryLimited, time.Now)...)
	sources = append(sources, trustMetricSources(tr)...)
	sources = append(sources, schema.metricSources()...)
	sources = append(sources, metrics.Source{Name: "jit_batch_docs", Help: "Users per just-in-time InsertMany sent to MongoDB.",
		Histogram: func() metrics.Histogram { return st.JITBatchDocs() }})
	metricsHandler, err := metrics.Handler(metricsSubsystem, sources)
	if err != nil {
		return nil, err
	}
	n.admin = admin.New(admin.Config{Addr: cfg.AdminAddr, Metrics: metricsHandler}, log)
	n.health = newHealth(n.admin, log, st, ring, dir.cache, tr.verifier, schema)
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
