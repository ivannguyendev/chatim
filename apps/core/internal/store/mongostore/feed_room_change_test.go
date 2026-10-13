package mongostore

import (
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func roomUpdate(t *testing.T, id any, fields bson.D) changeDoc {
	t.Helper()
	ev := reactionUpdate(t, id, fields)
	ev.NS.Coll = roomsCollection
	return ev
}

func TestDecodeChangeReadsRoomInfoUpdatesFromTheKeyAndInfoVer(t *testing.T) {
	want := domain.Room{ID: 7_340_000_009, InfoVer: 3}
	for name, v := range map[string]any{"int32": int32(3), "int64": int64(3), "double": float64(3)} {
		got, err := decodeChange(roomUpdate(t, int64(7_340_000_009), bson.D{{Key: "name", Value: "Kho"}, {Key: "info_ver", Value: v}}))
		if err != nil || got.Kind != store.RoomChanged || got.Room != want || !got.CommittedAt.Equal(codecTime) || got.Member.User != "" {
			t.Fatalf("%s: room update change = %+v, %v; want %+v", name, got, err, want)
		}
	}
}

func TestDecodeChangeRejectsBrokenRoomUpdates(t *testing.T) {
	cases := map[string]changeDoc{
		"no info ver":       roomUpdate(t, int64(7), bson.D{{Key: "member_count", Value: int64(3)}}),
		"info ver 0":        roomUpdate(t, int64(7), bson.D{{Key: "info_ver", Value: int64(0)}}),
		"string info ver":   roomUpdate(t, int64(7), bson.D{{Key: "info_ver", Value: "2"}}),
		"room 0":            roomUpdate(t, int64(0), bson.D{{Key: "info_ver", Value: int64(2)}}),
		"negative room":     roomUpdate(t, int64(-4), bson.D{{Key: "info_ver", Value: int64(2)}}),
		"binary room id":    roomUpdate(t, keys.Msg(1, 0, 1), bson.D{{Key: "info_ver", Value: int64(2)}}),
		"string room id":    roomUpdate(t, "7", bson.D{{Key: "info_ver", Value: int64(2)}}),
		"no updated fields": roomUpdate(t, int64(7), bson.D{}),
	}
	for name, ev := range cases {
		if _, err := decodeChange(ev); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodeChange = %v, want errCorrupt", name, err)
		}
	}
}

func TestDecodeChangeReadsPreferenceUpdates(t *testing.T) {
	id := keys.Member(7_340_000_001, "bob")
	fields := bson.D{{Key: "pinned_at", Value: codecTime}, {Key: "preference_ver", Value: int64(4)}, {Key: "last_change_at", Value: codecTime}}
	got, err := decodeChange(memberUpdate(t, id, fields))
	want := domain.Member{Room: 7_340_000_001, User: "bob", Preferences: domain.RoomPreferences{Ver: 4}}
	if err != nil || got.Kind != store.PreferenceChanged || got.Member != want {
		t.Fatalf("preference change = %+v, %v; want %+v", got, err, want)
	}
	if _, err := decodeChange(memberUpdate(t, id, bson.D{{Key: "preference_ver", Value: int64(0)}})); !errors.Is(err, errCorrupt) {
		t.Fatalf("preference ver 0: decodeChange = %v, want errCorrupt", err)
	}
	got, err = decodeChange(memberUpdate(t, id, bson.D{{Key: "ver", Value: int64(5)}, {Key: "preference_ver", Value: int64(0)}}))
	if err != nil || got.Kind != store.MemberChanged || got.Member.Ver != 5 {
		t.Fatalf("rejoin change = %+v, %v; want a member change at ver 5", got, err)
	}
}
