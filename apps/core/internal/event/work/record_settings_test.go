package work_test

import (
	"errors"
	"math"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestRoomAndPreferenceChangesBecomeRecordsWithNaturalIDs(t *testing.T) {
	info := work.RecordOf(store.Change{Kind: store.RoomChanged, Room: domain.Room{ID: 777, InfoVer: 3}, CommittedAt: committed})
	if info.Room != 777 || info.Version != 3 || info.User != "" || info.ID() != "u:777-info-v3" {
		t.Fatalf("room change record = %+v id %q, want room 777 at info ver 3 and id u:777-info-v3", info, info.ID())
	}
	m := domain.Member{Room: 777, User: "minh", Preferences: domain.RoomPreferences{Ver: 5}}
	pref := work.RecordOf(store.Change{Kind: store.PreferenceChanged, Member: m, CommittedAt: committed})
	if pref.Room != 777 || pref.Version != 5 || pref.User != "minh" || pref.ID() != "f:777-pf-minh-v5" {
		t.Fatalf("preference record = %+v id %q, want minh at ver 5 and id f:777-pf-minh-v5", pref, pref.ID())
	}
	huge := work.RecordOf(store.Change{Kind: store.RoomChanged, Room: domain.Room{ID: 777, InfoVer: math.MaxUint32 + 9}})
	if huge.Version != math.MaxUint32 {
		t.Fatalf("huge info ver record version = %d, want it held at MaxUint32", huge.Version)
	}
}

func TestRoomChangeRecordsCarryNoUserAndPreferenceRecordsNeedOne(t *testing.T) {
	info := work.Record{Kind: store.RoomChanged, Room: 777, Version: 2, CommittedAt: committed}
	b := work.Encode(info)
	if got, err := work.Decode(b); len(b) != work.RecordSize || err != nil || got != info {
		t.Fatalf("Decode(Encode(room change)) = %+v, %v; want %+v", got, err, info)
	}
	if _, err := work.Decode(append(b, 1, 'a')); !errors.Is(err, work.ErrBadRecord) || errors.Is(err, work.ErrUnknownKind) {
		t.Fatalf("room change with a user: Decode = %v, want a malformed record", err)
	}
	pref := work.Record{Kind: store.PreferenceChanged, Room: 777, Version: 2, User: "minh", CommittedAt: committed}
	if got, err := work.Decode(work.Encode(pref)); err != nil || got != pref {
		t.Fatalf("Decode(Encode(preference)) = %+v, %v; want %+v", got, err, pref)
	}
	pref.User = ""
	if _, err := work.Decode(work.Encode(pref)); !errors.Is(err, work.ErrBadRecord) || errors.Is(err, work.ErrUnknownKind) {
		t.Fatalf("preference without a user: Decode = %v, want a malformed record", err)
	}
}
