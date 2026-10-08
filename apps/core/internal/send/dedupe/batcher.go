package dedupe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

type Registry interface {
	Reserve(ctx context.Context, keys []Key) ([]Verdict, error)
	Commit(ctx context.Context, entries []Entry) error
	Abort(ctx context.Context, keys []Key) error
}

var (
	ErrBatcherClosed  = fmt.Errorf("cid batcher closed: %w", apperr.ErrUnavailable)
	errBatcherStarted = errors.New("cid batcher already started")
)

type reserveCall struct {
	keys []Key
	out  chan reserveResult
}

func (c reserveCall) size() int { return len(c.keys) }

type reserveResult struct {
	verdicts []Verdict
	err      error
}

type settleCall struct {
	commits []Entry
	aborts  []Key
}

func (c settleCall) size() int { return len(c.commits) + len(c.aborts) }

type batchShard struct {
	reserves chan reserveCall
	settles  chan settleCall
	full     atomic.Bool
}

type Batcher struct {
	store    Registry
	cfg      BatchConfig
	log      *slog.Logger
	shards   []*batchShard
	mu       sync.RWMutex
	closed   bool
	started  atomic.Bool
	dropped  atomic.Uint64
	late     atomic.Bool
	stopping chan struct{}
	stop     sync.Once
	abort    chan struct{}
	halt     sync.Once
	done     chan struct{}
}

func NewBatcher(store Registry, cfg BatchConfig, log *slog.Logger) (*Batcher, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: cid batcher needs a store", apperr.ErrInvalidArgument)
	}
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	b := &Batcher{
		store: store, cfg: cfg, log: log, shards: make([]*batchShard, cfg.Shards),
		stopping: make(chan struct{}), abort: make(chan struct{}), done: make(chan struct{}),
	}
	for i := range b.shards {
		b.shards[i] = &batchShard{reserves: make(chan reserveCall, cfg.Queue), settles: make(chan settleCall, cfg.Queue)}
	}
	return b, nil
}

func (b *Batcher) Reserve(ctx context.Context, keys []Key) ([]Verdict, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	call := reserveCall{keys: keys, out: make(chan reserveResult, 1)}
	if err := b.submit(ctx, b.shard(keys[0].Room), call); err != nil {
		return nil, err
	}
	select {
	case res := <-call.out:
		return res.verdicts, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *Batcher) submit(ctx context.Context, s *batchShard, call reserveCall) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return ErrBatcherClosed
	}
	select {
	case s.reserves <- call:
		return nil
	case <-b.stopping:
		return ErrBatcherClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *Batcher) Commit(_ context.Context, entries []Entry) error {
	if len(entries) > 0 {
		b.settle(entries[0].Key.Room, settleCall{commits: entries})
	}
	return nil
}

func (b *Batcher) Abort(_ context.Context, keys []Key) error {
	if len(keys) > 0 {
		b.settle(keys[0].Room, settleCall{aborts: keys})
	}
	return nil
}

func (b *Batcher) settle(room uint64, call settleCall) {
	s := b.shard(room)
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		n := call.size()
		b.dropped.Add(uint64(max(n, 0)))
		if b.late.CompareAndSwap(false, true) {
			b.log.Warn("cid settle after close; dropping", "room", room, "keys", n)
		}
		return
	}
	select {
	case s.settles <- call:
		s.full.Store(false)
	default:
		n := call.size()
		b.dropped.Add(uint64(max(n, 0)))
		if s.full.CompareAndSwap(false, true) {
			b.log.Warn("cid settle queue full; dropping commits", "room", room, "keys", n)
		}
	}
}

func (b *Batcher) Close(ctx context.Context) error {
	b.stop.Do(func() { close(b.stopping) })
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		for _, s := range b.shards {
			close(s.reserves)
			close(s.settles)
		}
	}
	b.mu.Unlock()
	if !b.started.Load() {
		b.discardQueued()
		return nil
	}
	select {
	case <-b.done:
		return nil
	case <-ctx.Done():
		b.halt.Do(func() { close(b.abort) })
		<-b.done
		return ctx.Err()
	}
}

func (b *Batcher) Dropped() uint64 { return b.dropped.Load() }

func (b *Batcher) shard(room uint64) *batchShard {
	return b.shards[int(slotmap.Of(room))%len(b.shards)]
}
