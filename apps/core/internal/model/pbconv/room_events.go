package pbconv

import (
	"strconv"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func RoomInfoEventID(room, infoVer uint64) string {
	return RoomID(room) + "-info-v" + strconv.FormatUint(infoVer, 10)
}

func PreferencesEventID(room uint64, user string, preferenceVer uint64) string {
	return RoomID(room) + "-pf-" + user + "-v" + strconv.FormatUint(preferenceVer, 10)
}

func RoomUpdated(r domain.Room) *chatimv1.Event {
	ev := roomEnvelope(r.ID, r.Tenant, r.Type, RoomInfoEventID(r.ID, r.InfoVer), r.UpdatedBy, r.UpdatedAt)
	ev.Payload = &chatimv1.Event_RoomUpdated{RoomUpdated: &chatimv1.RoomUpdated{Room: Room(r)}}
	return ev
}

func RoomPreferencesChanged(r domain.Room, m domain.Member, at time.Time) *chatimv1.Event {
	ev := roomEnvelope(r.ID, r.Tenant, r.Type, PreferencesEventID(r.ID, m.User, m.Preferences.Ver), m.User, at)
	ev.Payload = &chatimv1.Event_RoomPreferencesChanged{RoomPreferencesChanged: &chatimv1.RoomPreferencesChanged{User: m.User, State: MyRoomState(m)}}
	return ev
}
