package effects_test

import (
	"errors"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

func (rg *memberRig) markRead(t *testing.T, user string, seq uint64) domain.ReadPosition {
	t.Helper()
	pos, moved, err := rg.rooms.MarkRead(t.Context(), room, user, seq)
	if err != nil || !moved {
		t.Fatalf("MarkRead(%s, %d) = %+v, %v, %v", user, seq, pos, moved, err)
	}
	return pos
}

func TestReadEventRepublishesTheCurrentPosition(t *testing.T) {
	rg := newMemberRig(t)
	if e := rg.read.Effect(); e.Name != effects.ReadEventName || e.Delay != delay {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.ReadEventName, delay)
	}
	pos := rg.markRead(t, "alice", 7)
	if errs := rg.read.Effect().Run(t.Context(), []work.Record{readRec(room, "alice", uint32(pos.Ver))}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.ReadEventID(room, "alice", pos.Ver)}) {
		t.Fatalf("stored = %v", got)
	}
	if subj := rg.js.Stored()[0].Subject; subj != "evt.acme.member.4242.read_updated" {
		t.Fatalf("subject = %q", subj)
	}
	events, err := rg.js.Events()
	if want := pbconv.ReadUpdated(rg.room, "alice", pos, memberNow); err != nil || !proto.Equal(events[0], want) {
		t.Fatalf("event = %v, %v; want %v", events, err, want)
	}
	if rg.read.Republished() != 1 || rg.read.Dropped() != 0 {
		t.Fatalf("republished %d, dropped %d; want 1 and 0", rg.read.Republished(), rg.read.Dropped())
	}
}

func TestReadEventSkipsANewerPositionAndRetriesAnOlderOne(t *testing.T) {
	rg := newMemberRig(t)
	rg.markRead(t, "alice", 3)
	pos := rg.markRead(t, "alice", 5)
	older, newer := uint32(pos.Ver-1), uint32(pos.Ver+1)
	errs := rg.read.Effect().Run(t.Context(), []work.Record{readRec(room, "alice", older), readRec(room, "alice", newer), readRec(room, "carol", 1)})
	if len(errs) != 3 || errs[0] != nil || !errors.Is(errs[1], store.ErrStaleRead) || errs[2] != nil {
		t.Fatalf("errs = %v, want only the change ahead of the read retried", errs)
	}
	if len(rg.js.Attempts()) != 0 || rg.read.Dropped() != 1 {
		t.Fatalf("attempts %d, dropped %d; want nothing sent and the missing member dropped", len(rg.js.Attempts()), rg.read.Dropped())
	}
}
