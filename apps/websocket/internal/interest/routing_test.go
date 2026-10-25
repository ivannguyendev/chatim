package interest

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/feed"
)

type audienceFixture struct {
	*fixture
	sinks map[string]*fakeSink
}

func newAudience(t *testing.T) *audienceFixture {
	f := newFixture(t)
	a := &audienceFixture{fixture: f, sinks: map[string]*fakeSink{}}
	va1, s := f.connect("t1", "va", 1)
	a.sinks["va1"] = s
	_, a.sinks["va2"] = f.connect("t1", "va")
	_, a.sinks["mb"] = f.connect("t1", "mb", 1)
	_, a.sinks["ow"] = f.connect("t1", "ow", 1)
	_, a.sinks["out"] = f.connect("t1", "out", 2)
	_, a.sinks["t2mb"] = f.connect("t2", "mb", 1)
	_, a.sinks["t2ow"] = f.connect("t2", "ow", 1)
	va1.Open("1")
	for _, s := range a.sinks {
		s.take()
	}
	return a
}

func (a *audienceFixture) receivers() []string {
	var got []string
	for name, s := range a.sinks {
		frames := s.take()
		if len(frames) > 1 {
			a.t.Fatalf("%s got %d frames for one event: %v", name, len(frames), frames)
		}
		if len(frames) == 1 {
			got = append(got, name)
		}
	}
	slices.Sort(got)
	return got
}

func TestEachKindReachesOnlyItsAudience(t *testing.T) {
	everyMember := []string{"mb", "ow", "va1", "va2"}
	viewers := []string{"va1"}
	owner := []string{"ow"}
	cases := map[string][]string{
		"msg_created": everyMember, "msg_edited": everyMember, "msg_deleted": everyMember,
		"room_created": everyMember, "room_updated": everyMember,
		"member_role_changed": viewers, "member_priority_changed": viewers, "member_count_changed": viewers,
		"reaction_changed": viewers, "counts_changed": viewers, "msg_pinned": viewers, "msg_unpinned": viewers,
		"message_hidden": owner, "history_cleared": owner, "bookmark_changed": owner,
		"room_preferences_changed": owner,
	}
	for kind, want := range cases {
		t.Run(kind, func(t *testing.T) {
			run(t, func(t *testing.T) {
				a := newAudience(t)
				a.route(event(kind, "t1", 1, "ow", 3))
				if got := a.receivers(); !slices.Equal(got, want) {
					t.Fatalf("%s reached %v, want %v", kind, got, want)
				}
			})
		})
	}
}

func TestMentionDataKindRoutesLikeAMessage(t *testing.T) {
	run(t, func(t *testing.T) {
		a := newAudience(t)
		env := msg("t1", 1)
		env.DataKind = "mention"
		a.route(env)
		if got := a.receivers(); !slices.Equal(got, []string{"mb", "ow", "va1", "va2"}) {
			t.Fatalf("mention msg_created reached %v", got)
		}
	})
}

func TestUnknownAndMalformedEventsAreDroppedAndCounted(t *testing.T) {
	run(t, func(t *testing.T) {
		a := newAudience(t)
		unknown := msg("t1", 1)
		unknown.Kind = "typing_started"
		a.route(unknown)
		badRoom := msg("t1", 1)
		badRoom.Room = "x1"
		wrongData := msg("t1", 1)
		wrongData.DataKind = "member"
		garbage := event("history_cleared", "t1", 1, "ow", 0)
		garbage.Data = []byte{0xff, 0xff}
		otherTenantPayload := event("history_cleared", "t2", 1, "ow", 0)
		otherTenantPayload.Tenant = "t1"
		otherRoomPayload := event("history_cleared", "t1", 2, "ow", 0)
		otherRoomPayload.Room = "1"
		wrongPayload := event("history_cleared", "t1", 1, "ow", 0)
		wrongPayload.Kind = "message_hidden"
		noUser := event("history_cleared", "t1", 1, "", 0)
		for _, env := range []feed.Envelope{badRoom, wrongData, garbage, otherTenantPayload, otherRoomPayload, wrongPayload, noUser} {
			a.route(env)
		}
		if got := a.receivers(); len(got) != 0 {
			t.Fatalf("dropped events reached %v", got)
		}
		if a.table.stats.dropped[dropUnknownKind].Load() != 1 || a.table.stats.dropped[dropMalformed].Load() != 7 {
			t.Fatalf("dropped unknown %d malformed %d, want 1 and 7",
				a.table.stats.dropped[dropUnknownKind].Load(), a.table.stats.dropped[dropMalformed].Load())
		}
	})
}

func TestTheSameUserIDInTwoTenantsNeverSharesEvents(t *testing.T) {
	run(t, func(t *testing.T) {
		a := newAudience(t)
		a.route(event("history_cleared", "t2", 1, "ow", 0))
		if got := a.receivers(); !slices.Equal(got, []string{"t2ow"}) {
			t.Fatalf("t2 private event reached %v", got)
		}
		a.route(msg("t2", 1))
		if got := a.receivers(); !slices.Equal(got, []string{"t2mb", "t2ow"}) {
			t.Fatalf("t2 message reached %v", got)
		}
		a.route(added("t2", 5, "ow", 2))
		a.route(msg("t1", 5))
		if got := a.receivers(); !slices.Equal(got, []string{"t2ow"}) {
			t.Fatalf("after t2/ow joined room 5, events reached %v; t1 room 5 must reach nobody", got)
		}
	})
}

func TestRoomCreatedBeforeTheMemberIsKnownReachesNobodyAndMemberAddedBringsTheRoom(t *testing.T) {
	run(t, func(t *testing.T) {
		a := newAudience(t)
		a.route(event("room_created", "t1", 9, "", 0))
		if got := a.receivers(); len(got) != 0 {
			t.Fatalf("room_created of an unknown room reached %v", got)
		}
		a.route(added("t1", 9, "mb", 1))
		if got := a.receivers(); !slices.Equal(got, []string{"mb"}) {
			t.Fatalf("member_added reached %v, want the added user", got)
		}
		a.route(msg("t1", 9))
		if got := a.receivers(); !slices.Equal(got, []string{"mb"}) {
			t.Fatalf("message of the new room reached %v", got)
		}
	})
}

func TestMemberChangesReachTheUserConcernedAndTheViewersOnce(t *testing.T) {
	run(t, func(t *testing.T) {
		a := newAudience(t)
		a.route(added("t1", 1, "out", 1))
		if got := a.receivers(); !slices.Equal(got, []string{"out", "va1"}) {
			t.Fatalf("member_added reached %v", got)
		}
		a.route(removed("t1", 1, "va", 2))
		if got := a.sinks["va1"].take(); !slices.Equal(got, []string{"event member_removed/t1/1/va/v2", "closed 1 removed"}) {
			t.Fatalf("removed viewer frames = %v", got)
		}
		if got := a.receivers(); !slices.Equal(got, []string{"va2"}) {
			t.Fatalf("member_removed reached %v besides the viewer", got)
		}
		a.route(msg("t1", 1))
		a.route(event("reaction_changed", "t1", 1, "", 0))
		if got := a.receivers(); !slices.Equal(got, []string{"mb", "out", "ow"}) {
			t.Fatalf("after removal a message reached %v", got)
		}
		if a.table.stats.delivered[levelOwner].Load() == 0 || a.table.stats.delivered[levelOpen].Load() == 0 || a.table.stats.delivered[levelMember].Load() == 0 {
			t.Fatal("delivery counters did not move")
		}
	})
}
