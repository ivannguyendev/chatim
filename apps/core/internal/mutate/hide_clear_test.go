package mutate_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestHideNeedsAnExistingMessage(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	hide := func(user string, seq uint64) error {
		return rg.m.Hide(t.Context(), mutate.HideCmd{Tenant: tenant, User: user, Room: room, Seq: seq})
	}
	for range 2 {
		if err := hide("bob", 1); err != nil {
			t.Fatalf("Hide: %v", err)
		}
	}
	if seqs, err := rg.hidden.HiddenIn(t.Context(), "bob", room, 0, 1, 1); err != nil || !slices.Equal(seqs, []uint64{1}) {
		t.Fatalf("bob hidden = %v, %v; want [1]", seqs, err)
	}
	if seqs, err := rg.hidden.HiddenIn(t.Context(), "alice", room, 0, 1, 1); err != nil || len(seqs) != 0 {
		t.Fatalf("alice hidden = %v, %v; want none", seqs, err)
	}
	marks, err := rg.hidden.Between(t.Context(), room, created, rg.at(), store.MaxHiddenScan)
	if err != nil || len(marks) != 1 || !marks[0].At.Equal(rg.at()) {
		t.Fatalf("hidden marks = %+v, %v; want one at the server time %v", marks, err, rg.at())
	}
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"unknown message", hide("bob", 9), domain.ErrMessageNotFound},
		{"not a member", hide("mallory", 1), domain.ErrNotMember},
		{"seq zero", hide("bob", 0), apperr.ErrInvalidArgument},
	}
	for _, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Fatalf("%s: Hide = %v, want %v", c.name, c.err, c.want)
		}
	}
}

func TestClearHistoryMarksTheServerTimeAndOnlyRaisesIt(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "m")
	clearAt := func(user string, now time.Time) (time.Time, error) {
		rg.now = now
		return rg.m.ClearHistory(t.Context(), mutate.ClearCmd{Tenant: tenant, User: user, Room: room})
	}
	first := rg.now
	got, err := clearAt("bob", first)
	if want := first.Truncate(time.Millisecond); err != nil || !got.Equal(want) {
		t.Fatalf("ClearHistory = %v, %v; want the server time %v cut to ms", got, err, want)
	}
	if got, err := clearAt("bob", first.Add(-time.Hour)); err != nil || !got.Equal(first.Truncate(time.Millisecond)) {
		t.Fatalf("ClearHistory with an earlier clock = %v, %v; want the mark kept at %v", got, err, first.Truncate(time.Millisecond))
	}
	later := first.Add(time.Minute)
	if got, err := clearAt("bob", later); err != nil || !got.Equal(later.Truncate(time.Millisecond)) {
		t.Fatalf("ClearHistory later = %v, %v; want %v", got, err, later.Truncate(time.Millisecond))
	}
	m, err := rg.rooms.Member(t.Context(), room, "bob")
	if err != nil || !m.ClearedAt.Equal(later.Truncate(time.Millisecond)) {
		t.Fatalf("bob member = %+v, %v; want cleared at %v", m, err, later.Truncate(time.Millisecond))
	}
	if alice, err := rg.rooms.Member(t.Context(), room, "alice"); err != nil || !alice.ClearedAt.IsZero() {
		t.Fatalf("alice member = %+v, %v; want no mark", alice, err)
	}
	if _, err := clearAt("mallory", later); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("ClearHistory of a non member = %v, want PermissionDenied", err)
	}
}

func TestHideAndClearAskThePolicy(t *testing.T) {
	var asked []access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error { asked = append(asked, r); return access.ErrDenied })
	rg := newRig(t, deny)
	rg.send(t, 1, "alice", "hi")
	if err := rg.m.Hide(t.Context(), mutate.HideCmd{Tenant: tenant, User: "bob", Room: room, Seq: 1}); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("Hide = %v, want PermissionDenied", err)
	}
	if _, err := rg.m.ClearHistory(t.Context(), mutate.ClearCmd{Tenant: tenant, User: "bob", Room: room}); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("ClearHistory = %v, want PermissionDenied", err)
	}
	if len(asked) != 2 || asked[0].Action != access.HideMessage || asked[0].Author != "alice" || asked[1].Action != access.ClearHistory || asked[1].Author != "" {
		t.Fatalf("policy asked %+v, want hide_message on alice's message, then clear_history without an author", asked)
	}
}

func TestHidingSendsOneMessageHiddenAndHidingAgainSendsNothing(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	rg.send(t, 2, "alice", "yo")
	hide := func(seq uint64) {
		t.Helper()
		if err := rg.m.Hide(t.Context(), mutate.HideCmd{Tenant: tenant, User: "bob", Room: room, Seq: seq}); err != nil {
			t.Fatalf("Hide(%d): %v", seq, err)
		}
	}
	hide(1)
	hide(1)
	rooms, events := rg.events.list()
	if len(events) != 1 || rooms[0] != room {
		t.Fatalf("hiding twice enqueued %v to %v, want one event", events, rooms)
	}
	ev, h := events[0], events[0].GetMessageHidden()
	if ev.GetId() != pbconv.HiddenEventID(room, "bob", 0, 1) || ev.GetActor() != "bob" || !ev.GetTs().AsTime().Equal(rg.at()) ||
		h.GetUser() != "bob" || h.GetThreadRoot() != 0 || h.GetSeq() != 1 {
		t.Fatalf("event = %v, want message_hidden of seq 1 by bob at %v", ev, rg.at())
	}
	rg.events.err = errBoom
	hide(2)
	if _, events := rg.events.list(); len(events) != 2 || events[1].GetMessageHidden().GetSeq() != 2 {
		t.Fatalf("events = %v, want a second message_hidden despite the enqueue error", events)
	}
}

func TestClearingSendsHistoryClearedOnlyWhenTheMarkRises(t *testing.T) {
	rg := newRig(t, nil)
	first, later := rg.now, rg.now.Add(time.Minute)
	for _, now := range []time.Time{first, first.Add(-time.Hour), first, later} {
		rg.now = now
		if _, err := rg.m.ClearHistory(t.Context(), mutate.ClearCmd{Tenant: tenant, User: "bob", Room: room}); err != nil {
			t.Fatalf("ClearHistory at %v: %v", now, err)
		}
	}
	_, events := rg.events.list()
	if len(events) != 2 {
		t.Fatalf("events = %v, want one history_cleared per rise", events)
	}
	for i, at := range []time.Time{first.Truncate(time.Millisecond), later.Truncate(time.Millisecond)} {
		ev, c := events[i], events[i].GetHistoryCleared()
		if ev.GetId() != pbconv.ClearedEventID(room, "bob", at) || ev.GetActor() != "bob" || c.GetUser() != "bob" || !c.GetClearedAt().AsTime().Equal(at) {
			t.Fatalf("event %d = %v, want history_cleared of bob at %v", i, ev, at)
		}
	}
}
