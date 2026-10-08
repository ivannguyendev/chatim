package pbconv

import (
	"strconv"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func MemberCountEventID(room, ver uint64) string {
	return RoomID(room) + "-members-v" + strconv.FormatUint(ver, 10)
}

func MemberCountChanged(r domain.Room, c domain.MemberCount, actor string, at time.Time) *chatimv1.Event {
	ev := roomEnvelope(r.ID, r.Tenant, r.Type, MemberCountEventID(r.ID, c.Ver), actor, at)
	ev.Payload = &chatimv1.Event_MemberCountChanged{MemberCountChanged: &chatimv1.MemberCountChanged{
		MemberCount: memberCount(c.Count), MemberCountVer: c.Ver,
	}}
	return ev
}
