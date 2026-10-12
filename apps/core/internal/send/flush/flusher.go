package flush

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

var ErrNotSent = fmt.Errorf("write group not sent before its deadline: %w", domain.ErrRetryLater)

var (
	errClosed       = fmt.Errorf("flusher closed: %w", domain.ErrRetryLater)
	errInvalidGroup = fmt.Errorf("%w: write group needs messages and a done callback", apperr.ErrInvalidArgument)
	errStarted      = errors.New("flusher already started")
)

type Group struct {
	Room     uint64
	Msgs     []domain.Message
	Done     func([]store.Result)
	Deadline time.Time
}

type Config struct {
	Shards        int
	Window        time.Duration
	MaxBatch      int
	QueueSize     int
	InsertTimeout time.Duration
}

type Flusher struct {
	shards  []*shard
	mu      sync.RWMutex
	closed  bool
	started atomic.Bool
	done    chan struct{}
}

func New(msgs store.Messages, cfg Config) (*Flusher, error) {
	if msgs == nil {
		return nil, fmt.Errorf("%w: flusher needs a message store", apperr.ErrInvalidArgument)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	f := &Flusher{shards: make([]*shard, cfg.Shards), done: make(chan struct{})}
	for i := range f.shards {
		f.shards[i] = &shard{
			msgs:     msgs,
			queue:    make(chan pending, cfg.QueueSize),
			window:   cfg.Window,
			maxBatch: cfg.MaxBatch,
			timeout:  cfg.InsertTimeout,
			closeAll: f.closeQueues,
		}
	}
	return f, nil
}

func (c Config) Validate() error {
	switch {
	case c.Shards <= 0 || c.Window <= 0 || c.MaxBatch <= 0 || c.QueueSize <= 0 || c.InsertTimeout <= 0:
		return fmt.Errorf("%w: flush config %+v must be positive", apperr.ErrInvalidArgument, c)
	case c.Shards > slotmap.Count:
		return fmt.Errorf("%w: flush shards %d exceed %d slots", apperr.ErrInvalidArgument, c.Shards, slotmap.Count)
	default:
		return nil
	}
}

func (f *Flusher) Run(ctx context.Context) error {
	if !f.started.CompareAndSwap(false, true) {
		return errStarted
	}
	defer close(f.done)
	errs := make([]error, len(f.shards))
	var wg sync.WaitGroup
	for i, s := range f.shards {
		wg.Go(func() { errs[i] = s.run(ctx) })
	}
	wg.Wait()
	return cmp.Or(errs...)
}

func (f *Flusher) Submit(ctx context.Context, g Group) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(g.Msgs) == 0 || g.Done == nil {
		return errInvalidGroup
	}
	s := f.shards[int(slotmap.Of(g.Room))%len(f.shards)]
	p := pending{Group: g, at: time.Now()}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.closed {
		return errClosed
	}
	select {
	case s.queue <- p:
		return nil
	default:
		return domain.ErrBusy
	}
}

func (f *Flusher) Close(ctx context.Context) error {
	f.closeQueues()
	select {
	case <-f.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *Flusher) closeQueues() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	f.closed = true
	for _, s := range f.shards {
		close(s.queue)
	}
}
