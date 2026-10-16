package resync_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

const (
	busyRoom    uint64 = 7_340_000_001
	newRoom     uint64 = 7_340_000_002
	staleRoom   uint64 = 7_340_000_003
	foreignRoom uint64 = 7_340_000_004
)

var (
	lostFrom = time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	lostTo   = lostFrom.Add(time.Hour)
	errMark  = errors.New("mark failed")
)

type markSpy struct {
	states *memstore.EventStates
	pages  [][]store.StateTarget
	checks []string
	err    error
}

func (s *markSpy) Mark(ctx context.Context, c store.StateCollection, targets []store.StateTarget) error {
	if s.err != nil {
		return s.err
	}
	s.pages = append(s.pages, slices.Clone(targets))
	return s.states.Mark(ctx, c, targets)
}

func (s *markSpy) ArmMessageCountCheck(_ context.Context, key store.MsgKey, counter string) (timer.Timer, error) {
	if s.err != nil {
		return timer.Timer{}, s.err
	}
	s.checks = append(s.checks, countCheckID(key.Room, key.Seq, counter))
	return timer.Timer{Seq: uint64(len(s.checks))}, nil
}

func (s *markSpy) marked() []store.StateTarget { return slices.Concat(s.pages...) }

func countCheckID(room, seq uint64, counter string) string {
	return strconv.FormatUint(room, 10) + "/" + strconv.FormatUint(seq, 10) + "/" + counter
}

func roomMark(room uint64, bit store.StateBit, ver uint64) store.StateTarget {
	return store.StateTarget{Collection: store.StateRooms, Bit: bit, Room: room, Ver: ver}
}

func messageMark(room, seq uint64) store.StateTarget {
	return store.StateTarget{Collection: store.StateMessages, Bit: store.BitMsgCreated, Room: room, Seq: seq}
}

func timelineMarks(room, newest, oldest uint64) []store.StateTarget {
	var out []store.StateTarget
	for seq := newest; seq >= oldest; seq-- {
		out = append(out, messageMark(room, seq))
	}
	return out
}

func assertMarked(t *testing.T, got, want []store.StateTarget) {
	t.Helper()
	if !slices.EqualFunc(got, want, func(a, b store.StateTarget) bool {
		at := a.At.Equal(b.At)
		a.At, b.At = time.Time{}, time.Time{}
		return at && a == b
	}) {
		t.Fatalf("marked %d targets %+v,\nwant %d targets %+v", len(got), got, len(want), want)
	}
}

type world struct {
	rooms     *memstore.Rooms
	msgs      *memstore.Messages
	edits     *memstore.Edits
	reactions *memstore.Interactions
	pins      *memstore.Pins
	hidden    *memstore.Hidden
	states    *memstore.EventStates
}

func newWorld(t *testing.T) world {
	t.Helper()
	w := world{
		rooms: memstore.NewRooms(), msgs: memstore.NewMessages(), edits: memstore.NewEdits(),
		reactions: memstore.NewInteractions(), pins: memstore.NewPins(), hidden: memstore.NewHidden(),
	}
	w.states = memstore.NewEventStates(memstore.EventStateStores{
		Messages: w.msgs, Rooms: w.rooms, Edits: w.edits, Hidden: w.hidden, Interactions: w.reactions, Pins: w.pins,
	})
	old := lostFrom.Add(-48 * time.Hour)
	w.room(t, busyRoom, "acme", old)
	w.room(t, newRoom, "acme", lostFrom.Add(10*time.Minute))
	w.room(t, staleRoom, "acme", old)
	w.room(t, foreignRoom, "other", lostFrom.Add(20*time.Minute))
	w.messages(t, busyRoom, 150, lostFrom.Add(-30*time.Minute))
	w.messages(t, staleRoom, 5, lostFrom.Add(-3*time.Hour))
	acts := []store.Activity{{Room: busyRoom, Seq: 150, At: lostFrom.Add(2 * time.Hour)}}
	if err := w.rooms.TouchActivity(t.Context(), acts); err != nil {
		t.Fatalf("TouchActivity: %v", err)
	}
	return w
}

func (w world) room(t *testing.T, id uint64, tenant string, created time.Time) {
	t.Helper()
	r := domain.Room{ID: id, Tenant: tenant, Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: created, MemberCount: 1}
	owner := domain.Member{Room: id, Tenant: tenant, User: "alice", Role: domain.RoleOwner, JoinedAt: created}
	if err := w.rooms.Create(t.Context(), r, []domain.Member{owner}); err != nil {
		t.Fatalf("Create(%d): %v", id, err)
	}
}

func (w world) messages(t *testing.T, room uint64, n int, first time.Time) {
	t.Helper()
	msgs := make([]domain.Message, n)
	for i := range msgs {
		seq := uint64(i + 1)
		msgs[i] = domain.Message{
			Room: room, Seq: seq, Tenant: "acme", From: "alice", Kind: domain.KindText, Text: "hi",
			CID: "c" + strconv.FormatUint(seq, 10), CreatedAt: first.Add(time.Duration(i) * time.Minute),
		}
	}
	for i, r := range w.msgs.Insert(t.Context(), msgs) {
		if r.Outcome != store.Inserted {
			t.Fatalf("insert %d/%d: %+v", room, i+1, r)
		}
	}
}

func (w world) spy() *markSpy { return &markSpy{states: w.states} }

func (w world) deps(spy *markSpy) resync.Deps {
	return resync.Deps{Rooms: w.rooms, Pages: w.msgs, Edits: w.edits, Interactions: w.reactions, Pins: w.pins, Members: w.rooms, Hidden: w.hidden, States: spy, Timers: spy}
}
