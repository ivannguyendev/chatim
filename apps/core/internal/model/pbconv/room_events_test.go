package pbconv_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestRoomSettingEventIDsAreNaturalKeys(t *testing.T) {
	if got := pbconv.RoomInfoEventID(777, 3); got != "777-info-v3" {
		t.Errorf("RoomInfoEventID = %q, want 777-info-v3", got)
	}
	if got := pbconv.PreferencesEventID(777, "minh", 12); got != "777-pf-minh-v12" {
		t.Errorf("PreferencesEventID = %q, want 777-pf-minh-v12", got)
	}
}

func TestRoomUpdatedCarriesTheRoomAtItsInfoVer(t *testing.T) {
	r := groupRoom()
	r.Name, r.Avatar, r.InfoVer, r.UpdatedBy, r.UpdatedAt, r.MemberCount = "Kho", "files/av/1.png", 4, "alice", changedAt, 3
	want := memberEnvelope("9007199254740993-info-v4", "alice")
	want.Payload = &chatimv1.Event_RoomUpdated{RoomUpdated: &chatimv1.RoomUpdated{Room: pbconv.Room(r)}}
	got := pbconv.RoomUpdated(r)
	if !proto.Equal(got, want) || got.GetRoomUpdated().GetRoom().GetName() != "Kho" || got.GetRoomUpdated().GetRoom().GetInfoVer() != 4 {
		t.Fatalf("RoomUpdated = %v, want %v", got, want)
	}
}

func TestRoomPreferencesChangedCarriesTheFullStateOfTheUser(t *testing.T) {
	m := activeMember()
	m.Preferences = domain.RoomPreferences{PinnedAt: sentAt, MutedUntil: changedAt, Ver: 7}
	want := memberEnvelope("9007199254740993-pf-bob-v7", "bob")
	want.Payload = &chatimv1.Event_RoomPreferencesChanged{RoomPreferencesChanged: &chatimv1.RoomPreferencesChanged{User: "bob", State: pbconv.MyRoomState(m)}}
	got := pbconv.RoomPreferencesChanged(groupRoom(), m, changedAt)
	state := got.GetRoomPreferencesChanged().GetState()
	if !proto.Equal(got, want) || state.GetPreferenceVer() != 7 || !proto.Equal(state.GetPinnedAt(), timestamppb.New(sentAt)) || state.GetArchivedAt() != nil {
		t.Fatalf("RoomPreferencesChanged = %v, want %v", got, want)
	}
}
