package interest

import (
	"context"
	"errors"
	"hash/maphash"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

const shardCount = 64

type Sink interface {
	Send(frame []byte)
	TrySend(frame []byte) bool
	Fail()
}

type Room struct {
	ID  uint64
	Ver uint32
}

type Loader interface {
	Load(ctx context.Context, tenant, user string) ([]Room, error)
}

type Config struct {
	Inflight     int
	OpenPending  time.Duration
	ResyncWindow time.Duration
}

type userKey struct {
	tenant string
	user   string
}

type roomKey struct {
	tenant string
	room   uint64
}

type userShard struct {
	mu    sync.Mutex
	users map[userKey]*user
}

type roomShard struct {
	mu      sync.Mutex
	members map[roomKey]map[*user]struct{}
	open    map[roomKey]map[*Conn]struct{}
}

type Table struct {
	cfg        Config
	loader     Loader
	log        *slog.Logger
	seed       maphash.Seed
	users      [shardCount]userShard
	rooms      [shardCount]roomShard
	slots      chan struct{}
	lastSeq    atomic.Uint64
	life       context.Context
	stop       context.CancelFunc
	closeMu    sync.RWMutex
	closed     bool
	loads      sync.WaitGroup
	resyncMu   sync.Mutex
	resyncStop context.CancelFunc
	backlogMu  sync.Mutex
	backlog    map[*Conn]struct{}
	backlogged atomic.Int64
	stats      stats
	failLog    rate.Sometimes
}

func New(cfg Config, loader Loader, log *slog.Logger) (*Table, error) {
	if cfg.Inflight <= 0 || cfg.OpenPending <= 0 || cfg.ResyncWindow <= 0 {
		return nil, errors.New("interest: inflight, open pending and resync window must be positive")
	}
	if loader == nil || log == nil {
		return nil, errors.New("interest: loader and logger are required")
	}
	life, stop := context.WithCancel(context.Background())
	t := &Table{cfg: cfg, loader: loader, log: log, seed: maphash.MakeSeed(), slots: make(chan struct{}, cfg.Inflight), life: life, stop: stop, backlog: map[*Conn]struct{}{}, failLog: rate.Sometimes{Interval: time.Second}}
	for i := range t.users {
		t.users[i].users = map[userKey]*user{}
		t.rooms[i].members = map[roomKey]map[*user]struct{}{}
		t.rooms[i].open = map[roomKey]map[*Conn]struct{}{}
	}
	t.stats.init()
	return t, nil
}

func (t *Table) Close() {
	t.closeMu.Lock()
	t.closed = true
	t.closeMu.Unlock()
	t.stop()
	t.loads.Wait()
}

func (t *Table) spawn(fn func()) bool {
	t.closeMu.RLock()
	defer t.closeMu.RUnlock()
	if t.closed {
		return false
	}
	t.loads.Go(fn)
	return true
}

func (t *Table) userShard(k userKey) *userShard {
	var h maphash.Hash
	h.SetSeed(t.seed)
	_, _ = h.WriteString(k.tenant)
	_ = h.WriteByte(0)
	_, _ = h.WriteString(k.user)
	return &t.users[h.Sum64()%shardCount]
}

func (t *Table) roomShard(k roomKey) *roomShard {
	return &t.rooms[maphash.Comparable(t.seed, k.room)%shardCount]
}

func (t *Table) lookup(k userKey) *user {
	s := t.userShard(k)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.users[k]
}

func (t *Table) index(u *user, room uint64) {
	k := roomKey{tenant: u.key.tenant, room: room}
	s := t.roomShard(k)
	s.mu.Lock()
	set := s.members[k]
	if set == nil {
		set = map[*user]struct{}{}
		s.members[k] = set
	}
	set[u] = struct{}{}
	s.mu.Unlock()
	t.stats.pairs.Add(1)
}

func (t *Table) unindex(u *user, room uint64) {
	k := roomKey{tenant: u.key.tenant, room: room}
	s := t.roomShard(k)
	s.mu.Lock()
	if set := s.members[k]; set != nil {
		delete(set, u)
		if len(set) == 0 {
			delete(s.members, k)
		}
	}
	s.mu.Unlock()
	t.stats.pairs.Add(-1)
}

func (t *Table) members(k roomKey) []*user {
	s := t.roomShard(k)
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.members[k]
	out := make([]*user, 0, len(set))
	for u := range set {
		out = append(out, u)
	}
	return out
}

func (t *Table) setOpen(c *Conn, room uint64, open bool) {
	k := roomKey{tenant: c.user.key.tenant, room: room}
	s := t.roomShard(k)
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.open[k]
	if open {
		if set == nil {
			set = map[*Conn]struct{}{}
			s.open[k] = set
		}
		set[c] = struct{}{}
		return
	}
	delete(set, c)
	if len(set) == 0 {
		delete(s.open, k)
	}
}

func (t *Table) viewers(k roomKey) []*Conn {
	s := t.roomShard(k)
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.open[k]
	out := make([]*Conn, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	return out
}
