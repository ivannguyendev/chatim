package mutate_test

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type laggingEdits struct{ *memstore.Edits }

func (laggingEdits) Latest(context.Context, store.MsgKey) (domain.Edit, bool, error) {
	return domain.Edit{}, false, nil
}

func TestEditWritesVersionOneWithTheOriginalText(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hello")
	got, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "hello there"))
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if got.Version != 1 || got.Text != "hello there" || got.Deleted || !got.EditedAt.Equal(rg.at()) {
		t.Fatalf("snapshot = %+v, want version 1 with the new text edited at %v", got, rg.at())
	}
	if s := rg.stored(t, 1); s.Version != 1 || s.Text != "hello there" {
		t.Fatalf("stored = %+v, want the projection applied before the ack", s)
	}
	want := domain.Edit{Room: room, Seq: 1, Version: 1, Kind: domain.EditText, Tenant: tenant, By: "alice", Text: "hello there", At: rg.at()}
	if facts := rg.facts(t, 1); len(facts) != 1 || !sameEdit(facts[0], want) {
		t.Fatalf("facts = %+v, want %+v", facts, want)
	}
	rooms, events := rg.events.list()
	if !slices.Equal(rooms, []uint64{room}) || len(events) != 1 || !proto.Equal(events[0], pbconv.MessageEdited(domain.RoomGroup, got, want)) {
		t.Fatalf("enqueued %v %v, want one msg_edited for room %d", rooms, events, room)
	}
	if id := events[0].GetId(); id != pbconv.MessageChangeEventID(room, 0, 1, 1) {
		t.Fatalf("event id = %q", id)
	}
}

func TestEditsBuildOnTheVersionTheClientSaw(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "v0")
	if _, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "v1")); err != nil {
		t.Fatalf("first edit: %v", err)
	}
	got, err := rg.m.Edit(t.Context(), edit("alice", 1, 1, "v2"))
	if err != nil || got.Version != 2 || got.Text != "v2" {
		t.Fatalf("second edit = %+v, %v; want version 2", got, err)
	}
	facts := rg.facts(t, 1)
	if len(facts) != 2 || facts[0].Text != "v1" || facts[1].Version != 2 || facts[1].Text != "v2" {
		t.Fatalf("facts = %+v, want v1 then v2", facts)
	}
	if _, events := rg.events.list(); len(events) != 2 || events[1].GetId() != pbconv.MessageChangeEventID(room, 0, 1, 2) {
		t.Fatalf("events = %v, want a second event with the v2 id", events)
	}
}

func TestEditWithAStaleBaseConflicts(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "v0")
	if _, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "v1")); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	for _, base := range []uint32{0, 2, math.MaxUint32} {
		if _, err := rg.m.Edit(t.Context(), edit("alice", 1, base, "other")); !errors.Is(err, domain.ErrVersionConflict) {
			t.Fatalf("Edit base %d = %v, want ErrVersionConflict", base, err)
		}
	}
	if s := rg.stored(t, 1); s.Version != 1 || s.Text != "v1" {
		t.Fatalf("stored = %+v, want v1 untouched", s)
	}
	if facts := rg.facts(t, 1); len(facts) != 1 {
		t.Fatalf("facts = %+v, want only v1", facts)
	}
}

func TestRetriedEditSucceedsWithoutASecondFact(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "v0")
	first, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "v1"))
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	rg.now = rg.now.Add(time.Second)
	again, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "v1"))
	if err != nil || again.Version != 1 || !again.EditedAt.Equal(first.EditedAt) {
		t.Fatalf("retry = %+v, %v; want the first result", again, err)
	}
	if facts := rg.facts(t, 1); len(facts) != 1 {
		t.Fatalf("facts = %+v, want one", facts)
	}
	if _, events := rg.events.list(); len(events) != 2 || !proto.Equal(events[0], events[1]) {
		t.Fatalf("events = %v, want the retry to enqueue the same event", events)
	}
}

func TestRetryAfterACrashFinishesTheProjection(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "v0")
	fact := domain.Edit{Room: room, Seq: 1, Version: 1, Kind: domain.EditText, Tenant: tenant, By: "alice", Text: "v1", At: created.Add(time.Second)}
	if err := rg.edits.Append(t.Context(), fact); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "v1"))
	if err != nil || got.Version != 1 || got.Text != "v1" || !got.EditedAt.Equal(fact.At) {
		t.Fatalf("retry = %+v, %v; want the stored fact projected", got, err)
	}
}

func TestDuplicateVersionIsARetryOnlyForTheSameChange(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "v0")
	rg.m = rg.mutator(t, nil, laggingEdits{rg.edits})
	other := domain.Edit{Room: room, Seq: 1, Version: 1, Kind: domain.EditText, Tenant: tenant, By: "alice", Text: "from another device", At: created}
	if err := rg.edits.Append(t.Context(), other); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "mine")); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("losing edit = %v, want ErrVersionConflict", err)
	}
	got, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "from another device"))
	if err != nil || got.Version != 1 || got.Text != "from another device" {
		t.Fatalf("same change = %+v, %v; want a retry success", got, err)
	}
}

func TestEditRejectsBadInput(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "a")
	thread := edit("alice", 1, 0, "x")
	thread.Thread = 1
	noRoom := edit("alice", 1, 0, "x")
	noRoom.Room = 0
	for name, c := range map[string]mutate.EditCmd{
		"empty text": edit("alice", 1, 0, ""), "blank text": edit("alice", 1, 0, " \n "),
		"seq zero": edit("alice", 0, 0, "x"), "thread": thread, "room zero": noRoom,
	} {
		if _, err := rg.m.Edit(t.Context(), c); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: Edit = %v, want ErrInvalidArgument", name, err)
		}
	}
}
