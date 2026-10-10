package pbconv

import (
	"strconv"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func BookmarkEventID(room, thread, seq uint64, user string, ver uint32) string {
	return RoomID(room) + "-bm-" + strconv.FormatUint(thread, 10) + "-" + strconv.FormatUint(seq, 10) + "-" + user + "-v" + strconv.FormatUint(uint64(ver), 10)
}

func BookmarkChanged(r domain.Room, b domain.Bookmark) *chatimv1.Event {
	ev := roomEnvelope(r.ID, r.Tenant, r.Type, BookmarkEventID(r.ID, b.Thread, b.Seq, b.User, b.Ver), b.User, b.At)
	ev.ThreadRoot, ev.Seq = b.Thread, b.Seq
	ev.Payload = &chatimv1.Event_BookmarkChanged{BookmarkChanged: &chatimv1.BookmarkChanged{
		ThreadRoot: b.Thread, Seq: b.Seq, User: b.User, On: b.On, Ver: b.Ver,
	}}
	return ev
}
