package effects_test

import (
	"errors"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestMemberEventDeclaresItsPolicy(t *testing.T) {
	e := newMemberRig(t).member.Effect()
	if e.Name != effects.MemberEventName || e.Delay != delay || e.Run == nil {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.MemberEventName, delay)
	}
}

func TestMemberEventRepublishesTheCurrentDoc(t *testing.T) {
	rg := newMemberRig(t)
	rg.join(t, "bob")
	doc := rg.change(t, "bob", domain.RoleAdmin, domain.MemberActive)
	if errs := rg.member.Effect().Run(t.Context(), []work.Record{memberRec(room, "bob", doc.Ver)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.MemberEventID(room, "bob", 2)}) {
		t.Fatalf("stored = %v", got)
	}
	if subj := rg.js.Stored()[0].Subject; subj != "evt.acme.member.4242.member_role_changed" {
		t.Fatalf("subject = %q", subj)
	}
	events, err := rg.js.Events()
	if want := pbconv.MemberEvent(domain.RoomGroup, doc); err != nil || !proto.Equal(events[0], want) {
		t.Fatalf("event = %v, %v; want the fast path event %v", events, err, want)
	}
	if rg.member.Republished() != 1 || rg.member.Dropped() != 0 {
		t.Fatalf("republished %d, dropped %d; want 1 and 0", rg.member.Republished(), rg.member.Dropped())
	}
}

func TestMemberEventAnnouncesMembersCreatedWithTheRoom(t *testing.T) {
	rg := newMemberRig(t)
	if errs := rg.member.Effect().Run(t.Context(), []work.Record{memberRec(room, "alice", 1)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	events, err := rg.js.Events()
	if err != nil || len(events) != 1 || events[0].GetMemberAdded().GetUser() != "alice" || events[0].GetId() != pbconv.MemberEventID(room, "alice", 1) {
		t.Fatalf("events = %v, %v; want member_added of the creator", events, err)
	}
}

func TestMemberEventSkipsANewerDocAndRetriesAnOlderOne(t *testing.T) {
	rg := newMemberRig(t)
	rg.join(t, "bob")
	rg.change(t, "bob", domain.RoleAdmin, domain.MemberActive)
	errs := rg.member.Effect().Run(t.Context(), []work.Record{memberRec(room, "bob", 1), memberRec(room, "bob", 3), memberRec(room, "bob", 2)})
	if len(errs) != 3 || errs[0] != nil || !errors.Is(errs[1], store.ErrStaleRead) || errs[2] != nil {
		t.Fatalf("errs = %v, want only the change ahead of the read retried", errs)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.MemberEventID(room, "bob", 2)}) || rg.member.Dropped() != 0 {
		t.Fatalf("stored %v, dropped %d; want only ver 2", got, rg.member.Dropped())
	}
}

func TestMemberEventDropsWhatIsGoneAndRetriesWhenTheStoreFails(t *testing.T) {
	rg := newMemberRig(t)
	if errs := rg.member.Effect().Run(t.Context(), []work.Record{memberRec(room, "carol", 1), memberRec(otherRoom, "alice", 1)}); !allNil(errs, 2) {
		t.Fatalf("errs = %v, want nil so dropped records are not retried", errs)
	}
	if len(rg.js.Attempts()) != 0 || rg.member.Dropped() != 2 {
		t.Fatalf("attempts %d, dropped %d; want 0 and 2 (no doc, no room)", len(rg.js.Attempts()), rg.member.Dropped())
	}
	eff := built(effects.NewMemberEvent(
		effects.MemberEventDeps{Members: brokenMembers{}, Rooms: brokenStore{}, JS: &publishtest.JetStream{}},
		effects.MessageChangedConfig{SubjectRoot: "evt"},
	))(t)
	if errs := eff.Effect().Run(t.Context(), []work.Record{memberRec(room, "bob", 1)}); len(errs) != 1 || !errors.Is(errs[0], errBoom) || eff.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want a retryable failure", errs, eff.Dropped())
	}
}
