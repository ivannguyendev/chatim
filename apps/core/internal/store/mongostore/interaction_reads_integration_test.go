package mongostore

import (
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func walkWinning(doc bson.Raw, visit func(bson.Raw)) {
	elems, _ := doc.Elements()
	for _, e := range elems {
		if e.Key() == "winningPlan" {
			walkStages(e.Value(), visit)
			continue
		}
		if d, ok := e.Value().DocumentOK(); ok {
			walkWinning(d, visit)
		}
		if a, ok := e.Value().ArrayOK(); ok {
			vals, _ := a.Values()
			for _, v := range vals {
				if d, ok := v.DocumentOK(); ok {
					walkWinning(d, visit)
				}
			}
		}
	}
}

func winningPlan(t *testing.T, db *mongo.Database, cmd bson.D) (stages, indexes []string) {
	t.Helper()
	raw, err := db.RunCommand(t.Context(), bson.D{{Key: "explain", Value: cmd}, {Key: "verbosity", Value: "queryPlanner"}}).Raw()
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	walkWinning(raw, func(d bson.Raw) {
		stages = append(stages, d.Lookup("stage").StringValue())
		if name, ok := d.Lookup("indexName").StringValueOK(); ok {
			indexes = append(indexes, name)
		}
	})
	return stages, indexes
}

func TestReactionCountIsCoveredByTheInteractionIndex(t *testing.T) {
	s, db := itStore(t, itClient(t))
	for user, emoji := range map[string]string{"alice": "👍", "bob": "👍", "carol": "❤️"} {
		r := domain.Reaction{Room: itRoom, Seq: 1, Tenant: "acme", User: user, Emoji: emoji, At: codecTime}
		if _, _, err := s.Interactions().SetReaction(t.Context(), r); err != nil {
			t.Fatalf("SetReaction: %v", err)
		}
	}
	key := store.MsgKey{Room: itRoom, Seq: 1}
	aggregate := bson.D{{Key: "aggregate", Value: interactionsCollection}, {Key: "pipeline", Value: countPipeline(key)}, {Key: "cursor", Value: bson.D{}}}
	stages, indexes := winningPlan(t, db, aggregate)
	const index = "message_key_1_kind_1_state_1_value_1"
	if !slices.Contains(indexes, index) || slices.Contains(stages, "FETCH") || slices.Contains(stages, "COLLSCAN") {
		t.Fatalf("winning plan stages %v on indexes %v, want a covered scan of %s without FETCH", stages, indexes, index)
	}
	want := []domain.ReactionCount{{Emoji: "👍", Count: 2}, {Emoji: "❤️", Count: 1}}
	if got, err := s.Interactions().CountReactions(t.Context(), key); err != nil || !slices.Equal(got, want) {
		t.Fatalf("CountReactions = %v, %v; want %v", got, err, want)
	}
	if got, err := s.Interactions().CountWitnessed(t.Context(), key, []store.Witness{{User: "alice", N: 1}}); err != nil || !slices.Equal(got, want) {
		t.Fatalf("CountWitnessed = %v, %v; want %v", got, err, want)
	}
}

func TestRepliesAndBookmarksReadThroughTheirKeys(t *testing.T) {
	_, db := itStore(t, itClient(t))
	parent := store.MsgKey{Room: itRoom, Seq: 40}
	lo := keys.InteractionReply(msgID(parent), 0, 0)
	_, hi := keys.InteractionReplyRange(msgID(parent))
	replies := bson.D{
		{Key: "find", Value: interactionsCollection},
		{Key: "filter", Value: bson.D{{Key: "_id", Value: bson.D{{Key: "$gt", Value: lo}, {Key: "$lt", Value: hi}}}, {Key: "state", Value: interactionLive}}},
		{Key: "sort", Value: bson.D{{Key: "_id", Value: 1}}}, {Key: "limit", Value: 10},
	}
	if stages, _ := winningPlan(t, db, replies); !slices.Contains(stages, "CLUSTERED_IXSCAN") || slices.Contains(stages, "SORT") {
		t.Fatalf("replies plan stages %v, want a bounded clustered scan without SORT", stages)
	}
	const index = "tenant_1_actor_id_1_kind_1_state_1_updated_at_-1_message_key_-1"
	for name, before := range map[string]store.BookmarkCursor{"first page": {}, "after a cursor": {At: codecTime, Key: parent}} {
		filter, sort := bookmarkQuery("acme", "minh", before)
		cmd := bson.D{{Key: "find", Value: interactionsCollection}, {Key: "filter", Value: filter}, {Key: "sort", Value: sort}, {Key: "limit", Value: 10}}
		stages, indexes := winningPlan(t, db, cmd)
		if !slices.Contains(indexes, index) || slices.Contains(stages, "SORT") || slices.Contains(stages, "COLLSCAN") {
			t.Fatalf("bookmarks %s plan stages %v on indexes %v, want %s without SORT", name, stages, indexes, index)
		}
	}
}
