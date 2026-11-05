package mongostore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
	"github.com/ivannguyendev/chatim/pkg/core/mongobound"
)

const deleteGroupBatch = 500

var (
	errGroupHasChildren = fmt.Errorf("%w: group has live child groups", apperr.ErrFailedPrecondition)
	errGroupListedTwice = fmt.Errorf("%w: group listed twice", apperr.ErrInvalidArgument)
)

var afterBatch func(n int)

type idDoc struct {
	ID string `bson:"_id"`
}

func (s *Store) SetUserGroups(ctx context.Context, tenantID, userID string, groupIDs []string, at time.Time, by string) error {
	ids, err := sortedUnique(groupIDs)
	if err != nil {
		return err
	}
	return s.runOnce(ctx, func(ctx context.Context) error {
		if err := s.mustExist(ctx, usersCollection, tenantKey(tenantID, userID)); err != nil {
			return err
		}
		if err := s.touchLiveGroups(ctx, tenantID, ids); err != nil {
			return err
		}
		set := versionedSet(differs("groups", ids), bson.D{{Key: "groups", Value: literal(ids)}}, at, by)
		return s.updateExisting(ctx, usersCollection, tenantKey(tenantID, userID), set)
	})
}

func (s *Store) touchLiveGroups(ctx context.Context, tenantID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, tenantKey(tenantID, id))
	}
	filter := mongobound.StringIDs(keys,
		bson.E{Key: "tenant_id", Value: tenantID}, bson.E{Key: "state", Value: store.GroupLive})
	var live []idDoc
	if err := s.findAll(ctx, groupsCollection, filter, &live, options.Find().SetProjection(bson.D{{Key: "_id", Value: 1}})); err != nil {
		return err
	}
	if len(live) != len(ids) {
		return apperr.ErrNotFound
	}
	_, err := s.db.Collection(groupsCollection).UpdateMany(ctx, filter,
		bson.D{{Key: "$inc", Value: bson.D{{Key: "members_ver", Value: int64(1)}}}})
	return err
}

func (s *Store) DeleteGroup(ctx context.Context, tenantID, id string, at time.Time, by string) (int, error) {
	var doc groupDoc
	if err := s.db.Collection(groupsCollection).FindOne(ctx, bson.D{{Key: "_id", Value: tenantKey(tenantID, id)}}).Decode(&doc); err != nil {
		return 0, s.mapErr(err)
	}
	if doc.State == store.GroupLive {
		err := s.runOnce(ctx, func(ctx context.Context) error { return s.markGroupDeleted(ctx, tenantID, id, at, by) })
		if err != nil {
			return 0, err
		}
	}
	return s.removeGroupFromUsers(ctx, tenantID, id, at, by)
}

func (s *Store) markGroupDeleted(ctx context.Context, tenantID, id string, at time.Time, by string) error {
	if err := s.bumpGroupTree(ctx, tenantID); err != nil {
		return err
	}
	child := bson.D{
		{Key: "tenant_id", Value: tenantID}, {Key: "parent_id", Value: id}, {Key: "state", Value: store.GroupLive},
	}
	err := s.db.Collection(groupsCollection).FindOne(ctx, child, options.FindOne().SetProjection(bson.D{{Key: "_id", Value: 1}})).Err()
	switch {
	case err == nil:
		return errGroupHasChildren
	case !errors.Is(err, mongo.ErrNoDocuments):
		return err
	}
	_, err = s.db.Collection(groupsCollection).UpdateOne(ctx, liveGroupFilter(tenantID, id), bson.D{
		{Key: "$set", Value: bson.D{
			{Key: "state", Value: store.GroupDeleted},
			{Key: "updated_at", Value: at},
			{Key: "updated_by", Value: by},
		}},
		{Key: "$inc", Value: bson.D{{Key: "ver", Value: int64(1)}, {Key: "members_ver", Value: int64(1)}}},
	})
	return err
}

func (s *Store) removeGroupFromUsers(ctx context.Context, tenantID, id string, at time.Time, by string) (int, error) {
	member := bson.E{Key: "groups", Value: id}
	find := options.Find().SetProjection(bson.D{{Key: "_id", Value: 1}}).SetLimit(deleteGroupBatch)
	update := bson.D{
		{Key: "$pull", Value: bson.D{member}},
		{Key: "$inc", Value: bson.D{{Key: "ver", Value: int64(1)}}},
		{Key: "$set", Value: bson.D{{Key: "updated_at", Value: at}, {Key: "updated_by", Value: by}}},
	}
	removed := 0
	for {
		var batch []idDoc
		if err := s.findAll(ctx, usersCollection, bson.D{{Key: "tenant_id", Value: tenantID}, member}, &batch, find); err != nil {
			return removed, err
		}
		if len(batch) == 0 {
			return removed, nil
		}
		keys := make([]string, 0, len(batch))
		for _, u := range batch {
			keys = append(keys, u.ID)
		}
		res, err := s.db.Collection(usersCollection).UpdateMany(ctx, mongobound.StringIDs(keys, member), update)
		if err != nil {
			return removed, s.mapErr(err)
		}
		removed += int(res.ModifiedCount)
		if afterBatch != nil {
			afterBatch(int(res.ModifiedCount))
		}
	}
}

func sortedUnique(ids []string) ([]string, error) {
	sorted := append([]string{}, ids...)
	slices.Sort(sorted)
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[i-1] {
			return nil, errGroupListedTwice
		}
	}
	return sorted, nil
}
