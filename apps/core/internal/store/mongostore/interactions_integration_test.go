package mongostore

import (
	"bytes"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/storetest"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func TestMongoInteractionsContract(t *testing.T) {
	client := itClient(t)
	storetest.RunInteractions(t, func(t *testing.T) (storetest.ReactableMessages, store.Interactions) {
		s, _ := itStore(t, client)
		return s, s.Interactions()
	})
}

func TestMongoPinsContract(t *testing.T) {
	client := itClient(t)
	storetest.RunPins(t, func(t *testing.T) (storetest.PinnableRooms, store.Pins) {
		s, _ := itStore(t, client)
		return s, s.Pins()
	})
}

func rawInteraction(t *testing.T, db *mongo.Database, id []byte) bson.Raw {
	t.Helper()
	raw, err := db.Collection(interactionsCollection).FindOne(t.Context(), bson.D{{Key: "_id", Value: id}}).Raw()
	if err != nil {
		t.Fatalf("FindOne raw %x: %v", id, err)
	}
	return raw
}

func assertLayout(t *testing.T, op string, raw bson.Raw, want ...string) {
	t.Helper()
	if got := fieldNames(t, raw); !slices.Equal(got, want) {
		t.Fatalf("%s stored fields = %v, want %v", op, got, want)
	}
	if _, k, ok := raw.Lookup("message_key").BinaryOK(); !ok || len(k) != keys.MsgLen {
		t.Fatalf("%s stored message_key = %s, want 24 bytes", op, raw.Lookup("message_key"))
	}
}

var reactionLayout = []string{"_id", "message_key", "room_id", "tenant", "kind", "actor_id", "value", "previous_value", "state", "ver", "created_at", "updated_at"}

func TestReactionDocumentLayout(t *testing.T) {
	s, db := itStore(t, itClient(t))
	r := domain.Reaction{Room: itRoom, Seq: 1, Tenant: "acme", User: "alice", Emoji: "$e", At: codecTime}
	id := keys.InteractionUser(keys.Msg(itRoom, 0, 1), keys.ReactionKind, "alice")
	if _, _, err := s.Interactions().SetReaction(t.Context(), r); err != nil {
		t.Fatalf("SetReaction: %v", err)
	}
	raw := rawInteraction(t, db, id)
	assertLayout(t, "SetReaction", raw, reactionLayout...)
	if raw.Lookup("value").StringValue() != "$e" || raw.Lookup("kind").StringValue() != "reaction" || raw.Lookup("state").AsInt64() != 1 {
		t.Fatalf("stored reaction = %s, want value $e, kind reaction, state 1", raw)
	}
	var stored []bson.Raw
	for i, e := range []string{"👍", "👍"} {
		r.Emoji, r.At = e, codecTime.Add(time.Duration(i+1)*time.Second)
		if _, _, err := s.Interactions().SetReaction(t.Context(), r); err != nil {
			t.Fatalf("SetReaction(%s): %v", e, err)
		}
		raw := rawInteraction(t, db, id)
		assertLayout(t, "SetReaction("+e+")", raw, reactionLayout...)
		stored = append(stored, raw)
	}
	if !bytes.Equal(stored[0], stored[1]) {
		t.Fatalf("SetReaction of the same emoji changed the doc: %s -> %s", stored[0], stored[1])
	}
	if _, _, err := s.Interactions().RemoveReaction(t.Context(), store.MsgKey{Room: itRoom, Seq: 1}, "alice", codecTime.Add(time.Minute)); err != nil {
		t.Fatalf("RemoveReaction: %v", err)
	}
	gone := rawInteraction(t, db, id)
	assertLayout(t, "RemoveReaction", gone, reactionLayout...)
	if gone.Lookup("value").StringValue() != "👍" || gone.Lookup("state").AsInt64() != 2 || gone.Lookup("ver").AsInt64() != 3 {
		t.Fatalf("removed reaction = %s, want value 👍 kept, state 2, ver 3", gone)
	}
}

func TestBookmarkAndReplyDocumentLayout(t *testing.T) {
	s, db := itStore(t, itClient(t))
	in, msg := s.Interactions(), keys.Msg(itRoom, 0, 40)
	b := domain.Bookmark{Room: itRoom, Seq: 40, Tenant: "acme", User: "minh", On: true, At: codecTime}
	if _, _, err := in.SetBookmark(t.Context(), b); err != nil {
		t.Fatalf("SetBookmark: %v", err)
	}
	id := keys.InteractionUser(msg, keys.BookmarkKind, "minh")
	first := rawInteraction(t, db, id)
	assertLayout(t, "SetBookmark", first, "_id", "message_key", "room_id", "tenant", "kind", "actor_id", "state", "ver", "created_at", "updated_at")
	b.At = codecTime.Add(time.Hour)
	if _, changed, err := in.SetBookmark(t.Context(), b); err != nil || changed {
		t.Fatalf("SetBookmark(again) = %v, %v; want no change", changed, err)
	}
	if again := rawInteraction(t, db, id); !bytes.Equal(first, again) {
		t.Fatalf("SetBookmark of the same state changed the doc: %s -> %s", first, again)
	}
	reply := domain.Reply{Parent: domain.MsgKey{Room: itRoom, Seq: 40}, Room: itRoom, Seq: 57, Tenant: "acme", From: "lan", At: codecTime}
	if _, err := in.AddReply(t.Context(), reply); err != nil {
		t.Fatalf("AddReply: %v", err)
	}
	raw := rawInteraction(t, db, keys.InteractionReply(msg, 0, 57))
	assertLayout(t, "AddReply", raw, "_id", "actor_id", "created_at", "kind", "message_key", "reply_seq", "room_id", "state", "tenant", "updated_at", "ver")
	if raw.Lookup("kind").StringValue() != "reply" || raw.Lookup("reply_seq").AsInt64() != 57 || raw.Lookup("actor_id").StringValue() != "lan" {
		t.Fatalf("stored reply = %s", raw)
	}
}

func TestReactionSetCountsEveryChangeOnceUnderRacingDevices(t *testing.T) {
	s, _ := itStore(t, itClient(t))
	reactions := s.Interactions()
	emojis := []string{"👍", "❤️", "😂", "🎉"}
	var changes atomic.Int64
	var wg sync.WaitGroup
	for _, e := range emojis {
		wg.Go(func() {
			for range 10 {
				r := domain.Reaction{Room: itRoom, Seq: 1, Tenant: "acme", User: "alice", Emoji: e, At: codecTime}
				_, changed, err := reactions.SetReaction(t.Context(), r)
				if err != nil {
					t.Errorf("SetReaction(%s) = %v, want nil", e, err)
				}
				if changed {
					changes.Add(1)
				}
			}
		})
	}
	wg.Wait()
	got, ok, err := reactions.GetReaction(t.Context(), store.MsgKey{Room: itRoom, Seq: 1}, "alice")
	if err != nil || !ok || int64(got.N) != changes.Load() || !slices.Contains(emojis, got.Emoji) {
		t.Fatalf("final = %+v, %v, %v; want change number %d and one of %v", got, ok, err, changes.Load(), emojis)
	}
}

func TestReactionSetOfTheSameUserRacingOnANewReactionNeverFails(t *testing.T) {
	s, _ := itStore(t, itClient(t))
	reactions := s.Interactions()
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
				_, changed, err := reactions.SetReaction(t.Context(), r)
				if err != nil {
					t.Errorf("round %d: SetReaction(%s) = %v, want nil", round, e, err)
				}
				if changed {
					changes.Add(1)
				}
			})
		}
		close(start)
		wg.Wait()
		got, ok, err := reactions.GetReaction(t.Context(), store.MsgKey{Room: itRoom, Seq: seq}, "alice")
		want := int64(len(slices.Compact(emojis[:])))
		if err != nil || !ok || int64(got.N) != changes.Load() || changes.Load() != want {
			t.Fatalf("round %d: final = %+v, %v, %v after %d changes; want change number %d", round, got, ok, err, changes.Load(), want)
		}
	}
}
