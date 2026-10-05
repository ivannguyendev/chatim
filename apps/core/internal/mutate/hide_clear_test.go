package mutate_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
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
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("hide enqueued %v, want no event", events)
	}
}

func TestClearHistoryOnlyRaisesTheMark(t *testing.T) {
	rg := newRig(t, nil)
	for seq := range uint64(3) {
		rg.send(t, seq+1, "alice", "m")
	}
	clearTo := func(user string, upTo uint64) uint64 {
		t.Helper()
		n, err := rg.m.ClearHistory(t.Context(), mutate.ClearCmd{Tenant: tenant, User: user, Room: room, UpToSeq: upTo})
		if err != nil {
			t.Fatalf("ClearHistory(%s, %d): %v", user, upTo, err)
		}
		return n
	}
	cases := []struct {
		user       string
		upTo, want uint64
	}{
		{"bob", 0, 3},
		{"bob", 1, 3},
		{"alice", 2, 2},
		{"carol", 99, 3},
	}
	for _, c := range cases {
		if got := clearTo(c.user, c.upTo); got != c.want {
			t.Fatalf("ClearHistory(%s, %d) = %d, want %d", c.user, c.upTo, got, c.want)
		}
	}
	m, err := rg.rooms.Member(t.Context(), room, "bob")
	if err != nil || m.ClearedBeforeSeq != 3 {
		t.Fatalf("bob member = %+v, %v; want cleared before 3", m, err)
	}
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("clear enqueued %v, want no event", events)
	}
}

func TestClearHistoryOfAnEmptyRoomKeepsZero(t *testing.T) {
	rg := newRig(t, nil)
	n, err := rg.m.ClearHistory(t.Context(), mutate.ClearCmd{Tenant: tenant, User: "bob", Room: room})
	if err != nil || n != 0 {
		t.Fatalf("ClearHistory = %d, %v; want 0", n, err)
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
