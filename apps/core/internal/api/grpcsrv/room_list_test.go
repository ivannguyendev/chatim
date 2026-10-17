package grpcsrv_test

import (
	"slices"
	"strconv"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/roomcache"
	"github.com/ivannguyendev/chatim/apps/core/internal/api/roomlist"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func newRoomLister(t *testing.T, rg *rig) *roomlist.Lister {
	t.Helper()
	info, err := roomcache.NewInfoCache(1_000)
	if err != nil {
		t.Fatal(err)
	}
	tails, err := roomcache.NewTailCache(1 << 24)
	if err != nil {
		t.Fatal(err)
	}
	snaps, err := roomcache.NewSnapshotCache(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := roomcache.NewPathCache(1_000)
	if err != nil {
		t.Fatal(err)
	}
	l, err := roomlist.New(roomlist.Deps{List: rg.rooms, Tails: rg.msgs, Hidden: rg.hidden, Info: info, Tail: tails, Snapshots: snaps, Paths: paths, JoinMax: 2000})
	if err != nil {
		t.Fatalf("roomlist.New: %v", err)
	}
	return l
}

func TestListMyRoomsPagesWithAnOpaqueCursor(t *testing.T) {
	rg := newRig(t, options{})
	first, second := rg.createGroup(t, "acme", "alice", "bob"), rg.createGroup(t, "acme", "alice", "bob")
	rg.send(t, as(t, "acme", "bob"), first, "c1", "xin chào")
	ctx := as(t, "acme", "alice")
	page1, err := rg.client.ListMyRooms(ctx, &chatimv1.ListMyRoomsRequest{Limit: 1})
	if err != nil {
		t.Fatalf("ListMyRooms: %v", err)
	}
	if len(page1.GetItems()) != 1 || page1.GetNext() == "" {
		t.Fatalf("page 1 = %d items next %q, want one item and a cursor", len(page1.GetItems()), page1.GetNext())
	}
	page2, err := rg.client.ListMyRooms(ctx, &chatimv1.ListMyRoomsRequest{Limit: 1, Next: page1.GetNext()})
	if err != nil {
		t.Fatalf("ListMyRooms page 2: %v", err)
	}
	if len(page2.GetItems()) != 1 || page2.GetNext() != "" {
		t.Fatalf("page 2 = %d items next %q, want one item and the end", len(page2.GetItems()), page2.GetNext())
	}
	got := []string{page1.GetItems()[0].GetRoom().GetId(), page2.GetItems()[0].GetRoom().GetId()}
	if !slices.Contains(got, first) || !slices.Contains(got, second) {
		t.Fatalf("rooms = %v, want %s and %s", got, first, second)
	}
	for _, it := range append(page1.GetItems(), page2.GetItems()...) {
		if it.GetRoom().GetId() != first {
			continue
		}
		if it.GetUnread().GetCount() != 1 || it.GetPreview().GetText() != "xin chào" || it.GetPreview().GetSender() != "bob" || it.GetMe().GetRole() != chatimv1.MemberRole_MEMBER_ROLE_OWNER {
			t.Errorf("item = %v, want one unread from bob with its text", it)
		}
	}
}

func TestListMyRoomsRejectsBadInput(t *testing.T) {
	rg := newRig(t, options{})
	ctx := as(t, "acme", "alice")
	archived := domain.EncodeRoomCursor(domain.FolderArchived, domain.RoomSortKey{Room: 42})
	for name, req := range map[string]*chatimv1.ListMyRoomsRequest{
		"limit over 100":      {Limit: 101},
		"unknown folder":      {Folder: chatimv1.RoomFolder(9)},
		"broken cursor":       {Next: "not-a-cursor"},
		"cursor of a folder":  {Next: archived},
		"cursor without room": {Next: domain.EncodeRoomCursor(domain.FolderMain, domain.RoomSortKey{})},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := rg.client.ListMyRooms(ctx, req)
			expectCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestGetRoomsReturnsItemsAndMissingIDs(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	resp, err := rg.client.GetRooms(as(t, "acme", "bob"), &chatimv1.GetRoomsRequest{RoomIds: []string{room, "987654321"}})
	if err != nil {
		t.Fatalf("GetRooms: %v", err)
	}
	if len(resp.GetItems()) != 1 || resp.GetItems()[0].GetRoom().GetId() != room || !slices.Equal(resp.GetMissing(), []string{"987654321"}) {
		t.Fatalf("GetRooms = %v", resp)
	}
	other, err := rg.client.GetRooms(as(t, "globex", "bob"), &chatimv1.GetRoomsRequest{RoomIds: []string{room}})
	if err != nil {
		t.Fatalf("GetRooms other tenant: %v", err)
	}
	if len(other.GetItems()) != 0 || !slices.Equal(other.GetMissing(), []string{room}) {
		t.Fatalf("GetRooms other tenant = %v, want the room missing", other)
	}
}

func TestGetRoomsRejectsBadIDs(t *testing.T) {
	rg := newRig(t, options{})
	many := make([]string, 101)
	for i := range many {
		many[i] = strconv.Itoa(i + 1)
	}
	for name, ids := range map[string][]string{"over 100": many, "not a number": {"x"}, "zero": {"0"}} {
		t.Run(name, func(t *testing.T) {
			_, err := rg.client.GetRooms(as(t, "acme", "alice"), &chatimv1.GetRoomsRequest{RoomIds: ids})
			expectCode(t, err, codes.InvalidArgument)
		})
	}
}
