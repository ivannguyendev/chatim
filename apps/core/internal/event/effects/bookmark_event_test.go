package effects_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

type bookmarkRig struct {
	marks *memstore.Interactions
	js    *publishtest.JetStream
	room  domain.Room
	event *effects.BookmarkEvent
}

func newBookmarkRig(t *testing.T) *bookmarkRig {
	t.Helper()
	rooms := memstore.NewRooms()
	rg := &bookmarkRig{marks: memstore.NewInteractions(), js: &publishtest.JetStream{}, room: createRoom(t, rooms, room)}
	rg.event = built(effects.NewBookmarkEvent(
		effects.BookmarkEventDeps{Bookmarks: rg.marks, Rooms: rooms, JS: rg.js},
		effects.MessageChangedConfig{SubjectRoot: "evt", Delay: delay, RoomCache: 16},
	))(t)
	return rg
}

func (rg *bookmarkRig) set(t *testing.T, seq uint64, on bool) domain.Bookmark {
	t.Helper()
	return rg.setIn(t, room, seq, on)
}

func (rg *bookmarkRig) setIn(t *testing.T, r, seq uint64, on bool) domain.Bookmark {
	t.Helper()
	b := domain.Bookmark{Room: r, Seq: seq, Tenant: tenant, User: "bob", On: on, At: memberNow}
	got, _, err := rg.marks.SetBookmark(t.Context(), b)
	if err != nil {
		t.Fatalf("SetBookmark(%d, %v): %v", seq, on, err)
	}
	return got
}

func bookmarkRec(r, seq uint64, ver uint32) work.Record {
	return work.Record{Kind: store.BookmarkChanged, Room: r, Seq: seq, User: "bob", Version: ver, CommittedAt: time.Now()}
}

func TestBookmarkEventRepublishesTheCurrentDocOnTheMemberSubject(t *testing.T) {
	rg := newBookmarkRig(t)
	if e := rg.event.Effect(); e.Name != effects.BookmarkEventName || e.Delay != delay {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.BookmarkEventName, delay)
	}
	rg.set(t, 3, true)
	off := rg.set(t, 3, false)
	errs := rg.event.Effect().Run(t.Context(), []work.Record{bookmarkRec(room, 3, 1), bookmarkRec(room, 3, 2)})
	if !allNil(errs, 2) {
		t.Fatalf("errs = %v", errs)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.BookmarkEventID(room, 0, 3, "bob", 2)}) {
		t.Fatalf("stored = %v, want only the current version", got)
	}
	if subj := rg.js.Stored()[0].Subject; subj != "evt.acme.member.4242.bookmark_changed" {
		t.Fatalf("subject = %q", subj)
	}
	events, err := rg.js.Events()
	if want := pbconv.BookmarkChanged(rg.room, off); err != nil || !proto.Equal(events[0], want) {
		t.Fatalf("event = %v, %v; want the fast path event %v", events, err, want)
	}
	if rg.event.Republished() != 1 || rg.event.Dropped() != 0 {
		t.Fatalf("republished %d, dropped %d; want 1 and 0", rg.event.Republished(), rg.event.Dropped())
	}
}

func TestBookmarkEventRetriesAheadOfTheReadAndDropsWhatIsGone(t *testing.T) {
	rg := newBookmarkRig(t)
	rg.set(t, 3, true)
	rg.setIn(t, otherRoom, 3, true)
	errs := rg.event.Effect().Run(t.Context(), []work.Record{bookmarkRec(room, 3, 2), bookmarkRec(room, 4, 1), bookmarkRec(otherRoom, 3, 1)})
	if len(errs) != 3 || !errors.Is(errs[0], store.ErrStaleRead) || errs[1] != nil || errs[2] != nil {
		t.Fatalf("errs = %v, want the newer version retried and the rest dropped", errs)
	}
	if rg.event.Dropped() != 2 || len(rg.js.Attempts()) != 0 {
		t.Fatalf("dropped %d, attempts %d; want the missing bookmark and the missing room dropped and nothing published", rg.event.Dropped(), len(rg.js.Attempts()))
	}
	eff := built(effects.NewBookmarkEvent(
		effects.BookmarkEventDeps{Bookmarks: brokenReactions{}, Rooms: brokenStore{}, JS: &publishtest.JetStream{}},
		effects.MessageChangedConfig{SubjectRoot: "evt"},
	))(t)
	if errs := eff.Effect().Run(t.Context(), []work.Record{bookmarkRec(room, 3, 1)}); len(errs) != 1 || !errors.Is(errs[0], errBoom) || eff.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want a retryable failure", errs, eff.Dropped())
	}
	if _, err := effects.NewBookmarkEvent(effects.BookmarkEventDeps{}, effects.MessageChangedConfig{SubjectRoot: "evt"}); err == nil {
		t.Fatal("NewBookmarkEvent without deps = nil error")
	}
}

func (brokenReactions) GetBookmark(context.Context, store.MsgKey, string) (domain.Bookmark, bool, error) {
	return domain.Bookmark{}, false, errBoom
}
