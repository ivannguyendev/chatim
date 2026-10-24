package presence

import (
	"context"
	"errors"
	"hash/maphash"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/pkg/metrics"
)

const (
	shardCount = 64
	MaxBatch   = 1000
)

var (
	errNoStore      = errors.New("presence: stamp store is required")
	errBadConfig    = errors.New("presence: ttl, stamp interval and flush interval must be positive")
	errBadBatch     = errors.New("presence: batch must be between 1 and 1000")
	errStartedTwice = errors.New("presence: stamper already started")
)

type Store interface {
	Stamp(ctx context.Context, keys []string, ttl time.Duration) error
}

type Config struct {
	TTL        time.Duration
	StampEvery time.Duration
	Flush      time.Duration
	Batch      int
}

type user struct {
	tenant, id string
}

type entry struct {
	stamped, touched time.Time
}

type shard struct {
	mu      sync.Mutex
	users   map[user]entry
	pending map[user]struct{}
}

type Stamper struct {
	cfg      Config
	store    Store
	seed     maphash.Seed
	shards   [shardCount]shard
	stamps   atomic.Uint64
	failures atomic.Uint64

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	done    chan struct{}
}

func NewStamper(cfg Config, store Store) (*Stamper, error) {
	switch {
	case store == nil:
		return nil, errNoStore
	case cfg.TTL <= 0 || cfg.StampEvery <= 0 || cfg.Flush <= 0:
		return nil, errBadConfig
	case cfg.Batch <= 0 || cfg.Batch > MaxBatch:
		return nil, errBadBatch
	}
	s := &Stamper{cfg: cfg, store: store, seed: maphash.MakeSeed()}
	for i := range s.shards {
		s.shards[i].users = map[user]entry{}
		s.shards[i].pending = map[user]struct{}{}
	}
	return s, nil
}

func (s *Stamper) Touch(tenant, id string, now time.Time) {
	u := user{tenant: tenant, id: id}
	sh := s.shardOf(u)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, known := sh.users[u]
	e.touched = now
	if !known || now.Sub(e.stamped) >= s.cfg.StampEvery {
		e.stamped = now
		sh.pending[u] = struct{}{}
	}
	sh.users[u] = e
}

func (s *Stamper) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return errStartedTwice
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.started, s.cancel, s.done = true, cancel, make(chan struct{})
	go s.loop(ctx, s.done)
	return nil
}

func (s *Stamper) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel == nil {
		return
	}
	s.cancel()
	<-s.done
	s.cancel = nil
}

func (s *Stamper) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	tick := time.NewTicker(s.cfg.Flush)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.flush(ctx)
		}
	}
}

func (s *Stamper) flush(ctx context.Context) {
	due := s.takePending()
	for start := 0; start < len(due); start += s.cfg.Batch {
		end := min(start+s.cfg.Batch, len(due))
		if err := s.store.Stamp(ctx, keysOf(due[start:end]), s.cfg.TTL); err != nil {
			s.failures.Add(1)
			s.putBack(due[start:])
			break
		}
		addCount(&s.stamps, end-start)
	}
	s.dropIdle(time.Now())
}

func (s *Stamper) takePending() []user {
	var due []user
	for i := range s.shards {
		sh := &s.shards[i]
		sh.mu.Lock()
		for u := range sh.pending {
			due = append(due, u)
		}
		clear(sh.pending)
		sh.mu.Unlock()
	}
	return due
}

func (s *Stamper) putBack(users []user) {
	for _, u := range users {
		sh := s.shardOf(u)
		sh.mu.Lock()
		if _, known := sh.users[u]; known {
			sh.pending[u] = struct{}{}
		}
		sh.mu.Unlock()
	}
}

func (s *Stamper) dropIdle(now time.Time) {
	for i := range s.shards {
		sh := &s.shards[i]
		sh.mu.Lock()
		for u, e := range sh.users {
			if now.Sub(e.touched) > s.cfg.TTL {
				delete(sh.users, u)
				delete(sh.pending, u)
			}
		}
		sh.mu.Unlock()
	}
}

func (s *Stamper) pendingCount() int {
	n := 0
	for i := range s.shards {
		sh := &s.shards[i]
		sh.mu.Lock()
		n += len(sh.pending)
		sh.mu.Unlock()
	}
	return n
}

func (s *Stamper) shardOf(u user) *shard {
	return &s.shards[maphash.Comparable(s.seed, u)%shardCount]
}

func keysOf(users []user) []string {
	keys := make([]string, len(users))
	for i, u := range users {
		keys[i] = Key(u.tenant, u.id)
	}
	return keys
}

func (s *Stamper) Sources() []metrics.Source {
	return []metrics.Source{
		{Name: "presence_stamps_total", Help: "Online keys written to the presence Redis.", Read: counterOf(&s.stamps)},
		{Name: "presence_stamp_failures_total", Help: "Presence stamp batches that failed and ended their round.", Read: counterOf(&s.failures)},
		{Name: "presence_stamp_pending", Help: "Users waiting for the next presence stamp round.", Gauge: true, Read: func() float64 { return float64(s.pendingCount()) }},
	}
}

func addCount(v *atomic.Uint64, n int) {
	if n > 0 {
		v.Add(uint64(n))
	}
}

func counterOf(v *atomic.Uint64) func() float64 { return func() float64 { return float64(v.Load()) } }
