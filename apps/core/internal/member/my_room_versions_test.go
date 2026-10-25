package member_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/member"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type failingRoomVersions struct {
	*memstore.Rooms
	err error
}

func (f failingRoomVersions) RoomVersionsOf(context.Context, string, string) ([]store.RoomVersion, error) {
	return nil, f.err
}

func roomVersionReader(t *testing.T, rooms *memstore.Rooms, members member.MemberLister) *member.Reader {
	t.Helper()
	checker, err := access.NewChecker(rooms, nil)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	r, err := member.NewReader(member.ReaderDeps{Access: checker, Members: members})
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	return r
}

func TestMyRoomVersionsListsOnlyTheCallersOwnRooms(t *testing.T) {
	rg := newRig(t, nil)
	r := roomVersionReader(t, rg.rooms, rg.rooms)
	got, err := r.MyRoomVersions(t.Context(), tenant, "bob")
	if err != nil || !slices.Equal(got, []store.RoomVersion{{Room: room, Ver: 1}}) {
		t.Fatalf("MyRoomVersions(bob) = %v, %v; want room %d at ver 1", got, err, room)
	}
	if got, err := r.MyRoomVersions(t.Context(), "globex", "bob"); err != nil || len(got) != 0 {
		t.Fatalf("MyRoomVersions(globex, bob) = %v, %v; want none", got, err)
	}
}

func TestMyRoomVersionsRejectsABadCaller(t *testing.T) {
	rg := newRig(t, nil)
	r := roomVersionReader(t, rg.rooms, failingRoomVersions{Rooms: rg.rooms, err: errors.New("must not be read")})
	for name, who := range map[string][2]string{"no tenant": {"", "bob"}, "no user": {tenant, ""}, "bad user": {tenant, "b o b"}} {
		if _, err := r.MyRoomVersions(t.Context(), who[0], who[1]); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: MyRoomVersions = %v, want ErrInvalidArgument", name, err)
		}
	}
}

func TestMyRoomVersionsReportsAStoreFailureAsUnavailable(t *testing.T) {
	rg := newRig(t, nil)
	r := roomVersionReader(t, rg.rooms, failingRoomVersions{Rooms: rg.rooms, err: errors.New("server selection timeout")})
	if _, err := r.MyRoomVersions(t.Context(), tenant, "bob"); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("MyRoomVersions = %v, want ErrUnavailable", err)
	}
	canceled := roomVersionReader(t, rg.rooms, failingRoomVersions{Rooms: rg.rooms, err: context.Canceled})
	if _, err := canceled.MyRoomVersions(t.Context(), tenant, "bob"); !errors.Is(err, context.Canceled) || errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("MyRoomVersions(canceled) = %v, want context.Canceled", err)
	}
}
