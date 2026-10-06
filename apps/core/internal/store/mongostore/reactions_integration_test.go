package mongostore

import (
	"bytes"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	var stored []bson.Raw
	for i, e := range []string{"👍", "👍"} {
		r.Emoji, r.At = e, codecTime.Add(time.Duration(i+1)*time.Second)
		if _, _, err := s.Reactions().Set(t.Context(), r); err != nil {
			t.Fatalf("Set(%s): %v", e, err)
		}
		raw, err := db.Collection(reactionsCollection).FindOne(t.Context(), bson.D{{Key: "_id", Value: keys.Reaction(itRoom, 0, 1, "alice")}}).Raw()
		if err != nil {
			t.Fatalf("FindOne raw: %v", err)
		}
		if got, want := fieldNames(t, raw), []string{"_id", "k", "r", "t", "u", "pe", "e", "n", "ts"}; !slices.Equal(got, want) {
			t.Fatalf("stored fields after Set(%s) = %v, want %v", e, got, want)
		}
		stored = append(stored, raw)
	}
	if !bytes.Equal(stored[0], stored[1]) {
		t.Fatalf("Set of the same emoji changed the doc: %s -> %s", stored[0], stored[1])
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
				if err != nil {
					t.Errorf("Set(%s) = %v, want nil", e, err)
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

func TestReactionSetOfTheSameUserRacingOnANewReactionNeverFails(t *testing.T) {
	s, _ := itStore(t, itClient(t))
	reactions := s.Reactions()
	for round := range uint64(40) {
		seq := round + 1
		emojis := [2]string{"👍", "👍"}
		if round%2 == 1 {
			emojis[1] = "❤️"
		}
		start := make(chan struct{})
		var changes atomic.Int64
		var wg sync.WaitGroup
		for _, e := range emojis {
			wg.Go(func() {
				<-start
				r := domain.Reaction{Room: itRoom, Seq: seq, Tenant: "acme", User: "alice", Emoji: e, At: codecTime}
				_, changed, err := reactions.Set(t.Context(), r)
				if err != nil {
					t.Errorf("round %d: Set(%s) = %v, want nil", round, e, err)
				}
				if changed {
					changes.Add(1)
				}
			})
		}
		close(start)
		wg.Wait()
		got, ok, err := reactions.Get(t.Context(), store.MsgKey{Room: itRoom, Seq: seq}, "alice")
		want := int64(len(slices.Compact(emojis[:])))
		if err != nil || !ok || int64(got.N) != changes.Load() || changes.Load() != want {
			t.Fatalf("round %d: final = %+v, %v, %v after %d changes; want change number %d", round, got, ok, err, changes.Load(), want)
		}
	}
}
