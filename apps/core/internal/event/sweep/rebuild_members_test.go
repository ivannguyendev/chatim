package sweep_test

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestMemberWithTwoBitsGivesTwoEventsClearedOneBitEach(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFullRig(t, nil)
		rec := &recordingStates{EventStates: f.states}
		f.replaceStates(t, rec)
		old := time.Now().Add(-time.Minute)
		f.group(t, old, "ann")
		pos, moved, err := f.rooms.MarkRead(t.Context(), room, "ann", 3)
		if err != nil || !moved {
			t.Fatalf("MarkRead = %v, %v", moved, err)
		}
		f.settle()
		readID := pbconv.ReadPositionsEventID(room, []*chatimv1.ReadEntry{{User: "ann", ReadSeq: 3, ReadVer: pos.Ver}})
		expectEvents(t, f, pbconv.MemberEventID(room, "ann", 1), readID)
		ev := f.published(t, readID)
		if got := ev.GetReadPositions().GetEntries(); len(got) != 1 || got[0].GetUser() != "ann" || got[0].GetReadSeq() != 3 || got[0].GetReadVer() != pos.Ver {
			t.Fatalf("read_positions = %+v, want one entry for ann at read seq 3", ev)
		}
		want := []store.StateTarget{
			{Collection: store.StateMembers, Bit: store.BitMember, Room: room, User: "ann", Ver: 1},
			{Collection: store.StateMembers, Bit: store.BitReadPosition, Room: room, User: "ann", Ver: pos.Ver},
		}
		if got := clearedIn(rec, store.StateMembers); !slices.Equal(got, want) {
			t.Fatalf("member clears = %+v, want %+v", got, want)
		}
		expectPending(t, f.rig, store.StateMembers, 0)
		f.stop(t)
	})
}

func TestHistoryClearedEventFollowsTheCurrentMark(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFullRig(t, nil)
		old := time.Now().Add(-time.Minute)
		f.group(t, old, "ann")
		first, second := old.Add(time.Second), old.Add(2*time.Second)
		for _, at := range []time.Time{first, second} {
			if _, raised, err := f.rooms.ClearHistory(t.Context(), room, "ann", at); err != nil || !raised {
				t.Fatalf("ClearHistory(%v) = %v, %v", at, raised, err)
			}
		}
		f.settle()
		expectEvents(t, f, pbconv.ClearedEventID(room, "ann", second))
		if f.event(t, pbconv.ClearedEventID(room, "ann", first)) {
			t.Fatal("history_cleared published for a mark that is no longer current")
		}
		expectPending(t, f.rig, store.StateMembers, 0)
		f.stop(t)
	})
}

func TestPreferencesAndRemovalArePublishedFromTheCurrentDoc(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFullRig(t, nil)
		old := time.Now().Add(-time.Minute)
		f.group(t, old, "ann", "bob")
		pinned := true
		prefs, changed, err := f.rooms.SetPreferences(t.Context(), room, "bob", domain.PreferencesChange{Pinned: &pinned}, old)
		if err != nil || !changed {
			t.Fatalf("SetPreferences = %v, %v", changed, err)
		}
		found, err := f.rooms.MembersOf(t.Context(), room, []string{"bob"})
		if err != nil || len(found) != 1 {
			t.Fatalf("MembersOf = %+v, %v", found, err)
		}
		next := found[0].Next(found[0].Role, domain.MemberRemoved, 0, "rq-1", "ann", old.Add(time.Second))
		if ok, err := f.rooms.ApplyMember(t.Context(), found[0], next); err != nil || !ok {
			t.Fatalf("ApplyMember = %v, %v", ok, err)
		}
		f.settle()
		expectEvents(t, f, pbconv.PreferencesEventID(room, "bob", prefs.Ver), pbconv.MemberEventID(room, "bob", 2))
		if f.event(t, pbconv.MemberEventID(room, "bob", 1)) {
			t.Fatal("member_added published for a member version that is no longer current")
		}
		if ev := f.published(t, pbconv.MemberEventID(room, "bob", 2)); ev.GetMemberRemoved().GetUser() != "bob" {
			t.Fatalf("member event v2 = %+v, want member_removed", ev)
		}
		if ev := f.published(t, pbconv.PreferencesEventID(room, "bob", prefs.Ver)); ev.GetRoomPreferencesChanged().GetState().GetPinnedAt() == nil {
			t.Fatalf("room_preferences_changed = %+v, want pinned", ev)
		}
		expectPending(t, f.rig, store.StateMembers, 0)
		f.stop(t)
	})
}

func TestMemberOfAGoneRoomIsDroppedAndClearedAtItsVersions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFullRig(t, nil)
		at := time.Now().Add(-time.Minute)
		seeds := []store.StateTarget{
			{Collection: store.StateMembers, Bit: store.BitMember, Room: room, User: "ann", Ver: 3},
			{Collection: store.StateMembers, Bit: store.BitReadPosition, Room: room, User: "ann", Ver: 4},
			{Collection: store.StateMembers, Bit: store.BitHistoryCleared, Room: room, User: "ann", At: at},
		}
		for _, s := range seeds {
			if err := f.states.SeedState(t.Context(), s, at); err != nil {
				t.Fatalf("SeedState: %v", err)
			}
		}
		f.start()
		expectPending(t, f.rig, store.StateMembers, 0)
		if f.Dropped(store.StateMembers) != 3 || len(f.js.Attempts()) != 0 {
			t.Fatalf("dropped %d, attempts %d; want 3 and 0", f.Dropped(store.StateMembers), len(f.js.Attempts()))
		}
		f.stop(t)
	})
}
