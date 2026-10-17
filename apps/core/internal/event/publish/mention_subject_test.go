package publish_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
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

func TestSubjectForUsesTheMentionFlagOnlyForMessageCreated(t *testing.T) {
	if got, _ := publish.SubjectFor("evt", tenant, roomA, "msg_created", true); got != "evt.acme.mention.101.msg_created" {
		t.Fatalf("msg_created with mentions = %q", got)
	}
	if got, _ := publish.SubjectFor("evt", tenant, roomA, "reaction_changed", true); got != "evt.acme.message.101.reaction_changed" {
		t.Fatalf("reaction_changed with the flag = %q", got)
	}
}
