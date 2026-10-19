package mutate_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
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

func TestHideAsksThePolicy(t *testing.T) {
	var asked []access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error { asked = append(asked, r); return access.ErrDenied })
	rg := newRig(t, deny)
	rg.send(t, 1, "alice", "hi")
	if err := rg.m.Hide(t.Context(), mutate.HideCmd{Tenant: tenant, User: "bob", Room: room, Seq: 1}); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("Hide = %v, want PermissionDenied", err)
	}
	if len(asked) != 1 || asked[0].Action != access.HideMessage || asked[0].Author != "alice" {
		t.Fatalf("policy asked %+v, want hide_message on alice's message", asked)
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
