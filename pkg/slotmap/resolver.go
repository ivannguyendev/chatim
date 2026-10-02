package slotmap

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/pkg/backoff"
)

const (
	DefaultRefresh     = 5 * time.Second
	DefaultLoadTimeout = 2 * time.Second
	MaxCores           = 256

	firstRetry = 100 * time.Millisecond
)

type ResolverConfig struct {
	Refresh     time.Duration
	LoadTimeout time.Duration
}

type Route struct {
	Core  string
	Addr  string
	Owner bool
}

type Resolver struct {
	rdb      redis.UniversalClient
	cfg      ResolverConfig
	log      *slog.Logger
	slotKeys []string
	table    atomic.Pointer[table]
	started  atomic.Uint64
	rotation atomic.Uint64
	mu       sync.Mutex
	lastErr  error
	ready    chan struct{}
	once     sync.Once
}

func NewResolver(rdb redis.UniversalClient, cfg ResolverConfig, log *slog.Logger) (*Resolver, error) {
	switch {
	case rdb == nil:
		return nil, errors.New("slotmap: resolver needs a redis client")
	case cfg.Refresh < 0 || cfg.LoadTimeout < 0:
		return nil, errors.New("slotmap: resolver Refresh and LoadTimeout must not be negative")
	}
	cfg.Refresh = cmp.Or(cfg.Refresh, DefaultRefresh)
	cfg.LoadTimeout = cmp.Or(cfg.LoadTimeout, DefaultLoadTimeout)
	if log == nil {
		log = slog.Default()
	}
	keys := make([]string, Count)
	for s := range keys {
		keys[s] = SlotKey(uint16(s))
	}
	return &Resolver{rdb: rdb, cfg: cfg, log: log, slotKeys: keys, ready: make(chan struct{})}, nil
}

func (r *Resolver) Ready() <-chan struct{} { return r.ready }

func (r *Resolver) Addr(room uint64) (string, bool) {
	rt, ok := r.Slot(Of(room))
	return rt.Addr, ok
}

func (r *Resolver) Slot(slot uint16) (Route, bool) {
	t := r.table.Load()
	if t == nil || slot >= Count || t.route[slot] == nil {
		return Route{}, false
	}
	c := t.route[slot]
	return Route{Core: c.id, Addr: c.addr, Owner: t.owned[slot]}, true
}

func (r *Resolver) AnyAddr() (string, bool) {
	t := r.table.Load()
	if t == nil || len(t.cores) == 0 {
		return "", false
	}
	return t.cores[r.rotation.Add(1)%uint64(len(t.cores))].addr, true
}

func (r *Resolver) Refresh(ctx context.Context) error {
	ticket := r.started.Load()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started.Load() != ticket && r.lastErr == nil {
		return nil
	}
	r.started.Add(1)
	r.lastErr = r.load(ctx)
	return r.lastErr
}

func (r *Resolver) Run(ctx context.Context) error {
	ps := r.subscribe(ctx)
	defer func() { _ = ps.Close() }()
	changes := ps.Channel()
	timer := time.NewTimer(0)
	defer timer.Stop()
	retry := firstRetry
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-changes:
			drain(changes)
		case <-timer.C:
		}
		next := r.cfg.Refresh
		if err := r.Refresh(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			next, retry = backoff.Jitter(retry), min(2*retry, r.cfg.Refresh)
			r.log.WarnContext(ctx, "slot table reload failed", "err", err, "retry_in", next)
		} else {
			retry = firstRetry
		}
		timer.Reset(next)
	}
}

func (r *Resolver) subscribe(ctx context.Context) *redis.PubSub {
	ps := r.rdb.Subscribe(ctx, ChangedChannel)
	confirmCtx, cancel := context.WithTimeout(ctx, r.cfg.LoadTimeout)
	defer cancel()
	if _, err := ps.Receive(confirmCtx); err != nil && ctx.Err() == nil {
		r.log.WarnContext(ctx, "slot change subscription not confirmed, relying on periodic reload", "err", err)
	}
	return ps
}

func drain(changes <-chan *redis.Message) {
	for {
		select {
		case <-changes:
		default:
			return
		}
	}
}
