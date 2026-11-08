package sweep_test

import (
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/chat/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/chat/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/chat/internal/store"
)

func (f *fullRig) sendShaped(t *testing.T, seq uint64, at time.Time, mentions, files bool) {
	t.Helper()
	m := domain.Message{Room: room, Seq: seq, Tenant: tenant, From: "ann", CID: "shaped-" + strconv.FormatUint(seq, 10), CreatedAt: at}
	if mentions {
		m.Text, m.Mentions = "hi @minh", target("minh", at)
	}
	if files {
		m.Attachments = []domain.Attachment{{Key: "acme/ann/20261108/x", Name: "x.png", Mime: "image/png", Size: 10}}
	}
	if !mentions && !files {
		m.Text = "hi"
	}
	if res := f.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("Insert seq %d = %+v", seq, res[0])
	}
}

func TestRepublishedMessageWithFilesGoesToTheMediaData(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFullRig(t, nil)
		old := time.Now().Add(-time.Minute)
		f.group(t, old, "ann")
		f.sendShaped(t, 1, old, false, true)
		f.sendShaped(t, 2, old, true, true)
		f.sendShaped(t, 3, old, false, false)
		f.settle()
		subjects := map[string]string{}
		for _, m := range f.js.Stored() {
			subjects[publishtest.MsgID(m)] = m.Subject
		}
		want := map[uint64]string{
			1: "evt.acme.media.4242.msg_created",
			2: "evt.acme.mention.4242.msg_created",
			3: "evt.acme.message.4242.msg_created",
		}
		for seq, subject := range want {
			if got := subjects[pbconv.MessageEventID(room, 0, seq)]; got != subject {
				t.Errorf("seq %d subject %q, want %q", seq, got, subject)
			}
		}
		expectPending(t, f.rig, store.StateMessages, 0)
		f.stop(t)
	})
}
