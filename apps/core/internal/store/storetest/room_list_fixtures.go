package storetest

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const (
	listUser             = "minh"
	listRoomBase  uint64 = 7_350_000_000
	listLeftRoom         = listRoomBase - 1
	listOtherRoom        = listRoomBase - 2
)

type listRoomPlan struct {
	room          uint64
	joinedAt      time.Time
	lastMessageAt time.Time
	lastSeq       uint64
	pinnedAt      time.Time
	archivedAt    time.Time
}

func listPlan(i int) listRoomPlan {
	n := max(i, 0)
	p := listRoomPlan{room: listRoomBase + uint64(n), joinedAt: secs(n % 97)}
	if n%5 != 0 {
		p.lastMessageAt, p.lastSeq = secs(n%89+10), uint64(n%13+1)
	}
	if n%11 == 0 {
		p.pinnedAt = secs(200 + n%3)
	}
	if n%7 == 0 {
		p.archivedAt = secs(300)
	}
	return p
}

func (p listRoomPlan) folder() domain.RoomFolder {
	return domain.FolderOf(domain.RoomPreferences{ArchivedAt: p.archivedAt})
}

func (p listRoomPlan) activity() store.RoomActivity {
	return store.RoomActivity{Room: p.room, LastMessageAt: p.lastMessageAt, LastSeq: p.lastSeq}
}

func seedListRooms(t *testing.T, s ListableRooms, from, to int) {
	t.Helper()
	var acts []store.Activity
	for i := from; i < to; i++ {
		p := listPlan(i)
		me := member(p.room, listUser, domain.RoleMember)
		me.JoinedAt = p.joinedAt
		mustCreate(t, s, group(p.room, "Room", 2), []domain.Member{member(p.room, "lan", domain.RoleOwner), me})
		if p.lastSeq > 0 {
			acts = append(acts, store.Activity{Room: p.room, Seq: p.lastSeq, At: p.lastMessageAt})
		}
		seedListPreferences(t, s, p)
	}
	if err := s.TouchActivity(t.Context(), acts); err != nil {
		t.Fatalf("TouchActivity(%d rooms): %v", len(acts), err)
	}
}

func seedListPreferences(t *testing.T, s ListableRooms, p listRoomPlan) {
	t.Helper()
	changes := map[time.Time]domain.PreferencesChange{p.pinnedAt: {Pinned: new(true)}, p.archivedAt: {Archived: new(true)}}
	for at, c := range changes {
		if at.IsZero() {
			continue
		}
		if _, _, err := s.SetPreferences(t.Context(), p.room, listUser, c, at); err != nil {
			t.Fatalf("SetPreferences(%d at %v): %v", p.room, at, err)
		}
	}
}

func seedListNoise(t *testing.T, s ListableRooms) {
	t.Helper()
	mustCreate(t, s, group(listLeftRoom, "Left", 2), []domain.Member{member(listLeftRoom, "lan", domain.RoleOwner), member(listLeftRoom, listUser, domain.RoleMember)})
	removeMember(t, s, docOf(t, s, listLeftRoom, listUser), secs(5))
	other := group(listOtherRoom, "Elsewhere", 1)
	other.Tenant = "globex"
	me := member(listOtherRoom, listUser, domain.RoleOwner)
	me.Tenant = "globex"
	mustCreate(t, s, other, []domain.Member{me})
}

func expectedListOrder(n int, folder domain.RoomFolder) []listRoomPlan {
	var out []listRoomPlan
	for i := range n {
		if p := listPlan(i); p.folder() == folder {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b listRoomPlan) int {
		return domain.CompareRoomSortKeys(domain.NewRoomSortKey(a.room, a.pinnedAt, a.lastMessageAt, a.joinedAt), domain.NewRoomSortKey(b.room, b.pinnedAt, b.lastMessageAt, b.joinedAt))
	})
	return out
}

func orderRooms(rows []store.RoomOrderRow) []uint64 {
	out := make([]uint64, len(rows))
	for i, r := range rows {
		out[i] = r.Member.Room
	}
	return out
}

func planRooms(plans []listRoomPlan) []uint64 {
	out := make([]uint64, len(plans))
	for i, p := range plans {
		out[i] = p.room
	}
	return out
}

func sameActivity(a, b store.RoomActivity) bool {
	return a.Room == b.Room && a.LastSeq == b.LastSeq && a.LastMessageAt.Equal(b.LastMessageAt) && a.LastEditAt.Equal(b.LastEditAt)
}
