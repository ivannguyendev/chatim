package mongostore

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func interactionFields(key store.MsgKey, room int64, tenant string, kind keys.InteractionKind, actor string) bson.D {
	return bson.D{
		{Key: "message_key", Value: msgID(key)},
		{Key: "room_id", Value: room},
		{Key: "tenant", Value: tenant},
		{Key: "kind", Value: interactionKindNames[kind]},
		{Key: "actor_id", Value: actor},
	}
}

func asLiterals(fields bson.D) bson.D {
	out := make(bson.D, len(fields))
	for i, e := range fields {
		if v, ok := e.Value.(string); ok {
			e.Value = literal(v)
		}
		out[i] = e
	}
	return out
}

func setReaction(head bson.D, x domain.Reaction) mongo.Pipeline {
	live := bson.D{{Key: "$eq", Value: bson.A{"$state", interactionLive}}}
	same := bson.D{{Key: "$and", Value: bson.A{live, bson.D{{Key: "$eq", Value: bson.A{"$value", literal(x.Emoji)}}}}}}
	keep := func(field string, next any) bson.D {
		return bson.D{{Key: "$cond", Value: bson.A{same, "$" + field, next}}}
	}
	previous := bson.D{{Key: "$cond", Value: bson.A{live, "$value", ""}}}
	set := append(asLiterals(head),
		bson.E{Key: "value", Value: keep("value", literal(x.Emoji))},
		bson.E{Key: "previous_value", Value: keep("previous_value", previous)},
		bson.E{Key: "state", Value: keep("state", interactionLive)},
		bson.E{Key: "ver", Value: keep("ver", nextChange())},
		bson.E{Key: "created_at", Value: createdOnce(x.At)},
		bson.E{Key: "updated_at", Value: keep("updated_at", x.At)},
	)
	return mongo.Pipeline{{{Key: "$set", Value: set}}}
}

func setBookmark(head bson.D, at time.Time) mongo.Pipeline {
	same := bson.D{{Key: "$eq", Value: bson.A{"$state", interactionLive}}}
	keep := func(field string, next any) bson.D {
		return bson.D{{Key: "$cond", Value: bson.A{same, "$" + field, next}}}
	}
	set := append(asLiterals(head),
		bson.E{Key: "state", Value: interactionLive},
		bson.E{Key: "ver", Value: keep("ver", nextChange())},
		bson.E{Key: "created_at", Value: createdOnce(at)},
		bson.E{Key: "updated_at", Value: keep("updated_at", at)},
	)
	return mongo.Pipeline{{{Key: "$set", Value: set}}}
}

func removeReply(fields bson.D, at time.Time) mongo.Pipeline {
	live := bson.D{{Key: "$eq", Value: bson.A{"$state", interactionLive}}}
	ver := bson.D{{Key: "$cond", Value: bson.A{live, nextChange(), bson.D{{Key: "$ifNull", Value: bson.A{"$ver", int64(1)}}}}}}
	updated := bson.D{{Key: "$cond", Value: bson.A{live, at, bson.D{{Key: "$ifNull", Value: bson.A{"$updated_at", at}}}}}}
	set := append(asLiterals(fields),
		bson.E{Key: "state", Value: interactionRemoved},
		bson.E{Key: "ver", Value: ver},
		bson.E{Key: "created_at", Value: createdOnce(at)},
		bson.E{Key: "updated_at", Value: updated},
	)
	return mongo.Pipeline{{{Key: "$set", Value: set}}}
}

func removeInteraction(at time.Time, keepValue bool) mongo.Pipeline {
	set := bson.D{}
	if keepValue {
		set = append(set, bson.E{Key: "previous_value", Value: "$value"})
	}
	set = append(set,
		bson.E{Key: "state", Value: interactionRemoved},
		bson.E{Key: "ver", Value: nextChange()},
		bson.E{Key: "updated_at", Value: at},
	)
	return mongo.Pipeline{{{Key: "$set", Value: set}}}
}

func createdOnce(at time.Time) bson.D {
	return bson.D{{Key: "$ifNull", Value: bson.A{"$created_at", at}}}
}

func nextChange() bson.D {
	return bson.D{{Key: "$add", Value: bson.A{bson.D{{Key: "$ifNull", Value: bson.A{"$ver", 0}}}, 1}}}
}

func literal(v string) bson.D { return bson.D{{Key: "$literal", Value: v}} }
