package storetest

import (
	"fmt"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type ListableRooms interface {
	MemberRooms
	store.RoomList
}

type roomListCase struct {
	name string
	run  func(t *testing.T, s ListableRooms)
}

func RunRoomList(t *testing.T, open func(t *testing.T) ListableRooms) {
	t.Helper()
	for _, c := range slices.Concat(roomListCases(), roomListReadCases()) {
		t.Run(c.name, func(t *testing.T) { c.run(t, open(t)) })
	}
}

func roomListCases() []roomListCase {
	return []roomListCase{
		{"joined and stepwise orders match the domain order at 3, 199, 2000 and 2001 rooms", roomOrderParity},
	}
}

func roomOrderParity(t *testing.T, s ListableRooms) {
	seedListNoise(t, s)
	seeded := 0
	for _, n := range []int{3, 199, 2000, 2001} {
		seedListRooms(t, s, seeded, n)
		seeded = n
		assertRoomCount(t, s, n)
		for _, folder := range []domain.RoomFolder{domain.FolderMain, domain.FolderArchived} {
			want := expectedListOrder(n, folder)
			q := store.RoomListQuery{Tenant: tenant, User: listUser, Folder: folder}
			joined, err := s.JoinedRoomOrder(t.Context(), q)
			if err != nil {
				t.Fatalf("JoinedRoomOrder(%d rooms, folder %d): %v", n, folder, err)
			}
			stepwise, err := s.StepwiseRoomOrder(t.Context(), q)
			if err != nil {
				t.Fatalf("StepwiseRoomOrder(%d rooms, folder %d): %v", n, folder, err)
			}
			op := fmt.Sprintf("%d rooms, folder %d", n, folder)
			assertListOrder(t, "JoinedRoomOrder "+op, joined, want)
			assertListOrder(t, "StepwiseRoomOrder "+op, stepwise, want)
			assertJoinedRows(t, op, s, joined)
			assertStepwiseRows(t, op, stepwise, want)
		}
	}
}

func assertRoomCount(t *testing.T, s ListableRooms, n int) {
	t.Helper()
	for _, c := range []struct{ limit, want int }{{n + 1, n}, {n, n}, {n - 1, n - 1}, {1, 1}} {
		got, err := s.CountRooms(t.Context(), tenant, listUser, c.limit)
		if err != nil || got != c.want {
			t.Fatalf("CountRooms(%d rooms, limit %d) = %d, %v; want %d", n, c.limit, got, err, c.want)
		}
	}
}

func assertListOrder(t *testing.T, op string, got []store.RoomOrderRow, want []listRoomPlan) {
	t.Helper()
	if g, w := orderRooms(got), planRooms(want); !slices.Equal(g, w) {
		t.Fatalf("%s = %d rooms %v,\nwant %d rooms %v", op, len(g), g[:min(len(g), 12)], len(w), w[:min(len(w), 12)])
	}
	for i, r := range got {
		if !sameActivity(r.Activity, want[i].activity()) {
			t.Fatalf("%s row %d activity = %+v, want %+v", op, i, r.Activity, want[i].activity())
		}
	}
}

func assertJoinedRows(t *testing.T, op string, s ListableRooms, rows []store.RoomOrderRow) {
	t.Helper()
	for _, i := range []int{0, len(rows) / 2, len(rows) - 1} {
		if i < 0 || i >= len(rows) {
			continue
		}
		want := docOf(t, s, rows[i].Member.Room, listUser)
		if !sameMember(rows[i].Member, want) {
			t.Fatalf("JoinedRoomOrder %s row %d member = %+v, want the whole doc %+v", op, i, rows[i].Member, want)
		}
	}
}

func assertStepwiseRows(t *testing.T, op string, rows []store.RoomOrderRow, plans []listRoomPlan) {
	t.Helper()
	for i, r := range rows {
		p := plans[i]
		want := domain.Member{
			Room: p.room, Tenant: tenant, User: listUser, State: domain.MemberActive, JoinedAt: p.joinedAt,
			Preferences: domain.RoomPreferences{PinnedAt: p.pinnedAt, ArchivedAt: p.archivedAt},
		}
		if !sameMember(r.Member, want) {
			t.Fatalf("StepwiseRoomOrder %s row %d member = %+v, want only the index fields %+v", op, i, r.Member, want)
		}
	}
}
