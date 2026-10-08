package ownership_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/ownership"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

const maxAttempts = 1000

func group(t *testing.T, s *memstore.Rooms, id uint64, seats ...domain.Member) {
	t.Helper()
	members := make([]domain.Member, len(seats))
	for i, m := range seats {
		m.Room = id
		members[i] = m
	}
	r := domain.Room{ID: id, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: base, MemberCount: len(seats)}
	if err := s.Create(t.Context(), r, members); err != nil {
		t.Fatalf("Create(%d): %v", id, err)
	}
}

func usersOf(r ownership.Request) []string {
	if r.Action == ownership.Leave || r.Target == r.Caller {
		return []string{r.Caller}
	}
	return []string{r.Caller, r.Target}
}

func change(ctx context.Context, s *memstore.Rooms, id uint64, r ownership.Request, inside func()) (store.OwnerResult, error) {
	return s.ChangeOwners(ctx, id, usersOf(r), func(v store.OwnerView) ([]store.MemberWrite, error) {
		if inside != nil {
			inside()
		}
		return ownership.Plan(r, v)
	})
}

func untilSettled(ctx context.Context, s *memstore.Rooms, id uint64, r ownership.Request) (store.OwnerResult, error) {
	for range maxAttempts {
		res, err := change(ctx, s, id, r, nil)
		if !errors.Is(err, domain.ErrRetryLater) {
			return res, err
		}
	}
	return store.OwnerResult{}, domain.ErrRetryLater
}

func activeOwners(t *testing.T, s *memstore.Rooms, id uint64, users ...string) (owners, active int) {
	t.Helper()
	docs, err := s.MembersOf(t.Context(), id, users)
	if err != nil {
		t.Fatalf("MembersOf(%d): %v", id, err)
	}
	for _, m := range docs {
		if m.Active() {
			active++
			if m.Role == domain.RoleOwner {
				owners++
			}
		}
	}
	return owners, active
}

var crew = []string{"alice", "bob", "carol", "dave"}

func crewRoom(t *testing.T, s *memstore.Rooms, id uint64) {
	t.Helper()
	group(t, s, id, seat("alice", domain.RoleOwner, 0, 0), seat("bob", domain.RoleOwner, 1, 0),
		seat("carol", domain.RoleAdmin, 2, 0), seat("dave", domain.RoleMember, 3, 5))
}

func TestTwoLastOwnersLeavingAtOnceKeepAnOwner(t *testing.T) {
	s := memstore.NewRooms()
	crewRoom(t, s, room)
	ctx := t.Context()
	a, b := req(ownership.Leave, "alice", "", ""), req(ownership.Leave, "bob", "", "")
	_, err := change(ctx, s, room, a, func() {
		if res, err := change(ctx, s, room, b, nil); err != nil || len(res.Written) != 1 {
			t.Errorf("B inside A = %+v, %v; want bob alone removed", res, err)
		}
	})
	if !errors.Is(err, domain.ErrRetryLater) {
		t.Fatalf("A error = %v; want ErrRetryLater", err)
	}
	res, err := change(ctx, s, room, a, nil)
	if err != nil || len(res.Written) != 2 || res.Written[0].User != "carol" || res.Written[0].Role != domain.RoleOwner || res.Written[1].User != "alice" || res.Written[1].Active() {
		t.Fatalf("A again = %+v, %v; want carol promoted first, then alice removed", res, err)
	}
	if owners, active := activeOwners(t, s, room, crew...); owners != 1 || active != 2 {
		t.Fatalf("owners, active = %d, %d; want 1, 2", owners, active)
	}
}

func TestOwnersRemovingEachOtherLeaveOneOwner(t *testing.T) {
	s := memstore.NewRooms()
	crewRoom(t, s, room)
	ctx := t.Context()
	a, b := req(ownership.Remove, "alice", "bob", ""), req(ownership.Remove, "bob", "alice", "")
	_, err := change(ctx, s, room, a, func() {
		if res, err := change(ctx, s, room, b, nil); err != nil || len(res.Written) != 1 {
			t.Errorf("B inside A = %+v, %v; want alice alone removed", res, err)
		}
	})
	if !errors.Is(err, domain.ErrRetryLater) {
		t.Fatalf("A error = %v; want ErrRetryLater", err)
	}
	if _, err := change(ctx, s, room, a, nil); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("A again error = %v; want ErrNotMember", err)
	}
	if owners, active := activeOwners(t, s, room, crew...); owners != 1 || active != 3 {
		t.Fatalf("owners, active = %d, %d; want 1, 3", owners, active)
	}
}

func TestConcurrentOwnerLeavesNeverOrphanAGroup(t *testing.T) {
	const rooms = 24
	s := memstore.NewRooms()
	for id := uint64(1); id <= rooms; id++ {
		if id%2 == 0 {
			crewRoom(t, s, id)
		} else {
			group(t, s, id, seat("alice", domain.RoleOwner, 0, 0), seat("bob", domain.RoleOwner, 1, 0))
		}
	}
	var wg sync.WaitGroup
	for id := uint64(1); id <= rooms; id++ {
		for _, u := range []string{"alice", "bob"} {
			wg.Go(func() {
				if _, err := untilSettled(t.Context(), s, id, req(ownership.Leave, u, "", "")); err != nil {
					t.Errorf("room %d: %s leaving: %v", id, u, err)
				}
			})
		}
	}
	wg.Wait()
	for id := uint64(1); id <= rooms; id++ {
		owners, active := activeOwners(t, s, id, crew...)
		want := 0
		if id%2 == 0 {
			want = 1
		}
		if owners != want || active != 2*want {
			t.Errorf("room %d: owners, active = %d, %d; want %d, %d", id, owners, active, want, 2*want)
		}
	}
}
