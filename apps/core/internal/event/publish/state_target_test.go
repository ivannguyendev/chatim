package publish_test

import (
	"maps"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type targetCase struct {
	ev   *chatimv1.Event
	want store.StateTarget
}

func targetFixtures() (domain.Room, domain.Message, domain.Member) {
	room := domain.Room{ID: roomA, Tenant: tenant, Type: domain.RoomGroup, Name: "team", CreatedBy: "alice", CreatedAt: sentAt, MemberCount: 1, InfoVer: 3, UpdatedBy: "alice", UpdatedAt: sentAt}
	m := domain.Message{
		Room: roomA, Thread: 5, Seq: 7, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "hi", CID: "c-7", CreatedAt: sentAt,
		Reactions: domain.ReactionSummary{Counts: []domain.ReactionCount{{Emoji: "👍", Count: 1}}, Version: 9},
		Replies:   domain.ReplyCount{N: 2, Version: 11},
	}
	bob := domain.Member{Room: roomA, Tenant: tenant, User: "bob", Role: domain.RoleMember, State: domain.MemberActive, Ver: 4, UpdatedBy: "alice", UpdatedAt: sentAt, Preferences: domain.RoomPreferences{PinnedAt: sentAt, Ver: 6}}
	return room, m, bob
}

func memberTargetCases(room domain.Room, bob domain.Member) map[string]targetCase {
	removed := bob.Next(domain.RoleMember, domain.MemberRemoved, 0, "r", "alice", sentAt)
	active := bob
	active.PreviousState = domain.MemberActive
	promoted := active.Next(domain.RoleAdmin, domain.MemberActive, 0, "r", "alice", sentAt)
	promoted.PreviousState = domain.MemberActive
	raised := active.Next(domain.RoleMember, domain.MemberActive, 5, "r", "bob", sentAt)
	member := func(ver uint32) store.StateTarget {
		return store.StateTarget{Collection: store.StateMembers, Bit: store.BitMember, Room: roomA, User: "bob", Ver: uint64(ver)}
	}
	clearedAt := sentAt.Add(time.Minute)
	return map[string]targetCase{
		"member_added":            {pbconv.MemberEvent(domain.RoomGroup, bob), member(bob.Ver)},
		"member_removed":          {pbconv.MemberEvent(domain.RoomGroup, removed), member(removed.Ver)},
		"member_role_changed":     {pbconv.MemberEvent(domain.RoomGroup, promoted), member(promoted.Ver)},
		"member_priority_changed": {pbconv.MemberEvent(domain.RoomGroup, raised), member(raised.Ver)},
		"read_updated": {pbconv.ReadUpdated(room, "bob", domain.ReadPosition{Seq: 7, Ver: 8}, sentAt),
			store.StateTarget{Collection: store.StateMembers, Bit: store.BitReadPosition, Room: roomA, User: "bob", Ver: 8}},
		"history_cleared": {pbconv.HistoryCleared(room, "bob", clearedAt, sentAt),
			store.StateTarget{Collection: store.StateMembers, Bit: store.BitHistoryCleared, Room: roomA, User: "bob", At: clearedAt}},
		"room_preferences_changed": {pbconv.RoomPreferencesChanged(room, bob, sentAt),
			store.StateTarget{Collection: store.StateMembers, Bit: store.BitPreferences, Room: roomA, User: "bob", Ver: 6}},
		"message_hidden": {pbconv.MessageHidden(room, "bob", 5, 7, sentAt),
			store.StateTarget{Collection: store.StateHidden, Bit: store.BitHidden, Room: roomA, Thread: 5, Seq: 7, User: "bob"}},
	}
}

func messageTargetCases(room domain.Room, m domain.Message) map[string]targetCase {
	e := domain.Edit{Room: roomA, Thread: 5, Seq: 7, Version: 2, Kind: domain.EditText, Tenant: tenant, By: "alice", Text: "new", At: sentAt}
	r := domain.Reaction{Room: roomA, Thread: 5, Seq: 7, Tenant: tenant, User: "bob", Emoji: "👍", N: 4, At: sentAt}
	b := domain.Bookmark{Room: roomA, Thread: 5, Seq: 7, Tenant: tenant, User: "carol", On: true, Ver: 12, At: sentAt}
	pin := domain.PinAction{Room: roomA, PV: 3, Tenant: tenant, Op: domain.PinOpPin, Thread: 5, Seq: 7, By: "bob", At: sentAt}
	unpin := pin
	unpin.PV, unpin.Op = 13, domain.PinOpUnpin
	msg := store.StateTarget{Collection: store.StateMessages, Room: roomA, Thread: 5, Seq: 7}
	created, reactions, replies := msg, msg, msg
	created.Bit = store.BitMsgCreated
	reactions.Bit, reactions.Ver = store.BitReactionCounts, 9
	replies.Bit, replies.Ver = store.BitReplyCounts, 11
	edit := store.StateTarget{Collection: store.StateEdits, Bit: store.BitEditFact, Room: roomA, Thread: 5, Seq: 7, Ver: 2}
	pins := func(pv uint64) store.StateTarget {
		return store.StateTarget{Collection: store.StatePins, Bit: store.BitPinFact, Room: roomA, Seq: pv}
	}
	return map[string]targetCase{
		"msg_created":      {pbconv.MessageCreated(domain.RoomGroup, m), created},
		"msg_edited":       {pbconv.MessageEdited(domain.RoomGroup, m, e), edit},
		"msg_deleted":      {pbconv.MessageDeleted(domain.RoomGroup, m, e), edit},
		"counts_reactions": {pbconv.CountsChanged(domain.RoomGroup, m, sentAt), reactions},
		"counts_replies":   {pbconv.ReplyCountsChanged(domain.RoomGroup, m, sentAt), replies},
		"reaction_changed": {pbconv.ReactionChanged(domain.RoomGroup, r),
			store.StateTarget{Collection: store.StateInteractions, Bit: store.BitInteraction, Room: roomA, Thread: 5, Seq: 7, User: "bob", Kind: keys.ReactionKind, Ver: 4}},
		"bookmark_changed": {pbconv.BookmarkChanged(room, b),
			store.StateTarget{Collection: store.StateInteractions, Bit: store.BitInteraction, Room: roomA, Thread: 5, Seq: 7, User: "carol", Kind: keys.BookmarkKind, Ver: 12}},
		"msg_pinned":   {pbconv.PinChanged(domain.RoomGroup, m, pin), pins(3)},
		"msg_unpinned": {pbconv.PinChanged(domain.RoomGroup, m, unpin), pins(13)},
	}
}

func roomTargetCases(room domain.Room) map[string]targetCase {
	rooms := func(bit store.StateBit, ver uint64) store.StateTarget {
		return store.StateTarget{Collection: store.StateRooms, Bit: bit, Room: roomA, Ver: ver}
	}
	return map[string]targetCase{
		"room_created":         {pbconv.RoomCreated(room), rooms(store.BitRoomCreated, 0)},
		"member_count_changed": {pbconv.MemberCountChanged(room, domain.MemberCount{Count: 2, Ver: 14}, "alice", sentAt), rooms(store.BitMemberCount, 14)},
		"room_updated":         {pbconv.RoomUpdated(room), rooms(store.BitRoomInfo, 3)},
	}
}

func everyTarget() map[string]targetCase {
	room, m, bob := targetFixtures()
	out := roomTargetCases(room)
	maps.Copy(out, messageTargetCases(room, m))
	maps.Copy(out, memberTargetCases(room, bob))
	return out
}

func TestEveryEventKindMapsToThePendingStateItClears(t *testing.T) {
	cases := everyTarget()
	if len(cases) != 20 {
		t.Fatalf("table has %d cases, want 20 (19 kinds, counts_changed twice)", len(cases))
	}
	for name, c := range cases {
		if c.ev == nil || c.ev.GetPayload() == nil {
			t.Fatalf("%s: builder returned no event", name)
		}
		got, ok := publish.StateTargetOf(c.ev)
		if !ok {
			t.Errorf("%s: StateTargetOf = false, want %+v", name, c.want)
			continue
		}
		if !got.At.Equal(c.want.At) {
			t.Errorf("%s: At = %v, want %v", name, got.At, c.want.At)
		}
		got.At, c.want.At = time.Time{}, time.Time{}
		if got != c.want {
			t.Errorf("%s: StateTargetOf = %+v, want %+v", name, got, c.want)
		}
		if err := got.Validate(); err != nil {
			t.Errorf("%s: target fails Validate: %v", name, err)
		}
	}
}

func TestEventsWithoutAClearableStateMapToNothing(t *testing.T) {
	room, m, _ := targetFixtures()
	noRoom := pbconv.MessageCreated(domain.RoomGroup, m)
	noRoom.RoomId = ""
	badRoom := pbconv.MessageCreated(domain.RoomGroup, m)
	badRoom.RoomId = "x101"
	noSeq := m
	noSeq.Seq = 0
	views := pbconv.CountsChanged(domain.RoomGroup, m, sentAt)
	views.GetCountsChanged().Counter = "views"
	noClearTime := pbconv.HistoryCleared(room, "bob", sentAt, sentAt)
	noClearTime.GetHistoryCleared().ClearedAt = nil
	noUser := pbconv.ReadUpdated(room, "", domain.ReadPosition{Seq: 7, Ver: 2}, sentAt)
	cases := map[string]*chatimv1.Event{
		"no payload":         {Id: "101-x", Tenant: tenant, RoomId: "101"},
		"no room id":         noRoom,
		"bad room id":        badRoom,
		"zero room":          {Id: "0-0-7", RoomId: "0", Seq: 7, Payload: &chatimv1.Event_MessageCreated{MessageCreated: &chatimv1.MessageCreated{}}},
		"no seq":             pbconv.MessageCreated(domain.RoomGroup, noSeq),
		"unknown counter":    views,
		"no cleared_at":      noClearTime,
		"no reader":          noUser,
		"edit without a ver": pbconv.MessageEdited(domain.RoomGroup, m, domain.Edit{Room: roomA, Thread: 5, Seq: 7}),
	}
	for name, ev := range cases {
		if got, ok := publish.StateTargetOf(ev); ok {
			t.Errorf("%s: StateTargetOf = %+v, true; want false", name, got)
		}
	}
}
