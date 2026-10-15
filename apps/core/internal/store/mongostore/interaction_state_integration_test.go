package mongostore

import (
	"bytes"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func TestSameInteractionAgainLeavesTheDocByteIdenticalFlaggedOrNot(t *testing.T) {
	s, db := itStore(t, itClient(t))
	in, msg := s.Interactions(), keys.Msg(itRoom, 0, 5)
	r := domain.Reaction{Room: itRoom, Seq: 5, Tenant: "acme", User: "alice", Emoji: "👍", At: codecTime}
	b := domain.Bookmark{Room: itRoom, Seq: 5, Tenant: "acme", User: "alice", On: true, At: codecTime}
	for _, c := range []struct {
		kind keys.InteractionKind
		set  func(at time.Time) (bool, error)
	}{
		{keys.ReactionKind, func(at time.Time) (bool, error) {
			r.At = at
			_, ok, err := in.SetReaction(t.Context(), r)
			return ok, err
		}},
		{keys.BookmarkKind, func(at time.Time) (bool, error) {
			b.At = at
			_, ok, err := in.SetBookmark(t.Context(), b)
			return ok, err
		}},
	} {
		id := keys.InteractionUser(msg, c.kind, "alice")
		if ok, err := c.set(codecTime); err != nil || !ok {
			t.Fatalf("kind %d: first set = %v, %v; want changed", c.kind, ok, err)
		}
		flagged := rawInteraction(t, db, id)
		assertFlagAt(t, "first set", flagged, flagged.Lookup(stateAtField).Time())
		expectSameAgain(t, db, id, c.set, flagged)
		target := store.StateTarget{Collection: store.StateInteractions, Bit: store.BitInteraction, Room: itRoom, Seq: 5, User: "alice", Kind: c.kind, Ver: 1}
		if err := s.EventStates().Clear(t.Context(), []store.StateTarget{target}); err != nil {
			t.Fatalf("Clear: %v", err)
		}
		clean := rawInteraction(t, db, id)
		if _, err := clean.LookupErr(stateField); err == nil {
			t.Fatalf("kind %d: cleared doc %s still has %s", c.kind, clean, stateField)
		}
		expectSameAgain(t, db, id, c.set, clean)
	}
}

func expectSameAgain(t *testing.T, db *mongo.Database, id []byte, set func(time.Time) (bool, error), before []byte) {
	t.Helper()
	if ok, err := set(codecTime.Add(time.Hour)); err != nil || ok {
		t.Fatalf("set again = %v, %v; want no change", ok, err)
	}
	if after := rawInteraction(t, db, id); !bytes.Equal(before, after) {
		t.Fatalf("set again changed the doc: %s -> %s", before, after)
	}
}
