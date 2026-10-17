package pbconv_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestMyRoomStateCopiesEveryField(t *testing.T) {
	joined, cleared, pinned, muted, archived := sentAt, sentAt.Add(time.Hour), sentAt.Add(2*time.Hour), sentAt.Add(3*time.Hour), sentAt.Add(4*time.Hour)
	m := domain.Member{
		Room: 777, User: "minh", Role: domain.RoleAdmin, JoinedAt: joined, ClearedAt: cleared, ReadSeq: 41, ReadVer: 6,
		Preferences: domain.RoomPreferences{PinnedAt: pinned, MutedUntil: muted, ArchivedAt: archived, Ver: 5},
	}
	want := &chatimv1.MyRoomState{
		Role: chatimv1.MemberRole_MEMBER_ROLE_ADMIN, JoinedAt: timestamppb.New(joined), ClearedAt: timestamppb.New(cleared),
		ReadSeq: 41, ReadVer: 6, PinnedAt: timestamppb.New(pinned), MutedUntil: timestamppb.New(muted),
		ArchivedAt: timestamppb.New(archived), PreferenceVer: 5,
	}
	if got := pbconv.MyRoomState(m); !proto.Equal(got, want) {
		t.Fatalf("MyRoomState = %v, want %v", got, want)
	}
	bare := pbconv.MyRoomState(domain.Member{Role: domain.RoleMember, JoinedAt: joined, ReadSeq: 12})
	if bare.GetClearedAt() != nil || bare.GetPinnedAt() != nil || bare.GetMutedUntil() != nil || bare.GetArchivedAt() != nil {
		t.Fatalf("unset times = %v, want them absent", bare)
	}
}

func TestLastPreviewCopiesEveryFieldAndIsAbsentWhenEmpty(t *testing.T) {
	p := domain.RoomPreview{Seq: 45, From: "lan", Kind: domain.KindSystem, Text: "Cuộc gọi 3 phút", CreatedAt: sentAt, Deleted: false, Edited: true}
	want := &chatimv1.LastPreview{
		Seq: 45, Sender: "lan", Kind: chatimv1.MessageKind_MESSAGE_KIND_SYSTEM, Text: "Cuộc gọi 3 phút", CreatedAt: timestamppb.New(sentAt), Edited: true,
	}
	if got := pbconv.LastPreview(p); !proto.Equal(got, want) {
		t.Fatalf("LastPreview = %v, want %v", got, want)
	}
	if got := pbconv.LastPreview(domain.RoomPreview{}); got != nil {
		t.Fatalf("empty LastPreview = %v, want nil", got)
	}
}

func TestRoomItemCombinesTheParts(t *testing.T) {
	it := domain.RoomItem{
		Room:        domain.Room{ID: 777, Tenant: "acme", Type: domain.RoomGroup, Name: "Cửa hàng 12", CreatedAt: sentAt, InfoVer: 1},
		Me:          domain.Member{Role: domain.RoleAdmin, JoinedAt: sentAt, ReadSeq: 41},
		Preview:     domain.RoomPreview{Seq: 45, From: "lan", Kind: domain.KindText, Text: "ok", CreatedAt: sentAt},
		Unread:      domain.Unread{Count: 2},
		PeerReadSeq: 0,
	}
	want := &chatimv1.RoomItem{
		Room: pbconv.Room(it.Room), Me: pbconv.MyRoomState(it.Me), Preview: pbconv.LastPreview(it.Preview),
		Unread: &chatimv1.Unread{Count: 2},
	}
	if got := pbconv.RoomItem(it); !proto.Equal(got, want) {
		t.Fatalf("RoomItem = %v, want %v", got, want)
	}
	dm := it
	dm.Preview, dm.Unread, dm.PeerReadSeq = domain.RoomPreview{}, domain.Unread{Count: 100, Approx: true}, 12
	got := pbconv.RoomItem(dm)
	if got.GetPreview() != nil || got.GetPeerReadSeq() != 12 || got.GetUnread().GetCount() != 100 || !got.GetUnread().GetApprox() {
		t.Fatalf("RoomItem = %v, want no preview, peer read 12, unread 100 approx", got)
	}
}

func TestUnreadClampsTheCount(t *testing.T) {
	for in, want := range map[int]uint32{-1: 0, 0: 0, 100: 100, math.MaxUint32 + 1: math.MaxUint32} {
		if got := pbconv.Unread(domain.Unread{Count: in}).GetCount(); got != want {
			t.Errorf("Unread(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestDomainRoomFolder(t *testing.T) {
	cases := []struct {
		in   chatimv1.RoomFolder
		want domain.RoomFolder
		ok   bool
	}{
		{chatimv1.RoomFolder_ROOM_FOLDER_UNSPECIFIED, domain.FolderMain, true},
		{chatimv1.RoomFolder_ROOM_FOLDER_MAIN, domain.FolderMain, true},
		{chatimv1.RoomFolder_ROOM_FOLDER_ARCHIVED, domain.FolderArchived, true},
		{chatimv1.RoomFolder(3), 0, false},
		{chatimv1.RoomFolder(-1), 0, false},
	}
	for _, c := range cases {
		got, err := pbconv.DomainRoomFolder(c.in)
		if got != c.want || (err == nil) != c.ok || (!c.ok && !errors.Is(err, apperr.ErrInvalidArgument)) {
			t.Errorf("DomainRoomFolder(%v) = %d, %v; want %d, ok=%v", c.in, got, err, c.want, c.ok)
		}
	}
}
