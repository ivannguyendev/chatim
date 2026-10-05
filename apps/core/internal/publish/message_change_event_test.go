package publish_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestMessageChangesGoToTheirOwnSubjectsWithoutAMark(t *testing.T) {
	m := domain.Message{Room: roomA, Seq: 7, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "new", CID: "c-7", CreatedAt: sentAt, Version: 2, EditedAt: sentAt}
	e := domain.Edit{Room: roomA, Seq: 7, Version: 2, Kind: domain.EditText, Tenant: tenant, By: "alice", Text: "new", At: sentAt}
	cases := map[string]struct {
		ev      *chatimv1.Event
		subject string
	}{
		"edited":  {pbconv.MessageEdited(domain.RoomGroup, m, e), "evt.acme.room.101.msg_edited"},
		"deleted": {pbconv.MessageDeleted(domain.RoomGroup, m, e), "evt.acme.room.101.msg_deleted"},
	}
	for name, c := range cases {
		msg, err := publish.Message("evt", roomA, c.ev)
		if err != nil {
			t.Fatalf("%s: Message: %v", name, err)
		}
		if msg.Subject != c.subject || publishtest.MsgID(msg) != "101-0-7-v2" {
			t.Fatalf("%s: subject %q msg id %q, want %s and 101-0-7-v2", name, msg.Subject, publishtest.MsgID(msg), c.subject)
		}
		if key, ok := publish.MarkKey(roomA, c.ev); ok {
			t.Fatalf("%s: MarkKey = %v, true; want no mark", name, key)
		}
	}
}
