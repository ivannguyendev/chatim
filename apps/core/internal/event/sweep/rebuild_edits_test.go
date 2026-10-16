package sweep_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/sweep"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

func (f *fullRig) appendEdit(t *testing.T, seq uint64, ver uint32, kind domain.EditKind, text string, at time.Time) {
	t.Helper()
	e := domain.Edit{Room: room, Seq: seq, Version: ver, Kind: kind, Tenant: tenant, By: "ann", Text: text, At: at}
	if err := f.edits.Append(t.Context(), e); err != nil {
		t.Fatalf("Append edit v%d: %v", ver, err)
	}
}

func (f *fullRig) message(t *testing.T, seq uint64) domain.Message {
	t.Helper()
	found, err := f.msgs.Find(t.Context(), room, []store.MsgKey{{Room: room, Seq: seq}})
	if err != nil || len(found) != 1 {
		t.Fatalf("Find seq %d = %+v, %v", seq, found, err)
	}
	return found[0]
}

func TestEditFactAheadOfItsProjectionIsAppliedThenPublished(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFullRig(t, nil)
		old := time.Now().Add(-time.Minute)
		f.group(t, old, "ann")
		f.send(t, room, 1, old)
		f.appendEdit(t, 1, 1, domain.EditText, "hello", old.Add(time.Second))
		f.settle()
		id := pbconv.MessageChangeEventID(room, 0, 1, 1)
		ev := f.published(t, id)
		if got := ev.GetMessageEdited().GetMessage(); got.GetVer() != 1 || got.GetText() != "hello" {
			t.Fatalf("msg_edited message = %+v, want ver 1 text hello", got)
		}
		if m := f.message(t, 1); m.Version != 1 || m.Text != "hello" {
			t.Fatalf("message after sweep = v%d %q, want v1 hello", m.Version, m.Text)
		}
		expectPending(t, f.rig, store.StateEdits, 0)
		f.stop(t)
	})
}

func TestDeleteFactProjectsTheDeleteAndPurgesOlderTexts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFullRig(t, nil)
		old := time.Now().Add(-time.Minute)
		f.group(t, old, "ann")
		f.send(t, room, 1, old)
		f.appendEdit(t, 1, 1, domain.EditText, "hello", old.Add(time.Second))
		f.appendEdit(t, 1, 2, domain.EditDelete, "", old.Add(2*time.Second))
		f.settle()
		expectEvents(t, f, pbconv.MessageChangeEventID(room, 0, 1, 1), pbconv.MessageChangeEventID(room, 0, 1, 2))
		if ev := f.published(t, pbconv.MessageChangeEventID(room, 0, 1, 2)); ev.GetMessageDeleted() == nil {
			t.Fatalf("v2 event = %+v, want msg_deleted", ev)
		}
		if m := f.message(t, 1); m.Version != 2 || !m.Deleted || m.Text != "" {
			t.Fatalf("message after sweep = %+v, want deleted v2", m)
		}
		fact, err := f.edits.At(t.Context(), store.MsgKey{Room: room, Seq: 1}, 1)
		if err != nil || fact.Text != "" {
			t.Fatalf("fact v1 after delete = %+v, %v; want its text purged", fact, err)
		}
		expectPending(t, f.rig, store.StateEdits, 0)
		f.stop(t)
	})
}

type frozenMessages struct{ *memstore.Messages }

func (frozenMessages) ApplyEdit(context.Context, domain.Edit) error { return nil }

func TestProjectionStillBehindTheFactKeepsTheFlag(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFullRig(t, func(f *fullRig) sweep.RebuilderStores {
			s := f.stores(t)
			s.Messages = frozenMessages{f.msgs}
			return s
		})
		old := time.Now().Add(-time.Minute)
		f.group(t, old, "ann")
		f.send(t, room, 1, old)
		f.appendEdit(t, 1, 1, domain.EditText, "hello", old.Add(time.Second))
		f.settle()
		if f.event(t, pbconv.MessageChangeEventID(room, 0, 1, 1)) {
			t.Fatal("msg_edited published while the projection is behind")
		}
		expectPending(t, f.rig, store.StateEdits, 1)
		if f.Failures() == 0 || f.Dropped(store.StateEdits) != 0 {
			t.Fatalf("failures %d, dropped %d; want a failure and no drop", f.Failures(), f.Dropped(store.StateEdits))
		}
		f.stop(t)
	})
}

func TestEditFactOfAMissingMessageIsDroppedAndCleared(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFullRig(t, nil)
		old := time.Now().Add(-time.Minute)
		f.group(t, old, "ann")
		f.appendEdit(t, 7, 1, domain.EditText, "hello", old)
		f.settle()
		expectPending(t, f.rig, store.StateEdits, 0)
		if f.Dropped(store.StateEdits) != 1 || f.event(t, pbconv.MessageChangeEventID(room, 0, 7, 1)) {
			t.Fatalf("dropped %d; want the fact dropped without an event", f.Dropped(store.StateEdits))
		}
		f.stop(t)
	})
}
