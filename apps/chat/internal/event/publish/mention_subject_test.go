package publish_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/chat/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/chat/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func mentioned(targets bool, all bool, deleted bool) domain.Message {
	m := domain.Message{Room: roomA, Seq: 7, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "hi @minh", CID: "c-7", CreatedAt: sentAt}
	if targets {
		m.Mentions = []domain.MentionTarget{{Kind: domain.MentionUser, ID: "minh", At: sentAt}}
	}
	m.MentionAll, m.Deleted = all, deleted
	if all {
		m.MentionAllAt = sentAt
	}
	return m
}

func TestMessageCreatedWithMentionsGoesToTheMentionData(t *testing.T) {
	cases := map[string]struct {
		m       domain.Message
		subject string
	}{
		"user targets":     {mentioned(true, false, false), "evt.acme.mention.101.msg_created"},
		"only @all":        {mentioned(false, true, false), "evt.acme.mention.101.msg_created"},
		"targets and @all": {mentioned(true, true, false), "evt.acme.mention.101.msg_created"},
		"no targets":       {mentioned(false, false, false), "evt.acme.message.101.msg_created"},
		"deleted snapshot": {mentioned(true, true, true), "evt.acme.message.101.msg_created"},
	}
	for name, c := range cases {
		msg, err := publish.Message("evt", roomA, pbconv.MessageCreated(domain.RoomGroup, c.m))
		if err != nil {
			t.Fatalf("%s: Message: %v", name, err)
		}
		if msg.Subject != c.subject {
			t.Errorf("%s: subject %q, want %q", name, msg.Subject, c.subject)
		}
	}
}

func TestOnlyMessageCreatedMovesToTheMentionData(t *testing.T) {
	m := mentioned(true, true, false)
	e := domain.Edit{Room: roomA, Seq: 7, Version: 2, Kind: domain.EditText, Tenant: tenant, By: "alice", Text: "hi @minh", At: sentAt}
	prev := m.MentionSet()
	cases := map[string]struct {
		ev      *chatimv1.Event
		subject string
	}{
		"msg_edited":     {pbconv.MessageEdited(domain.RoomGroup, m, e, prev), "evt.acme.message.101.msg_edited"},
		"msg_deleted":    {pbconv.MessageDeleted(domain.RoomGroup, m, e, prev), "evt.acme.message.101.msg_deleted"},
		"counts_changed": {pbconv.CountsChanged(domain.RoomGroup, m, sentAt), "evt.acme.message.101.counts_changed"},
		"msg_pinned":     {pbconv.PinChanged(domain.RoomGroup, m, domain.PinAction{Room: roomA, PV: 1, Tenant: tenant, Op: domain.PinOpPin, Seq: 7, By: "bob", At: sentAt}), "evt.acme.room.101.msg_pinned"},
	}
	for name, c := range cases {
		msg, err := publish.Message("evt", roomA, c.ev)
		if err != nil {
			t.Fatalf("%s: Message: %v", name, err)
		}
		if msg.Subject != c.subject {
			t.Errorf("%s: subject %q, want %q", name, msg.Subject, c.subject)
		}
	}
}

func TestSubjectForUsesTheMentionAndFileFlagsOnlyForMessageCreated(t *testing.T) {
	cases := map[string]struct {
		kind            string
		mentions, files bool
		subject         string
	}{
		"msg_created with mentions":        {"msg_created", true, false, "evt.acme.mention.101.msg_created"},
		"msg_created with files":           {"msg_created", false, true, "evt.acme.media.101.msg_created"},
		"msg_created with both":            {"msg_created", true, true, "evt.acme.mention.101.msg_created"},
		"msg_created with neither":         {"msg_created", false, false, "evt.acme.message.101.msg_created"},
		"reaction_changed with both flags": {"reaction_changed", true, true, "evt.acme.message.101.reaction_changed"},
		"msg_deleted with both flags":      {"msg_deleted", true, true, "evt.acme.message.101.msg_deleted"},
		"member_added with both flags":     {"member_added", true, true, "evt.acme.member.101.member_added"},
		"room_created with both flags":     {"room_created", true, true, "evt.acme.room.101.room_created"},
	}
	for name, c := range cases {
		if got, _ := publish.SubjectFor("evt", tenant, roomA, c.kind, c.mentions, c.files); got != c.subject {
			t.Errorf("%s = %q, want %q", name, got, c.subject)
		}
	}
}
