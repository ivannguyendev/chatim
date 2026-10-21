package wsclient

import (
	"slices"
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type versionedEvent func(other bool, ver uint64) *chatimv1.Event

func pick(other bool, first, second string) string {
	if other {
		return second
	}
	return first
}

func roomEvent(set func(ev *chatimv1.Event, ver uint64)) versionedEvent {
	return func(other bool, ver uint64) *chatimv1.Event {
		ev := &chatimv1.Event{RoomId: pick(other, "1", "2")}
		set(ev, ver)
		return ev
	}
}

func messageEvent(set func(ev *chatimv1.Event, user string, ver uint64)) versionedEvent {
	return func(other bool, ver uint64) *chatimv1.Event {
		ev := &chatimv1.Event{RoomId: "1", Seq: 5}
		if other {
			ev.Seq = 6
		}
		set(ev, "u1", ver)
		return ev
	}
}

func userEvent(set func(ev *chatimv1.Event, user string, ver uint64)) versionedEvent {
	return func(other bool, ver uint64) *chatimv1.Event {
		ev := &chatimv1.Event{RoomId: "1"}
		set(ev, pick(other, "u1", "u2"), ver)
		return ev
	}
}

func versionedKinds() map[string]versionedEvent {
	return map[string]versionedEvent{
		"member_added": userEvent(func(ev *chatimv1.Event, u string, v uint64) {
			ev.Payload = &chatimv1.Event_MemberAdded{MemberAdded: &chatimv1.MemberAdded{User: u, Ver: uint32(v)}}
		}),
		"member_removed": userEvent(func(ev *chatimv1.Event, u string, v uint64) {
			ev.Payload = &chatimv1.Event_MemberRemoved{MemberRemoved: &chatimv1.MemberRemoved{User: u, Ver: uint32(v)}}
		}),
		"member_role_changed": userEvent(func(ev *chatimv1.Event, u string, v uint64) {
			ev.Payload = &chatimv1.Event_MemberRoleChanged{MemberRoleChanged: &chatimv1.MemberRoleChanged{User: u, Ver: uint32(v)}}
		}),
		"member_priority_changed": userEvent(func(ev *chatimv1.Event, u string, v uint64) {
			ev.Payload = &chatimv1.Event_MemberPriorityChanged{MemberPriorityChanged: &chatimv1.MemberPriorityChanged{User: u, Ver: uint32(v)}}
		}),
		"read_updated": userEvent(func(ev *chatimv1.Event, u string, v uint64) {
			ev.Payload = &chatimv1.Event_ReadUpdated{ReadUpdated: &chatimv1.ReadUpdated{User: u, ReadVer: v}}
		}),
		"room_preferences_changed": userEvent(func(ev *chatimv1.Event, u string, v uint64) {
			ev.Payload = &chatimv1.Event_RoomPreferencesChanged{RoomPreferencesChanged: &chatimv1.RoomPreferencesChanged{
				User: u, State: &chatimv1.MyRoomState{PreferenceVer: v},
			}}
		}),
		"msg_pinned": roomEvent(func(ev *chatimv1.Event, v uint64) {
			ev.Payload = &chatimv1.Event_MessagePinned{MessagePinned: &chatimv1.MessagePinned{PinVer: v}}
		}),
		"msg_unpinned": roomEvent(func(ev *chatimv1.Event, v uint64) {
			ev.Payload = &chatimv1.Event_MessageUnpinned{MessageUnpinned: &chatimv1.MessageUnpinned{PinVer: v}}
		}),
		"room_updated": roomEvent(func(ev *chatimv1.Event, v uint64) {
			ev.Payload = &chatimv1.Event_RoomUpdated{RoomUpdated: &chatimv1.RoomUpdated{Room: &chatimv1.Room{InfoVer: v}}}
		}),
		"member_count_changed": roomEvent(func(ev *chatimv1.Event, v uint64) {
			ev.Payload = &chatimv1.Event_MemberCountChanged{MemberCountChanged: &chatimv1.MemberCountChanged{MemberCountVer: v}}
		}),
		"counts_changed reactions": messageEvent(func(ev *chatimv1.Event, _ string, v uint64) {
			ev.Payload = &chatimv1.Event_CountsChanged{CountsChanged: &chatimv1.CountsChanged{
				Counter: reactionsCounter, Reactions: &chatimv1.ReactionSummary{Ver: v},
			}}
		}),
		"counts_changed replies": messageEvent(func(ev *chatimv1.Event, _ string, v uint64) {
			ev.Payload = &chatimv1.Event_CountsChanged{CountsChanged: &chatimv1.CountsChanged{
				Counter: repliesCounter, ReplyCount: &chatimv1.ReplyCount{Ver: v},
			}}
		}),
		"msg_edited": messageEvent(func(ev *chatimv1.Event, _ string, v uint64) {
			ev.Payload = &chatimv1.Event_MessageEdited{MessageEdited: &chatimv1.MessageEdited{Ver: uint32(v)}}
		}),
		"msg_deleted": messageEvent(func(ev *chatimv1.Event, _ string, v uint64) {
			ev.Payload = &chatimv1.Event_MessageDeleted{MessageDeleted: &chatimv1.MessageDeleted{Ver: uint32(v)}}
		}),
		"reaction_changed": messageEvent(func(ev *chatimv1.Event, u string, v uint64) {
			ev.Payload = &chatimv1.Event_ReactionChanged{ReactionChanged: &chatimv1.ReactionChanged{User: u, Change: uint32(v)}}
		}),
		"bookmark_changed": messageEvent(func(ev *chatimv1.Event, u string, v uint64) {
			ev.Payload = &chatimv1.Event_BookmarkChanged{BookmarkChanged: &chatimv1.BookmarkChanged{User: u, Ver: uint32(v)}}
		}),
	}
}

func TestEveryVersionedKindDropsAnOlderOrEqualVersionOfItsOwnDocument(t *testing.T) {
	for name, build := range versionedKinds() {
		t.Run(name, func(t *testing.T) {
			a := NewApplier(0)
			got := verdicts(a, build(false, 3), build(false, 2), build(false, 3), build(true, 1), build(false, 4))
			want := []Verdict{VerdictDeliver, VerdictStale, VerdictStale, VerdictDeliver, VerdictDeliver}
			if !slices.Equal(got, want) {
				t.Fatalf("verdicts %v, want %v", got, want)
			}
		})
	}
}

func TestDocumentsSharingAVersionCounterGateEachOther(t *testing.T) {
	kinds := versionedKinds()
	pairs := [][2]string{
		{"member_added", "member_removed"},
		{"member_role_changed", "member_priority_changed"},
		{"msg_pinned", "msg_unpinned"},
		{"msg_edited", "msg_deleted"},
	}
	for _, pair := range pairs {
		a := NewApplier(0)
		got := verdicts(a, kinds[pair[0]](false, 2), kinds[pair[1]](false, 1))
		if !slices.Equal(got, []Verdict{VerdictDeliver, VerdictStale}) {
			t.Fatalf("%s v2 then %s v1: %v, want the second stale", pair[0], pair[1], got)
		}
	}
	a := NewApplier(0)
	got := verdicts(a, kinds["counts_changed reactions"](false, 5), kinds["counts_changed replies"](false, 1))
	if !slices.Equal(got, []Verdict{VerdictDeliver, VerdictDeliver}) {
		t.Fatalf("reaction counts v5 then reply counts v1: %v, want both delivered", got)
	}
}
