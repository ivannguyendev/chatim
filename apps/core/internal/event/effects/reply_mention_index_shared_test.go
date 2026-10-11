package effects_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

var mentionOps = domain.MentionTarget{Kind: domain.MentionGroup, ID: "ops"}

type failingShared struct{ err error }

func (f failingShared) RaiseSharedMention(context.Context, uint64, time.Time) error { return f.err }

func (rg *indexRig) sharedAt(t *testing.T) time.Time {
	t.Helper()
	r, err := rg.rooms.Get(t.Context(), room)
	if err != nil {
		t.Fatalf("Get room: %v", err)
	}
	return r.LastSharedMentionAt
}

func TestAnAllOrGroupMentionRaisesTheRoomSharedMentionTime(t *testing.T) {
	for name, edit := range map[string]func(*domain.Message){
		"all":   func(m *domain.Message) { m.MentionAll = true },
		"group": func(m *domain.Message) { m.Mentions = []domain.MentionTarget{mentionOps} },
	} {
		rg := newIndexRig(t)
		if errs := rg.run(t, rg.send(t, 1, edit)); !allNil(errs, 1) {
			t.Fatalf("%s: errs = %v", name, errs)
		}
		if got := rg.sharedAt(t); !got.Equal(indexedAt) {
			t.Fatalf("%s: shared mention time = %v, want the message time %v", name, got, indexedAt)
		}
	}
}

func TestAUserMentionLeavesTheSharedMentionTimeAlone(t *testing.T) {
	rg := newIndexRig(t)
	only := func(m *domain.Message) { m.Mentions = []domain.MentionTarget{mentionMinh} }
	if errs := rg.run(t, rg.send(t, 1, only)); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if got := rg.sharedAt(t); !got.IsZero() {
		t.Fatalf("shared mention time = %v, want unset after a user mention", got)
	}
}

func TestAnEditAddingAGroupMentionRaisesTheSharedMentionTime(t *testing.T) {
	rg := newIndexRig(t)
	only := func(m *domain.Message) { m.Mentions = []domain.MentionTarget{mentionMinh} }
	if errs := rg.run(t, rg.send(t, 1, only), rg.editMentioning(t, 1, 1, false, mentionMinh, mentionOps)); !allNil(errs, 2) {
		t.Fatalf("errs = %v", errs)
	}
	if got := rg.sharedAt(t); !got.Equal(indexedAt) {
		t.Fatalf("shared mention time = %v, want the message time %v", got, indexedAt)
	}
	if errs := rg.run(t, rg.edit(t, 1, 2, domain.EditDelete)); !allNil(errs, 1) || !rg.sharedAt(t).Equal(indexedAt) {
		t.Fatalf("errs %v shared %v; a delete never lowers the shared mention time", errs, rg.sharedAt(t))
	}
}

func TestAFailedSharedMentionRaiseNaksTheRecord(t *testing.T) {
	rg := newIndexRig(t)
	boom := errors.New("mongo down")
	d := rg.deps()
	d.Shared = failingShared{err: boom}
	rg.index = rg.build(t, d)
	all := func(m *domain.Message) { m.MentionAll = true }
	if errs := rg.run(t, rg.send(t, 1, all)); len(errs) != 1 || !errors.Is(errs[0], boom) {
		t.Fatalf("errs = %v, want the raise error so the record is retried", errs)
	}
	if !rg.mentionsOf(t, 1)[mentionAll].Live {
		t.Fatalf("mentions = %+v, want the @all doc written before the raise", rg.mentionsOf(t, 1))
	}
}
