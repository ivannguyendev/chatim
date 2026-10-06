package mongostore

import (
	"bytes"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/storetest"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func TestMongoReactionsContract(t *testing.T) {
	client := itClient(t)
	storetest.RunReactions(t, func(t *testing.T) (storetest.ReactableMessages, store.Reactions) {
		s, _ := itStore(t, client)
		return s, s.Reactions()
	})
}

func TestMongoPinsContract(t *testing.T) {
	client := itClient(t)
	storetest.RunPins(t, func(t *testing.T) (storetest.PinnableRooms, store.Pins) {
		s, _ := itStore(t, client)
		return s, s.Pins()
	})
}

func TestReactionDocumentLayout(t *testing.T) {
	s, db := itStore(t, itClient(t))
	r := domain.Reaction{Room: itRoom, Seq: 1, Tenant: "acme", User: "alice", Emoji: "$e", At: codecTime}
	if _, _, err := s.Reactions().Set(t.Context(), r); err != nil {
		t.Fatalf("Set: %v", err)
	}
	raw, err := db.Collection(reactionsCollection).FindOne(t.Context(), bson.D{{Key: "_id", Value: keys.Reaction(itRoom, 0, 1, "alice")}}).Raw()
	if err != nil {
		t.Fatalf("FindOne raw: %v", err)
	}
	if got, want := fieldNames(t, raw), []string{"_id", "k", "r", "t", "u", "pe", "e", "n", "ts"}; !slices.Equal(got, want) {
		t.Fatalf("stored fields = %v, want %v", got, want)
	}
	if e := raw.Lookup("e").StringValue(); e != "$e" {
		t.Fatalf("stored emoji = %q, want $e", e)
	}
	if _, k, ok := raw.Lookup("k").BinaryOK(); !ok || !bytes.Equal(k, keys.Msg(itRoom, 0, 1)) {
		t.Fatalf("stored k = %s, want keys.Msg", raw.Lookup("k"))
	}
}

func TestReactionSetCountsEveryChangeOnceUnderRacingDevices(t *testing.T) {
	s, _ := itStore(t, itClient(t))
	reactions := s.Reactions()
	emojis := []string{"👍", "❤️", "😂", "🎉"}
	var changes atomic.Int64
	var wg sync.WaitGroup
	for _, e := range emojis {
		wg.Go(func() {
			for range 10 {
				r := domain.Reaction{Room: itRoom, Seq: 1, Tenant: "acme", User: "alice", Emoji: e, At: codecTime}
				_, changed, err := reactions.Set(t.Context(), r)
				if err != nil && !errors.Is(err, store.ErrReactionContended) {
					t.Errorf("Set(%s) = %v, want nil or ErrReactionContended", e, err)
				}
				if changed {
					changes.Add(1)
				}
			}
		})
	}
	wg.Wait()
	got, ok, err := reactions.Get(t.Context(), store.MsgKey{Room: itRoom, Seq: 1}, "alice")
	if err != nil || !ok || int64(got.N) != changes.Load() || !slices.Contains(emojis, got.Emoji) {
		t.Fatalf("final = %+v, %v, %v; want change number %d and one of %v", got, ok, err, changes.Load(), emojis)
	}
}
