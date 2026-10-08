package effects_test

import (
	"errors"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

func TestMemberCountEventPublishesTheCurrentCountOncePerRoom(t *testing.T) {
	rg := newMemberRig(t)
	if e := rg.count.Effect(); e.Name != effects.MemberCountEventName || e.Delay != delay {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.MemberCountEventName, delay)
	}
	rg.join(t, "bob", "carol")
	c, err := rg.rooms.AddMemberCount(t.Context(), room, 2)
	if err != nil {
		t.Fatalf("AddMemberCount: %v", err)
	}
	recs := []work.Record{memberRec(room, "bob", 1), memberRec(room, "carol", 1), memberRec(room, "alice", 1)}
	if errs := rg.count.Effect().Run(t.Context(), recs); !allNil(errs, 3) {
		t.Fatalf("errs = %v", errs)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.MemberCountEventID(room, c.Ver)}) {
		t.Fatalf("stored = %v, want one count event at ver %d", got, c.Ver)
	}
	if subj := rg.js.Stored()[0].Subject; subj != "evt.acme.room.4242.member_count_changed" {
		t.Fatalf("subject = %q", subj)
	}
	events, err := rg.js.Events()
	if want := pbconv.MemberCountChanged(rg.stored(t), domain.MemberCount{Count: 3, Ver: 2}, "", memberNow); err != nil || !proto.Equal(events[0], want) {
		t.Fatalf("event = %v, %v; want %v", events, err, want)
	}
	if errs := rg.count.Effect().Run(t.Context(), recs[:1]); !allNil(errs, 1) || len(rg.js.Stored()) != 1 || rg.count.Republished() != 1 {
		t.Fatalf("errs %v, stored %d, republished %d; want the repeat deduplicated by id", errs, len(rg.js.Stored()), rg.count.Republished())
	}
}

func TestMemberCountEventDropsAGoneRoomAndRetriesWhenTheStoreFails(t *testing.T) {
	rg := newMemberRig(t)
	gone := []work.Record{memberRec(otherRoom, "bob", 1), memberRec(otherRoom, "carol", 1)}
	if errs := rg.count.Effect().Run(t.Context(), gone); !allNil(errs, 2) || rg.count.Dropped() != 2 || len(rg.js.Attempts()) != 0 {
		t.Fatalf("errs %v, dropped %d, attempts %d; want both records dropped", errs, rg.count.Dropped(), len(rg.js.Attempts()))
	}
	eff := built(effects.NewMemberCountEvent(
		effects.MemberEventDeps{Members: brokenMembers{}, Rooms: brokenStore{}, JS: &publishtest.JetStream{}},
		effects.MessageChangedConfig{SubjectRoot: "evt"},
	))(t)
	errs := eff.Effect().Run(t.Context(), []work.Record{memberRec(room, "bob", 1), memberRec(room, "carol", 1)})
	if len(errs) != 2 || !errors.Is(errs[0], errBoom) || !errors.Is(errs[1], errBoom) || eff.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want every record of the room retried", errs, eff.Dropped())
	}
}
