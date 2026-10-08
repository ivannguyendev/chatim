package mongostore

import (
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func activeMember() bson.D {
	return bson.D{{Key: "$eq", Value: bson.A{"$state", int64(domain.MemberActive)}}}
}

func joinPipeline(j domain.Join, room, readSeq int64, user string) mongo.Pipeline {
	active := activeMember()
	keep := func(field string, next any) bson.E {
		return bson.E{Key: field, Value: bson.D{{Key: "$cond", Value: bson.A{active, "$" + field, next}}}}
	}
	orZero := func(field string, zero any) bson.D {
		return bson.D{{Key: "$ifNull", Value: bson.A{"$" + field, zero}}}
	}
	plusOne := func(field string) bson.D {
		return bson.D{{Key: "$add", Value: bson.A{orZero(field, int64(0)), int64(1)}}}
	}
	set := bson.D{
		{Key: "room_id", Value: room},
		{Key: "tenant", Value: literal(j.Tenant)},
		{Key: "user_id", Value: literal(user)},
		keep("role", literal(string(domain.RoleMember))),
		{Key: "state", Value: int64(domain.MemberActive)},
		keep("priority", int64(0)),
		keep("joined_at", j.At),
		keep("ver", plusOne("ver")),
		keep("previous_role", orZero("role", "")),
		keep("previous_state", orZero("state", int64(0))),
		keep("previous_priority", orZero("priority", int64(0))),
		keep("request_id", literal(j.RequestID)),
		keep("updated_at", j.At),
		keep("updated_by", literal(j.By)),
		keep("last_change_at", bson.D{{Key: "$max", Value: bson.A{"$last_change_at", j.At}}}),
		keep("read_seq", bson.D{{Key: "$max", Value: bson.A{orZero("read_seq", int64(0)), readSeq}}}),
		keep("read_ver", plusOne("read_ver")),
	}
	return mongo.Pipeline{{{Key: "$set", Value: set}}}
}
