package pbconv

import (
	"strconv"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const RepliesCounter = "replies"

func MessageCounter(name string) bool {
	return name == ReactionsCounter || name == RepliesCounter
}

func MessageCountsEventID(room, thread, seq uint64, counter string, version uint64) string {
	return MessageEventID(room, thread, seq) + "-" + counter + "-v" + strconv.FormatUint(version, 10)
}

func ReplyCountsChanged(roomType domain.RoomType, m domain.Message, at time.Time) *chatimv1.Event {
	return &chatimv1.Event{
		Id:         MessageCountsEventID(m.Room, m.Thread, m.Seq, RepliesCounter, m.Replies.Version),
		Tenant:     m.Tenant,
		RoomId:     RoomID(m.Room),
		RoomType:   RoomType(roomType),
		ThreadRoot: m.Thread,
		Seq:        m.Seq,
		Ts:         timestamppb.New(at),
		Payload: &chatimv1.Event_CountsChanged{CountsChanged: &chatimv1.CountsChanged{
			Counter: RepliesCounter, ReplyCount: ReplyCount(m.Replies),
		}},
	}
}
