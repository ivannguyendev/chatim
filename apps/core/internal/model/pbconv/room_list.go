package pbconv

import (
	"fmt"
	"math"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errUnknownRoomFolder = fmt.Errorf("%w: folder", apperr.ErrInvalidArgument)

func DomainRoomFolder(f chatimv1.RoomFolder) (domain.RoomFolder, error) {
	switch f {
	case chatimv1.RoomFolder_ROOM_FOLDER_UNSPECIFIED, chatimv1.RoomFolder_ROOM_FOLDER_MAIN:
		return domain.FolderMain, nil
	case chatimv1.RoomFolder_ROOM_FOLDER_ARCHIVED:
		return domain.FolderArchived, nil
	default:
		return 0, errUnknownRoomFolder
	}
}

func MyRoomState(m domain.Member) *chatimv1.MyRoomState {
	p := m.Preferences
	return &chatimv1.MyRoomState{
		Role:          MemberRole(m.Role),
		JoinedAt:      optionalTime(m.JoinedAt),
		ClearedAt:     optionalTime(m.ClearedAt),
		ReadSeq:       m.ReadSeq,
		ReadVer:       m.ReadVer,
		PinnedAt:      optionalTime(p.PinnedAt),
		MutedUntil:    optionalTime(p.MutedUntil),
		ArchivedAt:    optionalTime(p.ArchivedAt),
		PreferenceVer: p.Ver,
	}
}

func LastPreview(p domain.RoomPreview) *chatimv1.LastPreview {
	if p.Seq == 0 {
		return nil
	}
	return &chatimv1.LastPreview{
		Seq: p.Seq, Sender: p.From, Kind: MessageKind(p.Kind), Text: p.Text,
		CreatedAt: optionalTime(p.CreatedAt), Deleted: p.Deleted, Edited: p.Edited,
	}
}

func Unread(u domain.Unread) *chatimv1.Unread {
	return &chatimv1.Unread{Count: unreadCount(u.Count), Approx: u.Approx}
}

func RoomItem(it domain.RoomItem) *chatimv1.RoomItem {
	return &chatimv1.RoomItem{
		Room:        Room(it.Room),
		Me:          MyRoomState(it.Me),
		Preview:     LastPreview(it.Preview),
		Unread:      Unread(it.Unread),
		PeerReadSeq: it.PeerReadSeq,
	}
}

func unreadCount(n int) uint32 {
	switch {
	case n < 0:
		return 0
	case n > math.MaxUint32:
		return math.MaxUint32
	default:
		return uint32(n)
	}
}
