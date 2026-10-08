package mongostore

import (
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
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

func TestReactionCountIsCoveredByTheEmojiIndex(t *testing.T) {
	s, db := itStore(t, itClient(t))
	for user, emoji := range map[string]string{"alice": "👍", "bob": "👍", "carol": "❤️"} {
		r := domain.Reaction{Room: itRoom, Seq: 1, Tenant: "acme", User: user, Emoji: emoji, At: codecTime}
		if _, _, err := s.Reactions().Set(t.Context(), r); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}
	key := store.MsgKey{Room: itRoom, Seq: 1}
	aggregate := bson.D{{Key: "aggregate", Value: reactionsCollection}, {Key: "pipeline", Value: countPipeline(key)}, {Key: "cursor", Value: bson.D{}}}
	raw, err := db.RunCommand(t.Context(), bson.D{{Key: "explain", Value: aggregate}, {Key: "verbosity", Value: "queryPlanner"}}).Raw()
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	var stages, indexes []string
	walkWinning(raw, func(d bson.Raw) {
		stages = append(stages, d.Lookup("stage").StringValue())
		if name, ok := d.Lookup("indexName").StringValueOK(); ok {
			indexes = append(indexes, name)
		}
	})
	if !slices.Contains(indexes, "message_key_1_emoji_1") || slices.Contains(stages, "FETCH") || slices.Contains(stages, "COLLSCAN") {
		t.Fatalf("winning plan stages %v on indexes %v, want a covered scan of message_key_1_emoji_1 without FETCH", stages, indexes)
	}
	got, err := s.Reactions().Count(t.Context(), key, []store.Witness{{User: "alice", N: 1}})
	if err != nil || !slices.Equal(got, []domain.ReactionCount{{Emoji: "👍", Count: 2}, {Emoji: "❤️", Count: 1}}) {
		t.Fatalf("Count = %v, %v", got, err)
	}
}
