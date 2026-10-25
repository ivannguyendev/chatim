package coreroute

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const DefaultReadyTimeout = 10 * time.Second

type Config struct {
	RedisAddr     string
	RedisDB       int
	RedisPassword string
	ClientName    string
	ReadyTimeout  time.Duration
	Log           *slog.Logger
	Dial          Dialer
}

type Router struct {
	Resolver  *slotmap.Resolver
	pool      *Pool
	rdb       *redis.Client
	refresher *refresher
	stop      context.CancelFunc
	runDone   chan error
	closeOnce sync.Once
	closeErr  error
}

func Open(ctx context.Context, cfg Config) (*Router, error) {
	if cfg.ReadyTimeout < 0 {
		return nil, errors.New("coreroute: ready timeout must not be negative")
	}
	log := cmp.Or(cfg.Log, slog.Default())
	rdb := redis.NewClient(&redis.Options{
		Addr: cfg.RedisAddr, DB: cfg.RedisDB, Password: cfg.RedisPassword, ClientName: cfg.ClientName, ContextTimeoutEnabled: true,
	})
	r := &Router{rdb: rdb, runDone: make(chan error, 1)}
	res, err := slotmap.NewResolver(rdb, slotmap.ResolverConfig{OnLoad: r.prune}, log)
	if err != nil {
		return nil, errors.Join(err, rdb.Close())
	}
	dial := cfg.Dial
	if dial == nil {
		dial = DialInsecure
	}
	pool, err := NewPool(res, dial)
	if err != nil {
		return nil, errors.Join(err, rdb.Close())
	}
	r.Resolver, r.pool = res, pool
	r.refresher = newRefresher(pool.Refresh, log)
	runCtx, stop := context.WithCancel(ctx)
	r.stop = stop
	go func() { r.runDone <- res.Run(runCtx) }()
	go r.refresher.run(runCtx)
	if err := r.awaitReady(ctx, cfg); err != nil {
		return nil, errors.Join(err, r.Close())
	}
	return r, nil
}

func (r *Router) Room(roomID string) (Conn, error) { return r.pool.Room(roomID) }

func (r *Router) Any() (Conn, error) { return r.pool.Any() }

func (r *Router) Trigger() { r.refresher.trigger() }

func (r *Router) Refresh(ctx context.Context) error { return r.pool.Refresh(ctx) }

func (r *Router) Close() error {
	r.closeOnce.Do(func() {
		r.stop()
		runErr := <-r.runDone
		r.refresher.wait()
		r.closeErr = errors.Join(runErr, r.pool.Close(), r.rdb.Close())
	})
	return r.closeErr
}

func (r *Router) prune(addrs []string) {
	if len(addrs) == 0 {
		return
	}
	r.pool.Prune(addrs)
}

func (r *Router) awaitReady(ctx context.Context, cfg Config) error {
	wait := cmp.Or(cfg.ReadyTimeout, DefaultReadyTimeout)
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-r.Resolver.Ready():
		return nil
	case <-timer.C:
		return fmt.Errorf("slot table not loaded from redis %s within %v", cfg.RedisAddr, wait)
	case <-ctx.Done():
		return ctx.Err()
	}
}
