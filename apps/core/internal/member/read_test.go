package member_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/member"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestMarkReadOnlyMovesForwardAndNeverTouchesTheMembership(t *testing.T) {
	rg := newRig(t, nil)
	rg.sendMany(t, 5)
	before := rg.memberOf(t, room, "bob")
	steps := []struct{ seq, wantSeq, wantVer uint64 }{{3, 3, 1}, {2, 3, 1}, {3, 3, 1}, {0, 5, 2}, {99, 5, 2}}
	for _, s := range steps {
		if got := rg.markRead(t, "bob", room, s.seq); got != (domain.ReadPosition{Seq: s.wantSeq, Ver: s.wantVer}) {
			t.Fatalf("MarkRead(%d) = %+v, want seq %d ver %d", s.seq, got, s.wantSeq, s.wantVer)
		}
	}
	after := rg.memberOf(t, room, "bob")
	if after.ReadSeq != 5 || after.ReadVer != 2 || withoutReadFields(after) != withoutReadFields(before) {
		t.Fatalf("bob doc = %+v,\nwant %+v with only the read position moved", after, before)
	}
	if forgets := rg.forgets.list(); len(forgets) != 0 {
		t.Fatalf("forgot members of %v, want no forget on a read move", forgets)
	}
}

func TestMarkUnreadOnlyMovesBack(t *testing.T) {
	rg := newRig(t, nil)
	rg.sendMany(t, 5)
	rg.markRead(t, "bob", room, 5)
	steps := []struct{ seq, wantSeq, wantVer uint64 }{{3, 2, 2}, {4, 2, 2}, {99, 2, 2}, {1, 0, 3}}
	for _, s := range steps {
		if got := rg.markUnread(t, "bob", room, s.seq); got != (domain.ReadPosition{Seq: s.wantSeq, Ver: s.wantVer}) {
			t.Fatalf("MarkUnread(%d) = %+v, want seq %d ver %d", s.seq, got, s.wantSeq, s.wantVer)
		}
	}
	if _, err := rg.m.MarkUnread(t.Context(), readCmd("bob", room, 0)); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("MarkUnread(0) = %v, want InvalidArgument", err)
	}
}

func TestEachMoveHandsOneReadPositionToTheSink(t *testing.T) {
	rg := newRig(t, nil)
	rg.sendMany(t, 4)
	rg.markRead(t, "bob", room, 2)
	rg.markRead(t, "bob", room, 1)
	rg.markUnread(t, "bob", room, 3)
	rg.markUnread(t, "bob", room, 2)
	rg.markRead(t, "bob", room, 4)
	adds := rg.reads.list()
	want := []domain.ReadPosition{{Seq: 2, Ver: 1}, {Seq: 1, Ver: 2}, {Seq: 4, Ver: 3}}
	if len(adds) != len(want) {
		t.Fatalf("sink adds = %+v, want %d", adds, len(want))
	}
	for i, pos := range want {
		a := adds[i]
		if a.room.ID != room || a.room.Tenant != tenant || a.room.Type != domain.RoomGroup || a.user != "bob" || a.pos != pos {
			t.Fatalf("add %d = %+v, want bob of room %d at %+v", i, a, room, pos)
		}
	}
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("events = %v, want none: read positions go through the sink", events)
	}
}

func TestAnEmptyRoomKeepsTheReadPosition(t *testing.T) {
	rg := newRig(t, nil)
	before := rg.memberOf(t, room, "bob")
	if got := rg.markRead(t, "bob", room, 3); got != (domain.ReadPosition{}) {
		t.Fatalf("MarkRead in an empty room = %+v, want the current position", got)
	}
	if got := rg.markUnread(t, "bob", room, 1); got != (domain.ReadPosition{}) {
		t.Fatalf("MarkUnread in an empty room = %+v, want the current position", got)
	}
	if after := rg.memberOf(t, room, "bob"); after != before {
		t.Fatalf("bob doc = %+v, want it untouched %+v", after, before)
	}
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("events = %v, want none", events)
	}
	if adds := rg.reads.list(); len(adds) != 0 {
		t.Fatalf("sink adds = %+v, want none", adds)
	}
}

func TestReadNeedsAnActiveMember(t *testing.T) {
	var asked []access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error {
		asked = append(asked, r)
		if r.User == "carol" {
			return access.ErrDenied
		}
		return nil
	})
	rg := newMemberRig(t, deny)
	rg.post(t, 1)
	if _, err := rg.m.RemoveMember(t.Context(), remove("owen", "max")); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	asked = nil
	cases := []struct {
		name string
		cmd  func() error
		want error
	}{
		{"never a member", func() error { _, err := rg.m.MarkRead(t.Context(), readCmd("zed", group, 1)); return err }, domain.ErrNotMember},
		{"removed", func() error { _, err := rg.m.MarkUnread(t.Context(), readCmd("max", group, 1)); return err }, domain.ErrNotMember},
		{"other tenant", func() error {
			_, err := rg.m.MarkRead(t.Context(), member.ReadCmd{Tenant: "other", User: "mia", Room: group, Seq: 1})
			return err
		}, domain.ErrRoomNotFound},
		{"policy denies", func() error { _, err := rg.m.MarkRead(t.Context(), readCmd("carol", room, 1)); return err }, apperr.ErrPermissionDenied},
	}
	for _, c := range cases {
		if err := c.cmd(); !errors.Is(err, c.want) {
			t.Fatalf("%s: = %v, want %v", c.name, err, c.want)
		}
	}
	if len(asked) != 1 || asked[0].Action != access.MarkRead || asked[0].User != "carol" {
		t.Fatalf("policy asked %+v, want mark_read for carol only", asked)
	}
	if _, events := rg.events.list(); len(events) != 2 {
		t.Fatalf("events = %v, want only the removal and its count", events)
	}
	if adds := rg.reads.list(); len(adds) != 0 {
		t.Fatalf("sink adds = %+v, want none", adds)
	}
}

func TestTheClampReadsTheLastSeqOnlyPastTheRoomHead(t *testing.T) {
	rg := newRig(t, nil)
	rg.sendMany(t, 5)
	rg.setHead(t, 3)
	msgs := &countingLast{Messages: rg.msgs}
	d := rg.deps(t, nil)
	d.Messages = msgs
	rg.m = rg.build(t, d)
	steps := []struct {
		seq, want uint64
		lasts     int32
	}{{2, 2, 0}, {3, 3, 0}, {4, 4, 1}, {0, 5, 2}, {9, 5, 3}}
	for _, s := range steps {
		if got := rg.markRead(t, "bob", room, s.seq); got.Seq != s.want || msgs.lasts.Load() != s.lasts {
			t.Fatalf("MarkRead(%d) = %+v after %d Last calls, want seq %d after %d", s.seq, got, msgs.lasts.Load(), s.want, s.lasts)
		}
	}
}

func TestDirectRoomsTrackReadPositionsToo(t *testing.T) {
	rg := newRig(t, nil)
	r, members, err := domain.NewDirectRoom(tenant, "owen", "mia", created, direct)
	if err != nil {
		t.Fatalf("NewDirectRoom: %v", err)
	}
	if err := rg.rooms.Create(t.Context(), r, members); err != nil {
		t.Fatalf("create direct: %v", err)
	}
	m := domain.Message{Room: direct, Seq: 1, Tenant: tenant, From: "owen", Kind: domain.KindText, Text: "hi", CID: "d-1", CreatedAt: created}
	if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert direct: %+v", res)
	}
	if got := rg.markRead(t, "mia", direct, 1); got != (domain.ReadPosition{Seq: 1, Ver: 1}) {
		t.Fatalf("MarkRead in a direct room = %+v, want seq 1 ver 1", got)
	}
	if adds := rg.reads.list(); len(adds) != 1 || adds[0].user != "mia" || adds[0].room.Type != domain.RoomDM {
		t.Fatalf("sink adds = %+v, want one for mia in the direct room", adds)
	}
}
