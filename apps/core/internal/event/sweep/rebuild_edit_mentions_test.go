package sweep_test

import (
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func target(id string, at time.Time) []domain.MentionTarget {
	return []domain.MentionTarget{{Kind: domain.MentionUser, ID: id, At: at}}
}

func (f *fullRig) sendMentioning(t *testing.T, seq uint64, at time.Time, id string) {
	t.Helper()
	m := domain.Message{Room: room, Seq: seq, Tenant: tenant, From: "ann", CID: "m-1", Text: "v0", CreatedAt: at, Mentions: target(id, at)}
	if res := f.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("Insert = %+v", res[0])
	}
}

func (f *fullRig) fact(t *testing.T, ver uint32, kind domain.EditKind, text string, at time.Time, targets []domain.MentionTarget) domain.Edit {
	t.Helper()
	e := domain.Edit{Room: room, Seq: 1, Version: ver, Kind: kind, Tenant: tenant, By: "ann", Text: text, At: at, Mentions: targets}
	if err := f.edits.Append(t.Context(), e); err != nil {
		t.Fatalf("Append v%d: %v", ver, err)
	}
	return e
}

func (f *fullRig) projectFacts(t *testing.T, facts ...domain.Edit) {
	t.Helper()
	for _, e := range facts {
		if err := f.msgs.ApplyEdit(t.Context(), e); err != nil {
			t.Fatalf("ApplyEdit v%d: %v", e.Version, err)
		}
		if e.Kind == domain.EditDelete {
			if err := f.edits.PurgeText(t.Context(), store.MsgKey{Room: room, Seq: 1}, e.Version-1); err != nil {
				t.Fatalf("PurgeText: %v", err)
			}
		}
	}
}

func (f *fullRig) expectPrevious(t *testing.T, ver uint32, want []domain.MentionTarget) {
	t.Helper()
	ev := f.published(t, pbconv.MessageChangeEventID(room, 0, 1, ver))
	got := ev.GetMessageEdited().GetPreviousMentions()
	if d := ev.GetMessageDeleted(); d != nil {
		got = d.GetPreviousMentions()
	}
	if ev == nil || !proto.Equal(got, pbconv.MentionSet(domain.Mentions{Targets: want})) {
		t.Fatalf("v%d previous_mentions = %v, want %v", ver, got, want)
	}
}

func TestRepublishedEditCarriesTheTargetsBeforeTheSnapshot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFullRig(t, nil)
		old := time.Now().Add(-time.Minute)
		f.group(t, old, "ann")
		f.sendMentioning(t, 1, old, "minh")
		f.fact(t, 0, domain.EditOriginal, "v0", old, target("minh", old))
		v1 := f.fact(t, 1, domain.EditText, "v1", old.Add(time.Second), target("lan", old.Add(time.Second)))
		v2 := f.fact(t, 2, domain.EditText, "v2", old.Add(2*time.Second), target("an", old.Add(2*time.Second)))
		f.projectFacts(t, v1, v2)
		f.settle()
		ev := f.published(t, pbconv.MessageChangeEventID(room, 0, 1, 1))
		if ev.GetMessageEdited().GetVer() != 1 || ev.GetMessageEdited().GetMessage().GetVer() != 2 {
			t.Fatalf("v1 event = %v, want fact ver 1 with the v2 snapshot", ev)
		}
		f.expectPrevious(t, 1, target("lan", old.Add(time.Second)))
		f.expectPrevious(t, 2, target("lan", old.Add(time.Second)))
		expectPending(t, f.rig, store.StateEdits, 0)
		f.stop(t)
	})
}

func TestRepublishedChangeOfADeletedMessageCarriesTheDeleteFactTargets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFullRig(t, nil)
		old := time.Now().Add(-time.Minute)
		f.group(t, old, "ann")
		f.sendMentioning(t, 1, old, "minh")
		f.fact(t, 0, domain.EditOriginal, "v0", old, target("minh", old))
		v1 := f.fact(t, 1, domain.EditText, "v1", old.Add(time.Second), target("lan", old.Add(time.Second)))
		gone := f.fact(t, 2, domain.EditDelete, "", old.Add(2*time.Second), target("lan", old.Add(time.Second)))
		f.projectFacts(t, v1, gone)
		f.settle()
		f.expectPrevious(t, 1, target("lan", old.Add(time.Second)))
		f.expectPrevious(t, 2, target("lan", old.Add(time.Second)))
		expectPending(t, f.rig, store.StateEdits, 0)
		f.stop(t)
	})
}

func TestRepublishedMessageWithMentionsGoesToTheMentionData(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFullRig(t, nil)
		old := time.Now().Add(-time.Minute)
		f.group(t, old, "ann")
		f.sendMentioning(t, 1, old, "minh")
		f.send(t, room, 2, old)
		f.settle()
		subjects := map[string]string{}
		for _, m := range f.js.Stored() {
			subjects[publishtest.MsgID(m)] = m.Subject
		}
		if got := subjects[pbconv.MessageEventID(room, 0, 1)]; got != "evt.acme.mention.4242.msg_created" {
			t.Fatalf("seq 1 subject %q, want the mention data", got)
		}
		if got := subjects[pbconv.MessageEventID(room, 0, 2)]; got != "evt.acme.message.4242.msg_created" {
			t.Fatalf("seq 2 subject %q, want the message data", got)
		}
		expectPending(t, f.rig, store.StateMessages, 0)
		f.stop(t)
	})
}
