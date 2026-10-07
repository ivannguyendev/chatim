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
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("hide enqueued %v, want no event", events)
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
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("clear enqueued %v, want no event", events)
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
