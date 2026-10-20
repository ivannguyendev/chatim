package member_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/member"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestClearHistoryMarksTheServerTimeAndOnlyRaisesIt(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "m")
	clearAt := func(user string, now time.Time) (time.Time, error) {
		rg.now = now
		return rg.m.ClearHistory(t.Context(), member.ClearCmd{Tenant: tenant, User: user, Room: room})
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

func TestClearAsksThePolicy(t *testing.T) {
	var asked []access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error { asked = append(asked, r); return access.ErrDenied })
	rg := newRig(t, deny)
	rg.send(t, 1, "alice", "hi")
	if _, err := rg.m.ClearHistory(t.Context(), member.ClearCmd{Tenant: tenant, User: "bob", Room: room}); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("ClearHistory = %v, want PermissionDenied", err)
	}
	if len(asked) != 1 || asked[0].Action != access.ClearHistory || asked[0].Author != "" {
		t.Fatalf("policy asked %+v, want clear_history without an author", asked)
	}
}

func TestClearingSendsHistoryClearedOnlyWhenTheMarkRises(t *testing.T) {
	rg := newRig(t, nil)
	first, later := rg.now, rg.now.Add(time.Minute)
	for _, now := range []time.Time{first, first.Add(-time.Hour), first, later} {
		rg.now = now
		if _, err := rg.m.ClearHistory(t.Context(), member.ClearCmd{Tenant: tenant, User: "bob", Room: room}); err != nil {
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
