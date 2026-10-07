package mongostore

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func (s *Store) AddMembers(ctx context.Context, j domain.Join, users []string) (store.JoinResult, error) {
	if err := store.ValidateJoin(j, users); err != nil {
		return store.JoinResult{}, err
	}
	room, err := toInt64("room id", j.Room)
	if err != nil {
		return store.JoinResult{}, err
	}
	readSeq, err := toInt64("read seq", j.ReadSeq)
	if err != nil {
		return store.JoinResult{}, err
	}
	models := make([]mongo.WriteModel, len(users))
	for i, u := range users {
		models[i] = mongo.NewUpdateOneModel().SetFilter(memberFilter(j.Room, u)).SetUpdate(joinPipeline(j, room, readSeq, u)).SetUpsert(true)
	}
	res, err := s.members.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
	if err != nil {
		return store.JoinResult{}, fmt.Errorf("add %d members to room %d: %w", len(users), j.Room, err)
	}
	docs, err := s.MembersOf(ctx, j.Room, users)
	if err != nil {
		return store.JoinResult{}, fmt.Errorf("add %d members to room %d: %w", len(users), j.Room, err)
	}
	return store.JoinResult{Members: docs, Changed: int(res.UpsertedCount + res.ModifiedCount)}, nil
}

func (s *Store) ApplyMember(ctx context.Context, cur, next domain.Member) (bool, error) {
	if err := store.ValidateMemberChange(cur, next); err != nil {
		return false, err
	}
	return applyMember(ctx, s.members, cur, next)
}

func applyMember(ctx context.Context, coll *mongo.Collection, cur, next domain.Member) (bool, error) {
	filter := append(memberFilter(cur.Room, cur.User), bson.E{Key: "ver", Value: int64(cur.Ver)})
	res, err := coll.UpdateOne(ctx, filter, membershipUpdate(next))
	if err != nil {
		return false, fmt.Errorf("apply member %q of room %d ver %d: %w", cur.User, cur.Room, next.Ver, err)
	}
	return res.MatchedCount == 1, nil
}

func membershipUpdate(next domain.Member) bson.D {
	set := bson.D{
		{Key: "role", Value: next.Role},
		{Key: "state", Value: int64(next.State)},
		{Key: "priority", Value: int64(next.Priority)},
		{Key: "previous_role", Value: next.PreviousRole},
		{Key: "previous_state", Value: int64(next.PreviousState)},
		{Key: "previous_priority", Value: int64(next.PreviousPriority)},
		{Key: "request_id", Value: next.RequestID},
		{Key: "updated_by", Value: next.UpdatedBy},
		{Key: "updated_at", Value: next.UpdatedAt},
	}
	return bson.D{
		{Key: "$set", Value: set},
		{Key: "$max", Value: bson.D{{Key: "last_change_at", Value: next.LastChangeAt}}},
		{Key: "$inc", Value: bson.D{{Key: "ver", Value: int64(1)}}},
	}
}

func (s *Store) MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error) {
	if err := store.ValidateLimit(len(users), domain.MaxMemberBatch+1); err != nil {
		return nil, err
	}
	return membersOf(ctx, s.members, room, users)
}

func membersOf(ctx context.Context, coll *mongo.Collection, room uint64, users []string) ([]domain.Member, error) {
	ids := make(bson.A, len(users))
	for i, u := range users {
		ids[i] = keys.Member(room, u)
	}
	found, err := findMembers(ctx, coll, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}})
	if err != nil {
		return nil, fmt.Errorf("members of room %d: %w", room, err)
	}
	byUser := make(map[string]domain.Member, len(found))
	for _, m := range found {
		byUser[m.User] = m
	}
	out := make([]domain.Member, 0, len(found))
	for _, u := range users {
		if m, ok := byUser[u]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func (s *Store) MembersBetween(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Member, error) {
	if err := store.ValidateLimit(limit, store.MaxMemberScan); err != nil {
		return nil, err
	}
	key, err := toInt64("room id", room)
	if err != nil {
		return nil, err
	}
	filter := bson.D{{Key: "room_id", Value: key}, {Key: "last_change_at", Value: bson.D{{Key: "$gte", Value: from}, {Key: "$lte", Value: to}}}}
	opts := options.Find().SetSort(bson.D{{Key: "last_change_at", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(int64(limit))
	out, err := findMembers(ctx, s.members, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("members of room %d changed in [%v, %v]: %w", room, from, to, err)
	}
	return out, nil
}

func findMembers(ctx context.Context, coll *mongo.Collection, filter bson.D, opts ...options.Lister[options.FindOptions]) ([]domain.Member, error) {
	cur, err := coll.Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	var docs []memberDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	return decodeMembers(docs)
}

func memberFilter(room uint64, user string) bson.D {
	return bson.D{{Key: "_id", Value: keys.Member(room, user)}}
}

func activeMemberFilter(room uint64, user string) bson.D {
	return append(memberFilter(room, user), bson.E{Key: "state", Value: int64(domain.MemberActive)})
}
