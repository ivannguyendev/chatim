package recovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/redisguard"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

var errStarted = errors.New("recovery sweeper already started")

type Slots interface {
	Owned() []uint16
	Owns(slot uint16) bool
}

type Rooms interface {
	Recover(ctx context.Context, room, from uint64) error
}

type Timeline interface {
	Last(ctx context.Context, room, thread uint64) (seq, pts uint64, err error)
	Page(ctx context.Context, q store.PageQuery) ([]domain.Message, error)
}

type Deps struct {
	Slots Slots
	Rooms Rooms
	Msgs  Timeline
	Redis *redis.Client
}

type Sweeper struct {
	deps    Deps
	cfg     Config
	log     *slog.Logger
	now     func() time.Time
	started atomic.Bool
	wake    chan struct{}
	cursor  map[uint16]int64

	mu       sync.Mutex
	pending  [slotmap.Count]bool
	npending int
}

func New(deps Deps, cfg Config, log *slog.Logger) (*Sweeper, error) {
	if deps.Slots == nil || deps.Rooms == nil || deps.Msgs == nil {
		return nil, fmt.Errorf("%w: recovery sweeper needs slots, rooms and a message timeline", apperr.ErrInvalidArgument)
	}
	if err := redisguard.CheckClient(deps.Redis, "recovery sweeper"); err != nil {
		return nil, err
	}
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	return &Sweeper{
		deps:   deps,
		cfg:    cfg,
		log:    log,
		now:    time.Now,
		wake:   make(chan struct{}, 1),
		cursor: make(map[uint16]int64),
	}, nil
}

func (s *Sweeper) Trigger(slots []uint16) {
	s.mu.Lock()
	for _, slot := range slots {
		if int(slot) < slotmap.Count && !s.pending[slot] {
			s.pending[slot] = true
			s.npending++
		}
	}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Sweeper) Run(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return errStarted
	}
	tick := time.NewTicker(s.cfg.Interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.wake:
			s.sweep(ctx, nil)
		case <-tick.C:
			s.sweep(ctx, s.deps.Slots.Owned())
		}
	}
}

func (s *Sweeper) sweep(ctx context.Context, periodic []uint16) {
	var sum summary
	for ctx.Err() == nil {
		slot, ok := s.nextTriggered()
		if !ok {
			if len(periodic) == 0 {
				break
			}
			slot, periodic = periodic[0], periodic[1:]
		}
		if s.deps.Slots.Owns(slot) {
			sum.add(s.sweepSlot(ctx, slot))
		}
	}
	sum.report(ctx, s.log)
}

func (s *Sweeper) nextTriggered() (uint16, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.npending == 0 {
		return 0, false
	}
	for i, marked := range s.pending {
		if marked {
			s.pending[i] = false
			s.npending--
			return uint16(i), true
		}
	}
	return 0, false
}
