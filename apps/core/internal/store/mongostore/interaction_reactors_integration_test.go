package mongostore

import (
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestReactorsAreCoveredByTheInteractionIndexWithoutSort(t *testing.T) {
	s, db := itStore(t, itClient(t))
	for user, emoji := range map[string]string{"alice": "👍", "bob": "👍", "carol": "❤️"} {
		r := domain.Reaction{Room: itRoom, Seq: 1, Tenant: "acme", User: user, Emoji: emoji, At: codecTime}
		if _, _, err := s.Interactions().SetReaction(t.Context(), r); err != nil {
			t.Fatalf("SetReaction: %v", err)
		}
	}
	key := store.MsgKey{Room: itRoom, Seq: 1}
	const index = "message_key_1_kind_1_state_1_value_1_updated_at_-1_actor_id_1"
	queries := map[string]store.ReactorQuery{
		"one emoji":                  {Key: key, Emoji: "👍", Limit: 50},
		"one emoji after a cursor":   {Key: key, Emoji: "👍", Limit: 50, After: store.ReactorCursor{At: codecTime, User: "alice"}},
		"every emoji":                {Key: key, Limit: 50},
		"every emoji after a cursor": {Key: key, Limit: 50, After: store.ReactorCursor{Emoji: "❤️", At: codecTime, User: "carol"}},
	}
	for name, q := range queries {
		filter, sort := reactorQuery(q)
		find := bson.D{
			{Key: "find", Value: interactionsCollection}, {Key: "filter", Value: filter}, {Key: "sort", Value: sort},
			{Key: "projection", Value: reactorProjection}, {Key: "limit", Value: q.Limit},
		}
		stages, indexes := winningPlan(t, db, find)
		if !slices.Contains(indexes, index) || slices.Contains(stages, "FETCH") || slices.Contains(stages, "SORT") || slices.Contains(stages, "COLLSCAN") {
			t.Fatalf("%s plan stages %v on indexes %v, want a covered scan of %s without FETCH or SORT", name, stages, indexes, index)
		}
	}
}
