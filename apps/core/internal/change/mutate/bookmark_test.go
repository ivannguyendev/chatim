package mutate_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func bookmark(user string, seq uint64, on bool) mutate.BookmarkCmd {
	return mutate.BookmarkCmd{Tenant: tenant, User: user, Room: room, Seq: seq, On: on}
}

func (rg *rig) mustBookmark(t *testing.T, c mutate.BookmarkCmd, wantChanged bool) {
	t.Helper()
	changed, err := rg.m.SetBookmark(t.Context(), c)
	if err != nil || changed != wantChanged {
		t.Fatalf("SetBookmark(%s, seq %d, on %v) = %v, %v; want changed %v", c.User, c.Seq, c.On, changed, err, wantChanged)
	}
}

func (rg *rig) bookmarkDoc(t *testing.T, seq uint64, user string) domain.Bookmark {
	t.Helper()
	doc, found, err := rg.reactions.GetBookmark(t.Context(), key(seq), user)
	if err != nil || !found {
		t.Fatalf("bookmark of %s on seq %d = %+v, %v, %v", user, seq, doc, found, err)
	}
	return doc
}

func TestBookmarkTwiceWritesOnceAndPublishesOnce(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	rg.mustBookmark(t, bookmark("bob", 1, true), true)
	on := rg.bookmarkDoc(t, 1, "bob")
	if !on.On || on.Ver != 1 || on.Tenant != tenant || !on.At.Equal(rg.at()) {
		t.Fatalf("bookmark = %+v, want on as version 1 at %v", on, rg.at())
	}
	rg.now = rg.now.Add(time.Second)
	rg.mustBookmark(t, bookmark("bob", 1, true), false)
	rooms, events := rg.events.list()
	if !slices.Equal(rooms, []uint64{room}) || len(events) != 1 || !proto.Equal(events[0], pbconv.BookmarkChanged(domain.Room{ID: room, Tenant: tenant, Type: domain.RoomGroup}, on)) {
		t.Fatalf("enqueued %v %v, want one bookmark_changed", rooms, events)
	}
	if events[0].GetId() != pbconv.BookmarkEventID(room, 0, 1, "bob", 1) {
		t.Fatalf("event id = %q", events[0].GetId())
	}
}

func TestRemovingABookmarkPublishesTheOffStateOnce(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	rg.mustBookmark(t, bookmark("carol", 1, false), false)
	rg.mustBookmark(t, bookmark("carol", 1, true), true)
	rg.now = rg.now.Add(time.Second)
	rg.mustBookmark(t, bookmark("carol", 1, false), true)
	rg.mustBookmark(t, bookmark("carol", 1, false), false)
	off := rg.bookmarkDoc(t, 1, "carol")
	if off.On || off.Ver != 2 {
		t.Fatalf("bookmark = %+v, want off as version 2", off)
	}
	_, events := rg.events.list()
	if len(events) != 2 || events[1].GetBookmarkChanged().GetOn() || events[1].GetId() != pbconv.BookmarkEventID(room, 0, 1, "carol", 2) {
		t.Fatalf("events = %v, want on then off", events)
	}
}

func TestBookmarkNeedsAnExistingLiveMessageToTurnOn(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	rg.send(t, 2, "alice", "ho")
	rg.mustBookmark(t, bookmark("bob", 1, true), true)
	for _, seq := range []uint64{1, 2} {
		if _, err := rg.m.Delete(t.Context(), del("alice", seq, 0)); err != nil {
			t.Fatalf("Delete seq %d: %v", seq, err)
		}
	}
	for _, seq := range []uint64{1, 2} {
		if _, err := rg.m.SetBookmark(t.Context(), bookmark("bob", seq, true)); !errors.Is(err, domain.ErrMessageDeleted) {
			t.Fatalf("bookmark deleted seq %d = %v, want ErrMessageDeleted", seq, err)
		}
	}
	if rg.bookmarkDoc(t, 1, "bob").Ver != 1 {
		t.Fatal("refused bookmark changed the stored one")
	}
	rg.mustBookmark(t, bookmark("bob", 1, false), true)
	if _, err := rg.m.SetBookmark(t.Context(), bookmark("bob", 9, true)); !errors.Is(err, domain.ErrMessageNotFound) {
		t.Fatalf("bookmark a missing message = %v, want ErrMessageNotFound", err)
	}
	bad := bookmark("bob", 1, true)
	bad.Thread = 3
	for name, c := range map[string]mutate.BookmarkCmd{"zero seq": bookmark("bob", 0, true), "thread": bad} {
		if _, err := rg.m.SetBookmark(t.Context(), c); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: SetBookmark = %v, want ErrInvalidArgument", name, err)
		}
	}
	if _, err := rg.m.SetBookmark(t.Context(), bookmark("dave", 1, true)); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("bookmark by a stranger = %v, want ErrNotMember", err)
	}
}

func TestBookmarkAsksThePolicyWithTheMessage(t *testing.T) {
	var asked []access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error { asked = append(asked, r); return access.ErrDenied })
	rg := newRig(t, deny)
	rg.send(t, 1, "alice", "hi")
	if _, err := rg.m.SetBookmark(t.Context(), bookmark("bob", 1, true)); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("SetBookmark = %v, want PermissionDenied", err)
	}
	if len(asked) != 1 || asked[0].Action != access.SetBookmark || asked[0].User != "bob" || asked[0].Author != "alice" {
		t.Fatalf("policy asked %+v, want set_bookmark by bob on alice's message", asked)
	}
	if _, found, _ := rg.reactions.GetBookmark(t.Context(), key(1), "bob"); found {
		t.Fatal("a denied bookmark was written")
	}
}
