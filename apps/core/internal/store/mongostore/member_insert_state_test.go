package mongostore

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestRoomInsertsCarryTheirFlagsAtTheCreationTime(t *testing.T) {
	r := domain.Room{ID: 9, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: codecTime}
	for name, c := range map[string]struct {
		created bool
		want    int32
	}{
		"insert room": {false, int32(store.BitRoomCreated)},
		"create":      {true, int32(store.BitRoomCreated | store.BitMemberCount)},
	} {
		doc, err := roomInsert(r, c.created)
		if err != nil {
			t.Fatalf("%s: roomInsert: %v", name, err)
		}
		raw := marshalRaw(t, doc)
		if got := raw.Lookup(stateField).Int32(); got != c.want {
			t.Fatalf("%s: %s = %d, want %d", name, stateField, got, c.want)
		}
		if got := raw.Lookup(stateAtField).Time(); !got.Equal(codecTime) {
			t.Fatalf("%s: %s = %v, want %v", name, stateAtField, got, codecTime)
		}
	}
	founder, err := founderInsert(r, domain.Member{Room: 9, Tenant: "acme", User: "alice", Role: domain.RoleOwner, JoinedAt: codecTime}, codecTime)
	if err != nil {
		t.Fatalf("founderInsert: %v", err)
	}
	assertFlagAt(t, "founder", marshalRaw(t, founder), codecTime)
}
