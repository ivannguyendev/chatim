package timer_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

const (
	room      uint64 = 7_340_000_001
	otherRoom uint64 = 7_340_000_002
	tenant           = "acme"
)

var (
	errBoom    = errors.New("boom")
	repairedAt = time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
)

type fakeEvents struct {
	mu     sync.Mutex
	events []*chatimv1.Event
	rooms  []uint64
	err    error
}

func (f *fakeEvents) Enqueue(r uint64, evs []*chatimv1.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	for _, ev := range evs {
		f.events, f.rooms = append(f.events, ev), append(f.rooms, r)
	}
	return nil
}

func (f *fakeEvents) ids() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.events))
	for i, ev := range f.events {
		out[i] = ev.GetId()
	}
	return out
}

type armed struct {
	room    uint64
	key     store.MsgKey
	counter string
}

type fakeTimers struct {
	mu    sync.Mutex
	armed []armed
	err   error
	calls *[]string
}

func (f *fakeTimers) ArmMemberCountCheck(_ context.Context, r uint64) (timer.Timer, error) {
	return f.arm(armed{room: r, counter: timer.MembersCounter})
}

func (f *fakeTimers) ArmMessageCountCheck(_ context.Context, key store.MsgKey, counter string) (timer.Timer, error) {
	return f.arm(armed{room: key.Room, key: key, counter: counter})
}

func (f *fakeTimers) arm(a armed) (timer.Timer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls != nil {
		*f.calls = append(*f.calls, "arm")
	}
	if f.err != nil {
		return timer.Timer{}, f.err
	}
	f.armed = append(f.armed, a)
	return timer.Timer{Seq: uint64(len(f.armed))}, nil
}

func (f *fakeTimers) all() []armed {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.armed)
}

func built[T any](v T, err error) func(*testing.T) T {
	return func(t *testing.T) T {
		t.Helper()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		return v
	}
}

func createRoom(t *testing.T, rooms *memstore.Rooms, id uint64) domain.Room {
	t.Helper()
	r := domain.Room{ID: id, Tenant: tenant, Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: repairedAt, MemberCount: 1}
	owner := domain.Member{Room: id, Tenant: tenant, User: "alice", Role: domain.RoleOwner, JoinedAt: repairedAt}
	if err := rooms.Create(t.Context(), r, []domain.Member{owner}); err != nil {
		t.Fatalf("Create(%d): %v", id, err)
	}
	got, err := rooms.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("Get(%d): %v", id, err)
	}
	return got
}

func memberCheck(r uint64) timer.Check {
	return timer.Check{Room: r, Counter: timer.MembersCounter, Op: 7}
}

func messageCheck(r, seq uint64, counter string) timer.Check {
	return timer.Check{Room: r, Seq: seq, Counter: counter, Op: 7}
}
