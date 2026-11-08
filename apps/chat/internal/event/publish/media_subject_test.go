package publish_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/chat/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/chat/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func withFiles(m domain.Message) domain.Message {
	m.Attachments = []domain.Attachment{{Key: "acme/alice/20261108/x", Name: "x.png", Mime: "image/png", Size: 10}}
	return m
}

func system(m domain.Message) domain.Message {
	m.Kind, m.SkipUnread, m.Meta = domain.KindSystem, true, []byte(`{"event":"member_left"}`)
	return m
}

func TestMessageCreatedRoutesByMentionsThenFiles(t *testing.T) {
	cases := map[string]struct {
		m       domain.Message
		subject string
	}{
		"text":              {mentioned(false, false, false), "evt.acme.message.101.msg_created"},
		"mention":           {mentioned(true, false, false), "evt.acme.mention.101.msg_created"},
		"media":             {withFiles(mentioned(false, false, false)), "evt.acme.media.101.msg_created"},
		"mention and media": {withFiles(mentioned(true, true, false)), "evt.acme.mention.101.msg_created"},
		"deleted media":     {withFiles(mentioned(false, false, true)), "evt.acme.message.101.msg_created"},
		"deleted both":      {withFiles(mentioned(true, true, true)), "evt.acme.message.101.msg_created"},
		"system":            {system(mentioned(false, false, false)), "evt.acme.message.101.msg_created"},
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

func TestOnlyMessageCreatedMovesToTheMediaData(t *testing.T) {
	m := withFiles(mentioned(false, false, false))
	e := domain.Edit{Room: roomA, Seq: 7, Version: 2, Kind: domain.EditText, Tenant: tenant, By: "alice", Text: "hi", At: sentAt}
	cases := map[string]struct {
		ev      *chatimv1.Event
		subject string
	}{
		"msg_edited":     {pbconv.MessageEdited(domain.RoomGroup, m, e, m.MentionSet()), "evt.acme.message.101.msg_edited"},
		"msg_deleted":    {pbconv.MessageDeleted(domain.RoomGroup, m, e, m.MentionSet()), "evt.acme.message.101.msg_deleted"},
		"counts_changed": {pbconv.CountsChanged(domain.RoomGroup, m, sentAt), "evt.acme.message.101.counts_changed"},
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
