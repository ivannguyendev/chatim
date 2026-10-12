package publish_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type kindCase struct {
	ev      *chatimv1.Event
	subject string
}

func everyKind() map[string]kindCase {
	room := domain.Room{ID: roomA, Tenant: tenant, Type: domain.RoomGroup, Name: "team", CreatedBy: "alice", CreatedAt: sentAt, MemberCount: 1}
	m := domain.Message{Room: roomA, Seq: 7, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "hi", CID: "c-7", CreatedAt: sentAt}
	e := domain.Edit{Room: roomA, Seq: 7, Version: 2, Kind: domain.EditText, Tenant: tenant, By: "alice", Text: "new", At: sentAt}
	r := domain.Reaction{Room: roomA, Seq: 7, Tenant: tenant, User: "bob", Emoji: "👍", N: 1, At: sentAt}
	pin := domain.PinAction{Room: roomA, PV: 3, Tenant: tenant, Op: domain.PinOpPin, Seq: 7, By: "bob", At: sentAt}
	unpin := pin
	unpin.PV, unpin.Op = 4, domain.PinOpUnpin
	bob := domain.Member{Room: roomA, Tenant: tenant, User: "bob", Role: domain.RoleMember, State: domain.MemberActive, Ver: 1, UpdatedBy: "alice", UpdatedAt: sentAt}
	removed := bob.Next(domain.RoleMember, domain.MemberRemoved, 0, "r", "alice", sentAt)
	promoted := bob.Next(domain.RoleAdmin, domain.MemberActive, 0, "r", "alice", sentAt)
	promoted.PreviousState = domain.MemberActive
	bob2 := bob
	bob2.PreviousState = domain.MemberActive
	pinned := bob2.Next(domain.RoleMember, domain.MemberActive, 5, "r", "bob", sentAt)
	return map[string]kindCase{
		"msg_created":             {pbconv.MessageCreated(domain.RoomGroup, m), "evt.acme.message.101.msg_created"},
		"msg_edited":              {pbconv.MessageEdited(domain.RoomGroup, m, e), "evt.acme.message.101.msg_edited"},
		"msg_deleted":             {pbconv.MessageDeleted(domain.RoomGroup, m, e), "evt.acme.message.101.msg_deleted"},
		"reaction_changed":        {pbconv.ReactionChanged(domain.RoomGroup, r), "evt.acme.message.101.reaction_changed"},
		"counts_changed":          {pbconv.CountsChanged(domain.RoomGroup, m, sentAt), "evt.acme.message.101.counts_changed"},
		"room_created":            {pbconv.RoomCreated(room), "evt.acme.room.101.room_created"},
		"msg_pinned":              {pbconv.PinChanged(domain.RoomGroup, m, pin), "evt.acme.room.101.msg_pinned"},
		"msg_unpinned":            {pbconv.PinChanged(domain.RoomGroup, m, unpin), "evt.acme.room.101.msg_unpinned"},
		"member_count_changed":    {pbconv.MemberCountChanged(room, domain.MemberCount{Count: 2, Ver: 2}, "alice", sentAt), "evt.acme.room.101.member_count_changed"},
		"member_added":            {pbconv.MemberEvent(domain.RoomGroup, bob), "evt.acme.member.101.member_added"},
		"member_removed":          {pbconv.MemberEvent(domain.RoomGroup, removed), "evt.acme.member.101.member_removed"},
		"member_role_changed":     {pbconv.MemberEvent(domain.RoomGroup, promoted), "evt.acme.member.101.member_role_changed"},
		"member_priority_changed": {pbconv.MemberEvent(domain.RoomGroup, pinned), "evt.acme.member.101.member_priority_changed"},
		"read_updated":            {pbconv.ReadUpdated(room, "bob", domain.ReadPosition{Seq: 7, Ver: 2}, sentAt), "evt.acme.member.101.read_updated"},
		"message_hidden":          {pbconv.MessageHidden(room, "bob", 0, 7, sentAt), "evt.acme.member.101.message_hidden"},
		"history_cleared":         {pbconv.HistoryCleared(room, "bob", sentAt, sentAt), "evt.acme.member.101.history_cleared"},
		"bookmark_changed":        {pbconv.BookmarkChanged(room, domain.Bookmark{Room: roomA, Seq: 7, Tenant: tenant, User: "bob", On: true, Ver: 1, At: sentAt}), "evt.acme.member.101.bookmark_changed"},
	}
}

func TestEachKindGoesToTheSubjectOfItsData(t *testing.T) {
	kinds := everyKind()
	if len(kinds) != 17 {
		t.Fatalf("table has %d kinds, want 17", len(kinds))
	}
	for name, c := range kinds {
		msg, err := publish.Message("evt", roomA, c.ev)
		if err != nil {
			t.Fatalf("%s: Message: %v", name, err)
		}
		if msg.Subject != c.subject {
			t.Errorf("%s: subject %q, want %q", name, msg.Subject, c.subject)
		}
	}
	unknown := &chatimv1.Event{Id: "101-x", Tenant: tenant}
	if msg, err := publish.Message("evt", roomA, unknown); err == nil || msg != nil {
		t.Fatalf("Message(no payload) = %v, %v; want an error and nothing to publish", msg, err)
	}
	if subject, err := publish.SubjectFor("evt", tenant, roomA, "bogus"); !errors.Is(err, apperr.ErrInvalidArgument) || subject != "" {
		t.Fatalf("SubjectFor(bogus) = %q, %v; want ErrInvalidArgument", subject, err)
	}
}

func TestOnlyMessageCreatedIsMarked(t *testing.T) {
	for name, c := range everyKind() {
		key, ok := publish.MarkKey(roomA, c.ev)
		switch {
		case name == "msg_created" && (!ok || key != store.MsgKey{Room: roomA, Seq: 7}):
			t.Errorf("MarkKey(msg_created) = %v, %v; want {%d 0 7}, true", key, ok, roomA)
		case name != "msg_created" && ok:
			t.Errorf("MarkKey(%s) = %v, true; want no mark", name, key)
		}
	}
}
