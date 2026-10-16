package projection_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const subjectRoot = "evt"

type kindCase struct {
	name     string
	set      func(ev *chatimv1.Event)
	listed   bool
	withSeq  bool
	isEdited bool
}

var kindCases = []kindCase{
	{name: "msg_created", listed: true, withSeq: true, set: func(ev *chatimv1.Event) {
		ev.Payload = &chatimv1.Event_MessageCreated{MessageCreated: &chatimv1.MessageCreated{}}
	}},
	{name: "msg_edited", listed: true, isEdited: true, set: func(ev *chatimv1.Event) {
		ev.Payload = &chatimv1.Event_MessageEdited{MessageEdited: &chatimv1.MessageEdited{}}
	}},
	{name: "msg_deleted", listed: true, isEdited: true, set: func(ev *chatimv1.Event) {
		ev.Payload = &chatimv1.Event_MessageDeleted{MessageDeleted: &chatimv1.MessageDeleted{}}
	}},
	{name: "reaction_changed", listed: true, set: func(ev *chatimv1.Event) {
		ev.Payload = &chatimv1.Event_ReactionChanged{ReactionChanged: &chatimv1.ReactionChanged{}}
	}},
	{name: "msg_pinned", listed: true, set: func(ev *chatimv1.Event) {
		ev.Payload = &chatimv1.Event_MessagePinned{MessagePinned: &chatimv1.MessagePinned{}}
	}},
	{name: "msg_unpinned", listed: true, set: func(ev *chatimv1.Event) {
		ev.Payload = &chatimv1.Event_MessageUnpinned{MessageUnpinned: &chatimv1.MessageUnpinned{}}
	}},
	{name: "member_added", listed: true, set: func(ev *chatimv1.Event) {
		ev.Payload = &chatimv1.Event_MemberAdded{MemberAdded: &chatimv1.MemberAdded{}}
	}},
	{name: "member_removed", listed: true, set: func(ev *chatimv1.Event) {
		ev.Payload = &chatimv1.Event_MemberRemoved{MemberRemoved: &chatimv1.MemberRemoved{}}
	}},
	{name: "member_role_changed", listed: true, set: func(ev *chatimv1.Event) {
		ev.Payload = &chatimv1.Event_MemberRoleChanged{MemberRoleChanged: &chatimv1.MemberRoleChanged{}}
	}},
	{name: "member_priority_changed", listed: true, set: func(ev *chatimv1.Event) {
		ev.Payload = &chatimv1.Event_MemberPriorityChanged{MemberPriorityChanged: &chatimv1.MemberPriorityChanged{}}
	}},
	{name: "bookmark_changed", listed: true, set: func(ev *chatimv1.Event) {
		ev.Payload = &chatimv1.Event_BookmarkChanged{BookmarkChanged: &chatimv1.BookmarkChanged{}}
	}},
	{name: "room_updated", listed: true, set: func(ev *chatimv1.Event) {
		ev.Payload = &chatimv1.Event_RoomUpdated{RoomUpdated: &chatimv1.RoomUpdated{}}
	}},
	{name: "room_preferences_changed", listed: true, set: func(ev *chatimv1.Event) {
		ev.Payload = &chatimv1.Event_RoomPreferencesChanged{RoomPreferencesChanged: &chatimv1.RoomPreferencesChanged{}}
	}},
	{name: "room_created", set: func(ev *chatimv1.Event) {
		ev.Payload = &chatimv1.Event_RoomCreated{RoomCreated: &chatimv1.RoomCreated{}}
	}},
	{name: "counts_changed", set: func(ev *chatimv1.Event) {
		ev.Payload = &chatimv1.Event_CountsChanged{CountsChanged: &chatimv1.CountsChanged{}}
	}},
	{name: "member_count_changed", set: func(ev *chatimv1.Event) {
		ev.Payload = &chatimv1.Event_MemberCountChanged{MemberCountChanged: &chatimv1.MemberCountChanged{}}
	}},
	{name: "read_updated", set: func(ev *chatimv1.Event) {
		ev.Payload = &chatimv1.Event_ReadUpdated{ReadUpdated: &chatimv1.ReadUpdated{}}
	}},
	{name: "message_hidden", set: func(ev *chatimv1.Event) {
		ev.Payload = &chatimv1.Event_MessageHidden{MessageHidden: &chatimv1.MessageHidden{}}
	}},
	{name: "history_cleared", set: func(ev *chatimv1.Event) {
		ev.Payload = &chatimv1.Event_HistoryCleared{HistoryCleared: &chatimv1.HistoryCleared{}}
	}},
}

func kindEvent(t *testing.T, c kindCase, thread uint64) *chatimv1.Event {
	t.Helper()
	ev := envelope(roomA, thread, 4, at(30))
	c.set(ev)
	return ev
}

func subscribed(filters []string, subject string) bool {
	for _, f := range filters {
		if subjectMatches(f, subject) {
			return true
		}
	}
	return false
}

func subjectMatches(filter, subject string) bool {
	fs, ss := strings.Split(filter, "."), strings.Split(subject, ".")
	for i, f := range fs {
		switch {
		case f == ">":
			return len(ss) > i
		case i >= len(ss):
			return false
		case f != "*" && f != ss[i]:
			return false
		}
	}
	return len(fs) == len(ss)
}

func TestRoomActivitySubscribesExactlyTheKindsThatTouchedActivity(t *testing.T) {
	filters := projection.NewRoomActivity(&fakeWriter{}).Subjects(subjectRoot)
	listed := 0
	for _, c := range kindCases {
		m, err := publish.Message(subjectRoot, roomA, kindEvent(t, c, 0))
		if err != nil {
			t.Fatalf("%s: publish.Message: %v", c.name, err)
		}
		if !strings.HasSuffix(m.Subject, "."+c.name) {
			t.Fatalf("%s: published on %s", c.name, m.Subject)
		}
		if got := subscribed(filters, m.Subject); got != c.listed {
			t.Errorf("%s on %s: subscribed %v, want %v", c.name, m.Subject, got, c.listed)
		}
		if c.listed {
			listed++
		}
	}
	if !slices.Contains(filters, "evt.*.member.*.room_preferences_changed") {
		t.Errorf("filters %v miss the literal room_preferences_changed subject", filters)
	}
	if len(filters) != listed {
		t.Errorf("filters %v: %d subjects, want one per listed kind (%d)", filters, len(filters), listed)
	}
}

func TestRoomActivityMapsEachListedKindToItsActivity(t *testing.T) {
	for _, c := range kindCases {
		if !c.listed {
			continue
		}
		for _, thread := range []uint64{0, 9} {
			w := &fakeWriter{}
			ev := decoded(t, kindEvent(t, c, thread))
			if errs := projection.NewRoomActivity(w).Handle(t.Context(), []projection.Event{ev}); errs[0] != nil {
				t.Fatalf("%s thread %d: Handle = %v", c.name, thread, errs[0])
			}
			want := store.Activity{Room: roomA, Thread: thread, At: at(30), Edited: c.isEdited && thread == 0}
			if c.withSeq {
				want.Seq = 4
			}
			if got := w.touched(); len(got) != 1 || len(got[0]) != 1 || got[0][0] != want {
				t.Errorf("%s thread %d: touched %v, want [[%v]]", c.name, thread, got, want)
			}
		}
	}
}

func TestRoomActivityDropsAKindItDoesNotSubscribe(t *testing.T) {
	w := &fakeWriter{}
	errs := projection.NewRoomActivity(w).Handle(t.Context(), []projection.Event{decoded(t, readUpdated(roomA, at(1)))})
	if len(errs) != 1 || !eventkit.Gone(errs[0]) {
		t.Fatalf("Handle(read_updated) = %v, want one error that drops the event", errs)
	}
	if len(w.touched()) != 0 {
		t.Errorf("touched %v for an unsubscribed kind, want no write", w.touched())
	}
}
