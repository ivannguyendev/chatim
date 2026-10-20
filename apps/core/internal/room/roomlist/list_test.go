package roomlist_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/room/roomlist"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type wantItem struct {
	room     uint64
	unread   int
	preview  uint64
	peerRead uint64
}

func list(t *testing.T, l *roomlist.Lister, tenant string, folder domain.RoomFolder, after *domain.RoomSortKey, limit int) roomlist.Page {
	t.Helper()
	p, err := l.List(t.Context(), roomlist.Query{Tenant: tenant, User: minh, Folder: folder, After: after, Limit: limit})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return p
}

func expectItems(t *testing.T, got []domain.RoomItem, want ...wantItem) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("items = %v, want %d items", roomsOf(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.Room.ID != w.room || g.Me.Room != w.room || g.Unread.Count != w.unread || g.Unread.Approx || g.Preview.Seq != w.preview || g.PeerReadSeq != w.peerRead {
			t.Errorf("item %d = room %d unread %+v preview %d peer %d, want %+v", i, g.Room.ID, g.Unread, g.Preview.Seq, g.PeerReadSeq, w)
		}
	}
}

func roomsOf(items []domain.RoomItem) []uint64 {
	out := make([]uint64, len(items))
	for i, it := range items {
		out[i] = it.Room.ID
	}
	return out
}

func TestTheMainFolderListsPinnedThenNewestWithUnreadPreviewAndPeerRead(t *testing.T) {
	st := seedMinh(t, 45)
	p := list(t, newLister(t, st, 2000), acme, domain.FolderMain, nil, 20)
	expectItems(t, p.Items,
		wantItem{room: direct, unread: 0, preview: 12, peerRead: 10},
		wantItem{room: shop, unread: 2, preview: 45},
		wantItem{room: depot, unread: 6, preview: 236},
	)
	if p.Next != nil {
		t.Errorf("next = %+v, want end", p.Next)
	}
	shopItem := p.Items[1]
	if shopItem.Me.Role != domain.RoleAdmin || shopItem.Me.ReadSeq != 41 || shopItem.Room.Name != "Cửa hàng 12" || shopItem.Preview.From != lan || shopItem.Preview.Text != "tin 45" {
		t.Errorf("shop item = %+v", shopItem)
	}
	if !p.Items[0].Me.Preferences.PinnedAt.Equal(clock(8, 0)) {
		t.Errorf("direct pinned_at = %v, want 08:00", p.Items[0].Me.Preferences.PinnedAt)
	}
}

func TestTheArchivedFolderHoldsOnlyTheArchivedRoom(t *testing.T) {
	st := seedMinh(t, 45)
	p := list(t, newLister(t, st, 2000), acme, domain.FolderArchived, nil, 20)
	expectItems(t, p.Items, wantItem{room: old, unread: 0, preview: 90})
}

func TestAWarmCacheReadsOnlyTheOrderTheDirectPeerAndHidden(t *testing.T) {
	st := seedMinh(t, 45)
	l := newLister(t, st, 2000)
	list(t, l, acme, domain.FolderMain, nil, 20)
	st.reset()
	p := list(t, l, acme, domain.FolderMain, nil, 20)
	st.expectCalls(t, map[string]int{"JoinedRoomOrder": 1, "MembersByKeys": 1, "HiddenInRooms": 1})
	expectItems(t, p.Items, wantItem{room: direct, preview: 12, peerRead: 10}, wantItem{room: shop, unread: 2, preview: 45}, wantItem{room: depot, unread: 6, preview: 236})
}

func TestANewMessageExtendsTheCachedTailByOneScan(t *testing.T) {
	st := seedMinh(t, 44)
	l := newLister(t, st, 2000)
	first := list(t, l, acme, domain.FolderMain, nil, 20)
	expectItems(t, first.Items[1:2], wantItem{room: shop, unread: 1, preview: 43})
	st.post(t, shop, acme, 45, lan, clock(10, 5))
	st.reset()
	p := list(t, l, acme, domain.FolderMain, nil, 20)
	if _, windows := st.snapshot(); !slices.Equal(windows, []windowCall{{room: shop, after: 44}}) {
		t.Errorf("tail scans = %v, want one scan of room 777 after seq 44", windows)
	}
	expectItems(t, p.Items[1:2], wantItem{room: shop, unread: 2, preview: 45})
}

func TestAStoreErrorFailsTheWholeCall(t *testing.T) {
	st := seedMinh(t, 45)
	st.brokenRoom = depot
	l := newLister(t, st, 2000)
	if _, err := l.List(t.Context(), roomlist.Query{Tenant: acme, User: minh, Folder: domain.FolderMain, Limit: 20}); !errors.Is(err, apperr.ErrUnavailable) {
		t.Errorf("List = %v, want ErrUnavailable", err)
	}
	if _, _, err := l.Get(t.Context(), acme, minh, []uint64{shop, depot}); !errors.Is(err, apperr.ErrUnavailable) {
		t.Errorf("Get = %v, want ErrUnavailable", err)
	}
}

func TestListRejectsABadLimitOrFolder(t *testing.T) {
	l := newLister(t, newWorld(), 2000)
	for _, q := range []roomlist.Query{
		{Tenant: acme, User: minh, Folder: domain.FolderMain, Limit: 0},
		{Tenant: acme, User: minh, Folder: domain.FolderMain, Limit: roomlist.MaxRooms + 1},
		{Tenant: acme, User: minh, Folder: 9, Limit: 20},
	} {
		if _, err := l.List(t.Context(), q); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("List(%+v) = %v, want ErrInvalidArgument", q, err)
		}
	}
}

func TestNewRequiresEveryDependency(t *testing.T) {
	if _, err := roomlist.New(roomlist.Deps{}); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("New(empty) = %v, want ErrInvalidArgument", err)
	}
}
