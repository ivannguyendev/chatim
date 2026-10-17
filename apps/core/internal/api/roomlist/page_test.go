package roomlist_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func TestTheNextPageIsCutFromTheSnapshotWithFreshMemberFields(t *testing.T) {
	st := seedMinh(t, 45)
	l := newLister(t, st, 2000)
	first := list(t, l, acme, domain.FolderMain, nil, 1)
	expectItems(t, first.Items, wantItem{room: direct, preview: 12, peerRead: 10})
	if first.Next == nil || first.Next.Room != direct {
		t.Fatalf("next = %+v, want the key of room 888", first.Next)
	}
	st.reset()
	second := list(t, l, acme, domain.FolderMain, first.Next, 1)
	st.expectCalls(t, map[string]int{"MembersByKeys": 1, "RoomActivities": 1, "RoomInfos": 1, "TailWindow": 1, "TailPreviews": 1, "HiddenInRooms": 1})
	expectItems(t, second.Items, wantItem{room: shop, unread: 2, preview: 45})
	third := list(t, l, acme, domain.FolderMain, second.Next, 1)
	expectItems(t, third.Items, wantItem{room: depot, unread: 6, preview: 236})
	if third.Next != nil {
		t.Errorf("last page next = %+v, want end", third.Next)
	}
}

func TestAnotherCoreRerunsTheOrderAndKeepsOnlyKeysAfterTheCursor(t *testing.T) {
	st := seedMinh(t, 45)
	first := list(t, newLister(t, st, 2000), acme, domain.FolderMain, nil, 2)
	expectItems(t, first.Items, wantItem{room: direct, preview: 12, peerRead: 10}, wantItem{room: shop, unread: 2, preview: 45})
	st.reset()
	second := list(t, newLister(t, st, 2000), acme, domain.FolderMain, first.Next, 2)
	expectItems(t, second.Items, wantItem{room: depot, unread: 6, preview: 236})
	if second.Next != nil {
		t.Errorf("next = %+v, want end", second.Next)
	}
	calls, _ := st.snapshot()
	if calls["CountRooms"] != 1 || calls["JoinedRoomOrder"] != 1 {
		t.Errorf("rerun calls = %v, want one count and one joined order", calls)
	}
}

func TestARoomLeftAfterTheSnapshotDropsOutOfTheNextPage(t *testing.T) {
	st := seedMinh(t, 45)
	l := newLister(t, st, 2000)
	first := list(t, l, acme, domain.FolderMain, nil, 1)
	st.leave(t, shop, minh)
	second := list(t, l, acme, domain.FolderMain, first.Next, 1)
	if len(second.Items) != 0 || second.Next == nil || second.Next.Room != shop {
		t.Fatalf("second page = %v next %+v, want no items and a cursor past room 777", roomsOf(second.Items), second.Next)
	}
	third := list(t, l, acme, domain.FolderMain, second.Next, 1)
	expectItems(t, third.Items, wantItem{room: depot, unread: 6, preview: 236})
}

func TestAnotherTenantNeverSeesTheRoomsOfASameNamedUser(t *testing.T) {
	st := seedMinh(t, 45)
	l := newLister(t, st, 2000)
	p := list(t, l, globex, domain.FolderMain, nil, 20)
	expectItems(t, p.Items, wantItem{room: foreign, unread: 1, preview: 1})
	items, missing, err := l.Get(t.Context(), globex, minh, []uint64{shop, direct, foreign})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	expectItems(t, items, wantItem{room: foreign, unread: 1, preview: 1})
	if len(missing) != 2 || missing[0] != shop || missing[1] != direct {
		t.Errorf("missing = %v, want [777 888]", missing)
	}
}
