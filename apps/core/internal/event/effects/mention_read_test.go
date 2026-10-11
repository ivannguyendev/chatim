package effects_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type readRig struct {
	rooms    *memstore.Rooms
	mentions *memstore.Mentions
	eff      *effects.MentionRead
}

type failingMentionReads struct{ err error }

func (f failingMentionReads) MarkRead(context.Context, uint64, string, uint64) error { return f.err }

func newReadRig(t *testing.T) *readRig {
	t.Helper()
	rg := &readRig{rooms: memstore.NewRooms(), mentions: memstore.NewMentions()}
	r := domain.Room{ID: room, Tenant: tenant, Type: domain.RoomGroup, Name: "team", CreatedBy: "alice", CreatedAt: indexedAt, MemberCount: 2}
	members := []domain.Member{
		{Room: room, Tenant: tenant, User: "alice", Role: domain.RoleOwner, JoinedAt: indexedAt},
		{Room: room, Tenant: tenant, User: "minh", Role: domain.RoleMember, JoinedAt: indexedAt},
	}
	if err := rg.rooms.Create(t.Context(), r, members); err != nil {
		t.Fatalf("create room: %v", err)
	}
	for _, seq := range []uint64{80, 90, 100} {
		set := store.MentionSet{Key: store.MsgKey{Room: room, Seq: seq}, Tenant: tenant, Sender: "alice", Targets: []domain.MentionTarget{mentionMinh, mentionAll}, CreatedAt: indexedAt, At: indexedAt}
		if err := rg.mentions.ApplyMentions(t.Context(), set); err != nil {
			t.Fatalf("ApplyMentions %d: %v", seq, err)
		}
	}
	rg.eff = built(effects.NewMentionRead(effects.MentionReadDeps{Members: rg.rooms, Mentions: rg.mentions}))(t)
	return rg
}

func (rg *readRig) move(t *testing.T, read bool, seq uint64) work.Record {
	t.Helper()
	move := rg.rooms.MarkUnread
	if read {
		move = rg.rooms.MarkRead
	}
	pos, changed, err := move(t.Context(), room, "minh", seq)
	if err != nil || !changed {
		t.Fatalf("move read position to %d = %v, %v", seq, changed, err)
	}
	return work.Record{Kind: store.ReadChanged, Room: room, User: "minh", Version: uint32(pos.Ver), CommittedAt: indexedAt}
}

func (rg *readRig) readSeqs(t *testing.T) []uint64 {
	t.Helper()
	var out []uint64
	for _, seq := range []uint64{80, 90, 100} {
		got, err := rg.mentions.MentionsOf(t.Context(), store.MsgKey{Room: room, Seq: seq})
		if err != nil {
			t.Fatalf("MentionsOf %d: %v", seq, err)
		}
		for _, m := range got {
			switch {
			case m.Target == mentionMinh && m.Read:
				out = append(out, seq)
			case m.Target != mentionMinh && m.Read:
				t.Fatalf("%v at %d is read; only user mentions carry a read state", m.Target, seq)
			}
		}
	}
	return out
}

func TestMentionReadFollowsTheReadPositionBothWays(t *testing.T) {
	rg := newReadRig(t)
	read := rg.move(t, true, 95)
	for range 2 {
		if errs := rg.eff.Effect().Run(t.Context(), []work.Record{read}); !allNil(errs, 1) {
			t.Fatalf("errs = %v", errs)
		}
		if got := rg.readSeqs(t); len(got) != 2 || got[0] != 80 || got[1] != 90 {
			t.Fatalf("read mentions = %v after reading to 95, want [80 90]", got)
		}
	}
	unread := rg.move(t, false, 79)
	for range 2 {
		if errs := rg.eff.Effect().Run(t.Context(), []work.Record{read, unread}); !allNil(errs, 2) {
			t.Fatalf("errs = %v", errs)
		}
		if got := rg.readSeqs(t); len(got) != 0 {
			t.Fatalf("read mentions = %v after marking 80 unread, want none", got)
		}
	}
}

func TestMentionReadDropsAMissingMemberAndNaksAStaleRead(t *testing.T) {
	rg := newReadRig(t)
	gone := work.Record{Kind: store.ReadChanged, Room: room, User: "nobody", Version: 1, CommittedAt: indexedAt}
	ahead := work.Record{Kind: store.ReadChanged, Room: room, User: "minh", Version: 7, CommittedAt: indexedAt}
	errs := rg.eff.Effect().Run(t.Context(), []work.Record{gone, ahead})
	if len(errs) != 2 || errs[0] != nil || !errors.Is(errs[1], store.ErrStaleRead) || rg.eff.Dropped() != 1 {
		t.Fatalf("errs %v dropped %d; want the missing member dropped and the record ahead of the doc retried", errs, rg.eff.Dropped())
	}
}

func TestMentionReadNaksWhenTheWriteFails(t *testing.T) {
	rg := newReadRig(t)
	boom := errors.New("mongo down")
	rg.eff = built(effects.NewMentionRead(effects.MentionReadDeps{Members: rg.rooms, Mentions: failingMentionReads{err: boom}}))(t)
	rec := rg.move(t, true, 95)
	if errs := rg.eff.Effect().Run(t.Context(), []work.Record{rec, rec}); len(errs) != 2 || !errors.Is(errs[0], boom) || !errors.Is(errs[1], boom) {
		t.Fatalf("errs = %v, want the write error on both records", errs)
	}
}

func TestNewMentionReadRejectsMissingDeps(t *testing.T) {
	rg := newReadRig(t)
	for name, d := range map[string]effects.MentionReadDeps{
		"no members":  {Mentions: rg.mentions},
		"no mentions": {Members: rg.rooms},
	} {
		if _, err := effects.NewMentionRead(d); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: err = %v, want ErrInvalidArgument", name, err)
		}
	}
	if e := rg.eff.Effect(); e.Name != effects.MentionReadName || e.Delay != time.Duration(0) {
		t.Fatalf("effect = %q delay %v, want %q with no delay", e.Name, e.Delay, effects.MentionReadName)
	}
}
