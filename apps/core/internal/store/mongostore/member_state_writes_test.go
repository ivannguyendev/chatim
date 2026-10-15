package mongostore

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestMemberAndRoomWritesRaiseTheirBitInTheSameStage(t *testing.T) {
	m := domain.Member{Room: 9, User: "bob", Role: domain.RoleMember, State: domain.MemberActive, Ver: 1}
	next := m.Next(domain.RoleAdmin, domain.MemberActive, 2, "$req", "$alice", codecTime)
	rename := store.RoomInfoChange{Update: domain.RoomUpdate{Name: new("$Design"), Avatar: new("")}, By: "$alice", At: codecTime}
	for name, c := range map[string]struct {
		pipeline mongo.Pipeline
		field    string
		bit      store.StateBit
	}{
		"apply member":     {membershipUpdate(next), "ver", store.BitMember},
		"move read":        {readMoveUpdate(5, codecTime), "read_ver", store.BitReadPosition},
		"clear history":    {clearHistoryUpdate(codecTime), "cleared_at", store.BitHistoryCleared},
		"preferences":      {preferencePipeline([]preferenceField{{name: "pinned_at", value: codecTime}}, codecTime), "preference_ver", store.BitPreferences},
		"room info":        {roomInfoUpdate(rename), "info_ver", store.BitRoomInfo},
		"count increment":  {countIncrement(1), "member_count_ver", store.BitMemberCount},
		"count reset":      {countReset(4), "member_count_ver", store.BitMemberCount},
		"owners and count": {ownersUpdate(-1), "member_count_ver", store.BitMemberCount},
	} {
		set := onlySetStage(t, name, c.pipeline)
		if _, err := set.LookupErr(c.field); err != nil {
			t.Fatalf("%s: $set %s has no %s", name, set, c.field)
		}
		if bits := set.Lookup(stateField, "$bitOr").Array().Index(1).Int32(); bits != int32(c.bit) {
			t.Fatalf("%s: $bitOr %d, want %d", name, bits, c.bit)
		}
		if _, err := set.LookupErr(stateAtField, "$min"); err != nil {
			t.Fatalf("%s: $set %s has no %s $min", name, set, stateAtField)
		}
	}
}

func TestOwnersUpdateWithoutACountChangeRaisesNoBit(t *testing.T) {
	set := onlySetStage(t, "owners only", ownersUpdate(0))
	if _, err := set.LookupErr(stateField); err == nil {
		t.Fatalf("owners only: $set %s raises a bit", set)
	}
	if _, err := set.LookupErr("member_count"); err == nil {
		t.Fatalf("owners only: $set %s touches the count", set)
	}
}

func TestMemberWritesQuoteUserStrings(t *testing.T) {
	m := domain.Member{Room: 9, User: "bob", Role: domain.RoleMember, State: domain.MemberActive, Ver: 1}
	set := onlySetStage(t, "apply member", membershipUpdate(m.Next(domain.RoleAdmin, domain.MemberActive, 2, "$req", "$alice", codecTime)))
	for _, f := range []string{"role", "previous_role", "request_id", "updated_by"} {
		if _, err := set.LookupErr(f, "$literal"); err != nil {
			t.Fatalf("$set %s is not a $literal: %s", f, set.Lookup(f))
		}
	}
	info := onlySetStage(t, "room info", roomInfoUpdate(store.RoomInfoChange{Update: domain.RoomUpdate{Name: new("$x"), Avatar: new("$y")}, By: "$z", At: codecTime}))
	for _, f := range []string{"name", "avatar", "updated_by"} {
		if _, err := info.LookupErr(f, "$literal"); err != nil {
			t.Fatalf("room info $set %s is not a $literal: %s", f, info.Lookup(f))
		}
	}
}

func TestJoinRaisesBitOneOnlyOnTheChangingBranch(t *testing.T) {
	j := domain.Join{Room: 9, Tenant: "acme", RequestID: "req", By: "alice", At: codecTime}
	set := onlySetStage(t, "join", joinPipeline(j, 9, 0, "bob"))
	branches := set.Lookup(stateField, "$cond").Array()
	if kept := branches.Index(1).StringValue(); kept != "$"+stateField {
		t.Fatalf("join keeps %q on an active doc, want $%s", kept, stateField)
	}
	if bits := branches.Index(2).Document().Lookup("$bitOr").Array().Index(1).Int32(); bits != int32(store.BitMember) {
		t.Fatalf("join raises %d, want %d", bits, store.BitMember)
	}
}

func onlySetStage(t *testing.T, name string, p mongo.Pipeline) bson.Raw {
	t.Helper()
	if len(p) != 1 {
		t.Fatalf("%s: %d stages, want 1", name, len(p))
	}
	return marshalRaw(t, p[0]).Lookup("$set").Document()
}
