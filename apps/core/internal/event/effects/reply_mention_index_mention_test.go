package effects_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func mentioning(m *domain.Message) {
	m.Mentions, m.MentionAll = []domain.MentionTarget{mentionMinh}, true
}

func TestMentionsOfANewMessageGetOneDocPerTarget(t *testing.T) {
	rg := newIndexRig(t)
	if errs := rg.run(t, rg.send(t, 1, mentioning)); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	got := rg.mentionsOf(t, 1)
	want := domain.Mention{Key: domain.MsgKey{Room: room, Seq: 1}, Tenant: tenant, Target: mentionMinh, Sender: "alice", Live: true, CreatedAt: indexedAt, UpdatedAt: indexedAt}
	if len(got) != 2 || got[mentionMinh] != want || !got[mentionAll].Live {
		t.Fatalf("mentions = %+v, want %+v and a live @all", got, want)
	}
	if len(rg.timers.checks()) != 0 || len(rg.js.Stored()) != 0 {
		t.Fatalf("armed %v events %d; a mention alone arms and publishes nothing", rg.timers.checks(), len(rg.js.Stored()))
	}
}

func TestDeletingAMessageRetiresItsMentions(t *testing.T) {
	rg := newIndexRig(t)
	if errs := rg.run(t, rg.send(t, 1, mentioning), rg.editMentioning(t, 1, 1, true, mentionMinh)); !allNil(errs, 2) {
		t.Fatalf("errs = %v", errs)
	}
	if got := rg.mentionsOf(t, 1); !got[mentionMinh].Live || got[mentionMinh].Ver != 1 {
		t.Fatalf("after an edit mentions = %+v, want still live at v1", got)
	}
	if errs := rg.run(t, rg.edit(t, 1, 2, domain.EditDelete)); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	for target, m := range rg.mentionsOf(t, 1) {
		if m.Live || m.Ver != 2 {
			t.Errorf("%v = %+v, want retired at v2", target, m)
		}
	}
}

func TestAnEditThatDropsAMentionRetiresItsDoc(t *testing.T) {
	rg := newIndexRig(t)
	lan := domain.MentionTarget{Kind: domain.MentionUser, ID: "lan"}
	if errs := rg.run(t, rg.send(t, 1, mentioning)); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if errs := rg.run(t, rg.editMentioning(t, 1, 1, false, lan)); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	got := rg.mentionsOf(t, 1)
	if len(got) != 3 || got[mentionMinh].Live || got[mentionAll].Live || !got[lan].Live || got[lan].Ver != 1 || got[mentionMinh].Ver != 1 {
		t.Fatalf("mentions = %+v, want minh and @all retired and lan live at v1", got)
	}
}

func TestAnOldEditRecordAfterANewOneChangesNothing(t *testing.T) {
	rg := newIndexRig(t)
	if errs := rg.run(t, rg.send(t, 1, mentioning)); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	edit := rg.edit(t, 1, 1, domain.EditText)
	if errs := rg.run(t, rg.edit(t, 1, 2, domain.EditDelete)); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if errs := rg.run(t, edit); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	got := rg.mentionsOf(t, 1)
	if len(got) != 2 || got[mentionMinh].Live || got[mentionMinh].Ver != 2 || got[mentionAll].Live {
		t.Fatalf("mentions = %+v, want both retired at v2 after the late v1 record", got)
	}
}

func TestOnlyFlaggedRecordsReadMessagesOncePerRoom(t *testing.T) {
	rg := newIndexRig(t)
	plain := rg.send(t, 1, nil)
	a, b := rg.send(t, 2, mentioning), rg.send(t, 3, mentioning)
	original := work.Record{Kind: store.EditInserted, Room: room, Seq: 2, CommittedAt: indexedAt}
	if errs := rg.run(t, plain, a, b, original); !allNil(errs, 4) {
		t.Fatalf("errs = %v", errs)
	}
	if n := rg.finder.finds.Load(); n != 1 || len(rg.mentionsOf(t, 3)) != 2 {
		t.Fatalf("finds %d, mentions of 3 %v; want one read for the flagged records", n, rg.mentionsOf(t, 3))
	}
	if errs := rg.run(t, plain, original); !allNil(errs, 2) || rg.finder.finds.Load() != 1 {
		t.Fatalf("errs %v finds %d; want no read for unflagged records and the ver 0 row", errs, rg.finder.finds.Load())
	}
}

func TestAMissingMessageIsDropped(t *testing.T) {
	rg := newIndexRig(t)
	rec := work.Record{Kind: store.MessageInserted, Room: room, Seq: 9, Version: uint32(store.HasMention), CommittedAt: indexedAt}
	if errs := rg.run(t, rec); !allNil(errs, 1) || rg.index.Dropped() != 1 {
		t.Fatalf("errs %v dropped %d, want the record acked and counted as dropped", errs, rg.index.Dropped())
	}
}

func TestNewReplyMentionIndexRejectsBadInput(t *testing.T) {
	rg := newIndexRig(t)
	cfg := effects.ReplyMentionIndexConfig{SubjectRoot: "evt"}
	bad := map[string]func(*effects.ReplyMentionIndexDeps, *effects.ReplyMentionIndexConfig){
		"no messages": func(d *effects.ReplyMentionIndexDeps, _ *effects.ReplyMentionIndexConfig) { d.Messages = nil },
		"no replies":  func(d *effects.ReplyMentionIndexDeps, _ *effects.ReplyMentionIndexConfig) { d.Replies = nil },
		"no counts":   func(d *effects.ReplyMentionIndexDeps, _ *effects.ReplyMentionIndexConfig) { d.Counts = nil },
		"no mentions": func(d *effects.ReplyMentionIndexDeps, _ *effects.ReplyMentionIndexConfig) { d.Mentions = nil },
		"no timers":   func(d *effects.ReplyMentionIndexDeps, _ *effects.ReplyMentionIndexConfig) { d.Timers = nil },
		"no rooms":    func(d *effects.ReplyMentionIndexDeps, _ *effects.ReplyMentionIndexConfig) { d.Rooms = nil },
		"no js":       func(d *effects.ReplyMentionIndexDeps, _ *effects.ReplyMentionIndexConfig) { d.JS = nil },
		"no root":     func(_ *effects.ReplyMentionIndexDeps, c *effects.ReplyMentionIndexConfig) { c.SubjectRoot = "" },
		"bad cache":   func(_ *effects.ReplyMentionIndexDeps, c *effects.ReplyMentionIndexConfig) { c.RoomCache = -1 },
	}
	for name, edit := range bad {
		d, c := rg.deps(), cfg
		edit(&d, &c)
		if _, err := effects.NewReplyMentionIndex(d, c); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: err = %v, want ErrInvalidArgument", name, err)
		}
	}
}
