package mutate_test

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

const requestTTL = 15 * time.Minute

type callLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *callLog) add(c string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, c)
}

func (l *callLog) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.calls)
}

type fakeTimers struct {
	log   *callLog
	mu    sync.Mutex
	seq   uint64
	armed map[uint64]uint64
	err   error
}

func (f *fakeTimers) Arm(_ context.Context, room uint64) (work.Timer, error) {
	f.log.add("arm")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return work.Timer{}, f.err
	}
	f.seq++
	f.armed[f.seq] = room
	return work.Timer{Seq: f.seq}, nil
}

func (f *fakeTimers) Disarm(_ context.Context, t work.Timer) {
	f.log.add("disarm")
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.armed, t.Seq)
}

func (f *fakeTimers) pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.armed)
}

type forgetter struct {
	mu    sync.Mutex
	rooms []uint64
}

func (f *forgetter) ForgetMembers(room uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rooms = append(f.rooms, room)
}

func (f *forgetter) list() []uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.rooms)
}

type spyMembers struct {
	*memstore.Rooms
	log          *callLog
	addErr       error
	countErr     error
	beforeApply  func()
	duringDecide func()
}

func (s *spyMembers) AddMembers(ctx context.Context, j domain.Join, users []string) (store.JoinResult, error) {
	s.log.add("add_members")
	if s.addErr != nil {
		return store.JoinResult{}, s.addErr
	}
	return s.Rooms.AddMembers(ctx, j, users)
}

func (s *spyMembers) ApplyMember(ctx context.Context, cur, next domain.Member) (bool, error) {
	s.log.add("apply_member")
	if hook := s.beforeApply; hook != nil {
		s.beforeApply = nil
		hook()
	}
	return s.Rooms.ApplyMember(ctx, cur, next)
}

func (s *spyMembers) AddMemberCount(ctx context.Context, room uint64, delta int) (domain.MemberCount, error) {
	s.log.add("add_member_count")
	if s.countErr != nil {
		return domain.MemberCount{}, s.countErr
	}
	return s.Rooms.AddMemberCount(ctx, room, delta)
}

func (s *spyMembers) ChangeOwners(ctx context.Context, room uint64, users []string, decide store.OwnerDecision) (store.OwnerResult, error) {
	s.log.add("change_owners")
	return s.Rooms.ChangeOwners(ctx, room, users, func(v store.OwnerView) ([]store.MemberWrite, error) {
		if hook := s.duringDecide; hook != nil {
			s.duringDecide = nil
			hook()
		}
		return decide(v)
	})
}

type requestIDs struct {
	mu sync.Mutex
	n  int
}

func (r *requestIDs) next() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.n++
	return "op-" + strconv.Itoa(r.n)
}

type memberParts struct {
	members    *spyMembers
	calls      *callLog
	timers     *fakeTimers
	forgets    *forgetter
	requestIDs *requestIDs
	redis      *miniredis.Miniredis
	rdb        *redis.Client
	registry   *dedupe.Store
	requests   *dedupe.Requests
}

func newMemberParts(t *testing.T, rooms *memstore.Rooms) memberParts {
	t.Helper()
	calls := &callLog{}
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), ContextTimeoutEnabled: true})
	t.Cleanup(func() { _ = rdb.Close() })
	p := memberParts{
		members: &spyMembers{Rooms: rooms, log: calls}, calls: calls, forgets: &forgetter{}, requestIDs: &requestIDs{},
		timers: &fakeTimers{log: calls, armed: map[uint64]uint64{}}, redis: mr, rdb: rdb,
	}
	p.registry = newRegistry(t, rdb, "core-1")
	p.requests = newRequests(t, p.registry)
	return p
}

func newRegistry(t *testing.T, rdb *redis.Client, core string) *dedupe.Store {
	t.Helper()
	reg, err := dedupe.New(rdb, dedupe.Config{CoreID: core, Timeout: time.Second}, nil)
	if err != nil {
		t.Fatalf("dedupe.New: %v", err)
	}
	return reg
}

func newRequests(t *testing.T, reg dedupe.Registry) *dedupe.Requests {
	t.Helper()
	r, err := dedupe.NewRequests(reg, requestTTL)
	if err != nil {
		t.Fatalf("NewRequests: %v", err)
	}
	return r
}
