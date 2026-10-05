package pbconv

import (
	"strconv"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func MessageChangeEventID(room, thread, seq uint64, version uint32) string {
	return MessageEventID(room, thread, seq) + "-v" + strconv.FormatUint(uint64(version), 10)
}

func MessageEdited(roomType domain.RoomType, m domain.Message, e domain.Edit) *chatimv1.Event {
	ev := messageChange(roomType, m, e)
	ev.Payload = &chatimv1.Event_MessageEdited{MessageEdited: &chatimv1.MessageEdited{Message: Message(m), Version: e.Version}}
	return ev
}

func MessageDeleted(roomType domain.RoomType, m domain.Message, e domain.Edit) *chatimv1.Event {
	ev := messageChange(roomType, m, e)
	ev.Payload = &chatimv1.Event_MessageDeleted{MessageDeleted: &chatimv1.MessageDeleted{Message: Message(m), Version: e.Version}}
	return ev
}

func messageChange(roomType domain.RoomType, m domain.Message, e domain.Edit) *chatimv1.Event {
	return &chatimv1.Event{
		Id:         MessageChangeEventID(e.Room, e.Thread, e.Seq, e.Version),
		Tenant:     m.Tenant,
		RoomId:     RoomID(m.Room),
		RoomType:   RoomType(roomType),
		ThreadRoot: m.Thread,
		Seq:        m.Seq,
		Actor:      e.By,
		Ts:         timestamppb.New(e.At),
	}
}

func MessageVersions(m domain.Message, edits []domain.Edit, after uint32) []*chatimv1.MessageVersion {
	out := make([]*chatimv1.MessageVersion, 0, len(edits)+1)
	if after == 0 && len(edits) > 0 && edits[0].Version == 1 {
		out = append(out, &chatimv1.MessageVersion{
			Kind: chatimv1.EditKind_EDIT_KIND_ORIGINAL, Text: edits[0].Prev, By: m.From, At: timestamppb.New(m.CreatedAt),
		})
	}
	for _, e := range edits {
		out = append(out, &chatimv1.MessageVersion{Version: e.Version, Kind: editKind(e.Kind), Text: e.Text, By: e.By, At: timestamppb.New(e.At)})
	}
	return out
}

func editKind(k domain.EditKind) chatimv1.EditKind {
	switch k {
	case domain.EditText:
		return chatimv1.EditKind_EDIT_KIND_TEXT
	case domain.EditDelete:
		return chatimv1.EditKind_EDIT_KIND_DELETE
	default:
		return chatimv1.EditKind_EDIT_KIND_UNSPECIFIED
	}
}
