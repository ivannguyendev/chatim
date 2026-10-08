package pbconv

import (
	"strconv"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func ReadEventID(room uint64, user string, readVer uint64) string {
	return RoomID(room) + "-rd-" + user + "-v" + strconv.FormatUint(readVer, 10)
}

func HiddenEventID(room uint64, user string, thread, seq uint64) string {
	return RoomID(room) + "-hd-" + user + "-" + strconv.FormatUint(thread, 10) + "-" + strconv.FormatUint(seq, 10)
}

func ClearedEventID(room uint64, user string, at time.Time) string {
	return RoomID(room) + "-cl-" + user + "-" + strconv.FormatInt(at.UnixMilli(), 10)
}

func ReadUpdated(r domain.Room, user string, pos domain.ReadPosition, at time.Time) *chatimv1.Event {
	ev := roomEnvelope(r.ID, r.Tenant, r.Type, ReadEventID(r.ID, user, pos.Ver), user, at)
	ev.Payload = &chatimv1.Event_ReadUpdated{ReadUpdated: &chatimv1.ReadUpdated{User: user, ReadSeq: pos.Seq, ReadVer: pos.Ver}}
	return ev
}

func MessageHidden(r domain.Room, user string, thread, seq uint64, at time.Time) *chatimv1.Event {
	ev := roomEnvelope(r.ID, r.Tenant, r.Type, HiddenEventID(r.ID, user, thread, seq), user, at)
	ev.ThreadRoot, ev.Seq = thread, seq
	ev.Payload = &chatimv1.Event_MessageHidden{MessageHidden: &chatimv1.MessageHidden{User: user, ThreadRoot: thread, Seq: seq}}
	return ev
}

func HistoryCleared(r domain.Room, user string, clearedAt, at time.Time) *chatimv1.Event {
	ev := roomEnvelope(r.ID, r.Tenant, r.Type, ClearedEventID(r.ID, user, clearedAt), user, at)
	ev.Payload = &chatimv1.Event_HistoryCleared{HistoryCleared: &chatimv1.HistoryCleared{User: user, ClearedAt: timestamppb.New(clearedAt)}}
	return ev
}
