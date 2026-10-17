package projection_test

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type countingMembers struct {
	*memstore.Rooms
	calls atomic.Int32
	err   error
}

func (c *countingMembers) MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error) {
	c.calls.Add(1)
	if c.err != nil {
		return nil, c.err
	}
	return c.Rooms.MembersOf(ctx, room, users)
}

type failingMentionReads struct{ err error }

func (f failingMentionReads) MarkRead(context.Context, uint64, string, uint64) error { return f.err }

type readRig struct {
	rooms    *memstore.Rooms
	members  *countingMembers
	mentions *memstore.Mentions
	read     *projection.MentionRead
}

func newReadRig(t *testing.T) *readRig {
	t.Helper()
	rooms := memstore.NewRooms()
	rg := &readRig{rooms: rooms, members: &countingMembers{Rooms: rooms}, mentions: memstore.NewMentions()}
	createRoom(t, rooms, roomA, "alice", "minh")
	createRoom(t, rooms, roomB, "alice", "minh")
	for _, seq := range []uint64{80, 90, 100} {
		set := store.MentionSet{Key: store.MsgKey{Room: roomA, Seq: seq}, Tenant: tenant, Sender: "alice", Targets: []domain.MentionTarget{mentionMinh, mentionAll}, CreatedAt: indexedAt, At: indexedAt}
		if err := rg.mentions.ApplyMentions(t.Context(), set); err != nil {
			t.Fatalf("ApplyMentions %d: %v", seq, err)
		}
	}
	rg.read = built(projection.NewMentionRead(rg.members, rg.mentions))(t)
	return rg
}

func (rg *readRig) move(t *testing.T, room uint64, read bool, seq uint64) projection.Event {
	t.Helper()
	move := rg.rooms.MarkUnread
	if read {
		move = rg.rooms.MarkRead
	}
	pos, changed, err := move(t.Context(), room, "minh", seq)
	if err != nil || !changed {
		t.Fatalf("move read position to %d = %v, %v", seq, changed, err)
	}
	r := domain.Room{ID: room, Tenant: tenant, Type: domain.RoomGroup}
	return decoded(t, pbconv.ReadUpdated(r, "minh", pos, indexedAt))
}

func (rg *readRig) readSeqs(t *testing.T) []uint64 {
	t.Helper()
	var out []uint64
	for _, seq := range []uint64{80, 90, 100} {
		got, err := rg.mentions.MentionsOf(t.Context(), store.MsgKey{Room: roomA, Seq: seq})
		if err != nil {
			t.Fatalf("MentionsOf %d: %v", seq, err)
		}
		for _, m := range got {
			if m.Target == mentionMinh && m.Read {
				out = append(out, seq)
			}
		}
	}
	return out
}

func TestALateOlderReadEventFollowsTheCurrentPosition(t *testing.T) {
	rg := newReadRig(t)
	older := rg.move(t, roomA, true, 85)
	newer := rg.move(t, roomA, true, 95)
	noErrors(t, rg.read.Handle(t.Context(), []projection.Event{newer}), 1)
	noErrors(t, rg.read.Handle(t.Context(), []projection.Event{older}), 1)
	if got := rg.readSeqs(t); !slices.Equal(got, []uint64{80, 90}) {
		t.Fatalf("read mentions = %v, want [80 90] from the current position 95", got)
	}
	unread := rg.move(t, roomA, false, 79)
	noErrors(t, rg.read.Handle(t.Context(), []projection.Event{newer, unread, older}), 3)
	if got := rg.readSeqs(t); len(got) != 0 {
		t.Fatalf("read mentions = %v after marking 80 unread, want none", got)
	}
}

func TestMentionReadReadsMembersOncePerRoomPerBatch(t *testing.T) {
	rg := newReadRig(t)
	a1, a2 := rg.move(t, roomA, true, 85), rg.move(t, roomA, true, 95)
	b := rg.move(t, roomB, true, 10)
	noErrors(t, rg.read.Handle(t.Context(), []projection.Event{a1, b, a2}), 3)
	if n := rg.members.calls.Load(); n != 2 {
		t.Fatalf("MembersOf calls = %d, want one per room", n)
	}
}

func TestMentionReadNaksAReadAheadOfTheDocAndDropsAMissingMember(t *testing.T) {
	rg := newReadRig(t)
	ahead := rg.move(t, roomA, true, 95)
	ahead.Proto.GetReadUpdated().ReadVer = 9
	gone := rg.move(t, roomB, true, 10)
	gone.Proto.GetReadUpdated().User = "nobody"
	errs := rg.read.Handle(t.Context(), []projection.Event{ahead, gone})
	if !errors.Is(errs[0], store.ErrStaleRead) || eventkit.Gone(errs[0]) || !eventkit.Gone(errs[1]) {
		t.Fatalf("errs = %v; want the read ahead of the doc retried and the missing member dropped", errs)
	}
	if got := rg.readSeqs(t); len(got) != 0 {
		t.Fatalf("read mentions = %v, want nothing marked from a stale doc", got)
	}
}

func TestMentionReadNaksEveryEventOfAFailedRead(t *testing.T) {
	rg := newReadRig(t)
	ev := rg.move(t, roomA, true, 95)
	rg.members.err = errBoom
	if errs := rg.read.Handle(t.Context(), []projection.Event{ev, ev}); !errors.Is(errs[0], errBoom) || !errors.Is(errs[1], errBoom) {
		t.Fatalf("errs = %v, want the member read error on both events", errs)
	}
	rg.members.err = nil
	rg.read = built(projection.NewMentionRead(rg.members, failingMentionReads{err: errBoom}))(t)
	if errs := rg.read.Handle(t.Context(), []projection.Event{ev, ev}); !errors.Is(errs[0], errBoom) || !errors.Is(errs[1], errBoom) {
		t.Fatalf("errs = %v, want the write error on both events", errs)
	}
}

func TestMentionReadRefusesOtherEvents(t *testing.T) {
	rg := newReadRig(t)
	errs := rg.read.Handle(t.Context(), []projection.Event{decoded(t, msgCreated(roomA, 0, 1, at(1)))})
	if !errors.Is(errs[0], apperr.ErrInvalidArgument) {
		t.Fatalf("errs = %v, want ErrInvalidArgument", errs)
	}
}

func TestMentionReadIsNamedAndNeedsItsPorts(t *testing.T) {
	rg := newReadRig(t)
	if rg.read.Name() != "mention_read" {
		t.Fatalf("name %q", rg.read.Name())
	}
	for name, build := range map[string]func() (*projection.MentionRead, error){
		"no members":  func() (*projection.MentionRead, error) { return projection.NewMentionRead(nil, rg.mentions) },
		"no mentions": func() (*projection.MentionRead, error) { return projection.NewMentionRead(rg.members, nil) },
	} {
		if _, err := build(); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: err = %v, want ErrInvalidArgument", name, err)
		}
	}
}
