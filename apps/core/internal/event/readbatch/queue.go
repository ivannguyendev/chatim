package readbatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/jsconsume"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errStarted = errors.New("read batch queue already started")

type Enqueue func(room uint64, events []*chatimv1.Event) error

type pending struct {
	room  domain.Room
	users map[string]domain.ReadPosition
}

type Queue struct {
	enqueue  Enqueue
	cfg      Config
	log      *slog.Logger
	fails    *jsconsume.LimitedLog
	mu       sync.Mutex
	rooms    map[uint64]*pending
	closed   bool
	started  atomic.Bool
	stop     chan struct{}
	done     chan struct{}
	entries  atomic.Uint64
	events   atomic.Uint64
	dropped  atomic.Uint64
	failures atomic.Uint64
}

func New(enqueue Enqueue, cfg Config, log *slog.Logger) (*Queue, error) {
	if enqueue == nil {
		return nil, fmt.Errorf("%w: read batch queue needs an enqueue func", apperr.ErrInvalidArgument)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	return &Queue{
		enqueue: enqueue, cfg: cfg.withDefaults(), log: log, fails: jsconsume.NewLimitedLog(log),
		rooms: map[uint64]*pending{}, stop: make(chan struct{}), done: make(chan struct{}),
	}, nil
}

func (q *Queue) Add(room domain.Room, user string, pos domain.ReadPosition) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		q.dropped.Add(1)
		return
	}
	q.entries.Add(1)
	p, ok := q.rooms[room.ID]
	if !ok {
		p = &pending{room: domain.Room{ID: room.ID, Tenant: room.Tenant, Type: room.Type}, users: map[string]domain.ReadPosition{}}
		q.rooms[room.ID] = p
	}
	if old, seen := p.users[user]; !seen || pos.Ver > old.Ver {
		p.users[user] = pos
	}
}

func (q *Queue) Close(ctx context.Context) error {
	q.mu.Lock()
	first := !q.closed
	q.closed = true
	q.mu.Unlock()
	if !first {
		return nil
	}
	close(q.stop)
	if q.started.Load() {
		select {
		case <-q.done:
		case <-ctx.Done():
			q.dropped.Add(countEntries(q.swap()))
			return ctx.Err()
		}
	}
	q.flush(q.swap())
	return nil
}

func (q *Queue) swap() map[uint64]*pending {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.rooms) == 0 {
		return nil
	}
	rooms := q.rooms
	q.rooms = map[uint64]*pending{}
	return rooms
}

func countEntries(rooms map[uint64]*pending) uint64 {
	var n uint64
	for _, p := range rooms {
		n += uint64(len(p.users))
	}
	return n
}

func (q *Queue) Entries() uint64 { return q.entries.Load() }

func (q *Queue) Events() uint64 { return q.events.Load() }

func (q *Queue) Dropped() uint64 { return q.dropped.Load() }

func (q *Queue) EnqueueFailures() uint64 { return q.failures.Load() }
