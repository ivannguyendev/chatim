package sweep

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	startedMsg = "event sweeper started"
	stoppedMsg = "event sweeper lost the leader slot"
)

var (
	errStarted  = errors.New("event sweeper already started")
	errStopped  = errors.New("event sweeper stopping")
	errLostLead = errors.New("event sweeper no longer owns the leader slot")
)

type Owner interface {
	Owns(slot uint16) bool
}

type Deps struct {
	States      store.EventStates
	Checkpoints store.SweepCheckpoints
	Owner       Owner
	JS          publish.JetStream
	Rebuilders  map[store.StateCollection]Rebuilder
}

type Sweeper struct {
	deps    Deps
	cfg     Config
	log     *slog.Logger
	fails   *eventkit.LimitedLog
	drops   *eventkit.LimitedLog
	stale   *eventkit.LimitedLog
	odd     *eventkit.LimitedLog
	foreign map[string]struct{}
	pace    *pace
	stats   counters
	started atomic.Bool
	stop    chan struct{}
	halt    sync.Once
	done    chan struct{}
}

type lead struct {
	fresh map[store.StateCollection]bool
}

func New(deps Deps, cfg Config, log *slog.Logger) (*Sweeper, error) {
	if deps.States == nil || deps.Checkpoints == nil || deps.Owner == nil || deps.JS == nil {
		return nil, fmt.Errorf("%w: sweeper needs event states, checkpoints, an owner and a jetstream client", apperr.ErrInvalidArgument)
	}
	for c, rb := range deps.Rebuilders {
		if !c.Valid() || rb == nil {
			return nil, fmt.Errorf("%w: sweeper rebuilder for %s", apperr.ErrInvalidArgument, c)
		}
	}
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	return &Sweeper{
		deps: deps, cfg: cfg, log: log,
		fails: eventkit.NewLimitedLog(log), drops: eventkit.NewLimitedLog(log), stale: eventkit.NewLimitedLog(log),
		odd: eventkit.NewLimitedLog(log), foreign: make(map[string]struct{}),
		pace: newPace(cfg.Rate, cfg.Batch), stop: make(chan struct{}), done: make(chan struct{}),
	}, nil
}

func (s *Sweeper) Run(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return errStarted
	}
	defer close(s.done)
	var current *lead
	for {
		if s.stopping() {
			return nil
		}
		wait := s.cfg.Tick
		switch {
		case s.leading():
			if current == nil {
				current = s.newLead()
				s.log.InfoContext(ctx, startedMsg)
			}
			if err := s.pass(ctx, current); errors.Is(err, errLostLead) {
				current = s.lostLead(ctx)
			} else {
				wait = s.cfg.Interval
			}
		case current != nil:
			current = s.lostLead(ctx)
		}
		switch err := sleep(ctx, s.stop, wait); {
		case errors.Is(err, errStopped):
			return nil
		case err != nil:
			return err
		}
	}
}

func (s *Sweeper) Close(ctx context.Context) error {
	s.halt.Do(func() { close(s.stop) })
	if !s.started.Load() {
		return nil
	}
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Sweeper) leading() bool { return s.deps.Owner.Owns(LeaderSlot) }

func (s *Sweeper) stopping() bool {
	select {
	case <-s.stop:
		return true
	default:
		return false
	}
}

func (s *Sweeper) newLead() *lead {
	l := &lead{fresh: make(map[store.StateCollection]bool, len(s.deps.Rebuilders))}
	for c := range s.deps.Rebuilders {
		l.fresh[c] = true
	}
	return l
}

func (s *Sweeper) lostLead(ctx context.Context) *lead {
	s.stats.resetOldest()
	s.log.InfoContext(ctx, stoppedMsg)
	return nil
}
